package bridge

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/media"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// unavailablePlaceholder is the text stored for a message that could not be
// decrypted yet. A successful resend has the same id and replaces it.
const unavailablePlaceholder = "[message unavailable: waiting for the sender or your phone to resend it]"

// processMessage stores one message event. Live events and history sync go
// through the same path so that both capture the same fields; only live
// events are published to SSE and webhook subscribers.
func (b *Bridge) processMessage(ctx context.Context, evt *events.Message, live bool) {
	if evt.Message == nil || b.applyUpdate(ctx, evt, live) {
		return
	}
	info, msg := evt.Info, evt.Message
	text := content.Text(msg)
	attachment := content.Media(msg, info.Timestamp)
	if text == "" && attachment.Type == "" {
		return
	}
	chat := b.storedChat(ctx, info.Chat)
	chatJID := chat.String()

	name := b.chatName(ctx, chat, nil, info.Sender.User)
	if err := b.store.SaveChat(ctx, chatJID, name, info.Timestamp); err != nil {
		b.log.Warn("failed to store chat", "err", err)
	}

	stored := store.Message{
		ID:         info.ID,
		ChatJID:    chatJID,
		Sender:     info.Sender.User,
		SenderJID:  info.Sender.ToNonAD().String(),
		SenderName: b.resolveName(ctx, info.Sender, info.SenderAlt, info.PushName),
		Content:    text,
		Timestamp:  info.Timestamp,
		IsFromMe:   info.IsFromMe,
		Media:      attachment,
	}
	if ci := content.ContextInfo(msg); ci != nil {
		stored.QuotedID = ci.GetStanzaID()
		stored.QuotedSender = ci.GetParticipant()
		if ci.GetQuotedMessage() != nil {
			stored.QuotedText = content.QuotedPreview(ci.GetQuotedMessage())
		}
		stored.Mentions = ci.GetMentionedJID()
	}
	isNew := live && !b.store.MessageExists(ctx, info.ID, chatJID)
	if err := b.store.SaveMessage(ctx, stored); err != nil {
		b.log.Warn("failed to store message", "err", err)
		return
	}
	if live && chat.Server != types.BroadcastServer {
		b.countUnread(ctx, chatJID, info.IsFromMe, isNew)
		if isNew {
			if err := b.store.ClearChatDeleted(ctx, chatJID); err != nil {
				b.log.Warn("failed to restore deleted chat", "chat", chatJID, "err", err)
			}
		}
	}
	if poll := content.PollCreation(content.Unwrap(msg)); poll != nil {
		options := make([]string, 0, len(poll.GetOptions()))
		for _, o := range poll.GetOptions() {
			options = append(options, o.GetOptionName())
		}
		if err := b.store.SavePoll(ctx, info.ID, chatJID, poll.GetName(), options, int(poll.GetSelectableOptionsCount())); err != nil {
			b.log.Warn("failed to store poll", "message_id", info.ID, "err", err)
		}
	}
	if web := evt.SourceWebMsg; web != nil {
		b.storeHistoryExtras(ctx, info, chatJID, web)
	}
	if live {
		b.announceMessage(name, stored)
	}
}

// applyUpdate stores a message that changes another one: a reaction, a poll
// vote, an event response, an edit or a deletion. It reports whether msg was
// one; the others are messages of their own.
func (b *Bridge) applyUpdate(ctx context.Context, evt *events.Message, live bool) bool {
	info, msg := evt.Info, evt.Message
	// Reactions arrive as messages of their own. Some chats encrypt them the
	// same way edits are encrypted.
	reaction := msg.GetReactionMessage()
	if reaction == nil && msg.GetEncReactionMessage() != nil {
		decrypted, err := b.client.DecryptReaction(ctx, evt)
		if err != nil {
			b.log.Warn("failed to decrypt reaction", "message_id", info.ID, "err", err)
			return true
		}
		reaction = decrypted
	}
	if reaction != nil {
		b.storeReaction(ctx, store.Reaction{
			MessageID:  reaction.GetKey().GetID(),
			ChatJID:    b.chatOf(ctx, info.Chat, reaction.GetKey().GetID()),
			SenderJID:  info.Sender.ToNonAD().String(),
			SenderName: b.resolveName(ctx, info.Sender, info.SenderAlt, info.PushName),
			Emoji:      reaction.GetText(),
			Timestamp:  info.Timestamp,
		}, live)
		return true
	}

	// Poll votes are encrypted with the poll's secret and name the chosen
	// options only by hash.
	if msg.GetPollUpdateMessage() != nil {
		b.onPollVote(ctx, evt, live)
		return true
	}
	if msg.GetEncEventResponseMessage() != nil {
		b.onEventResponse(ctx, evt, live)
		return true
	}

	// Edits: newer clients send the new text encrypted in a separate
	// SecretEncryptedMessage that points at the original through
	// TargetMessageKey. info.ID is the id of the edit event itself, never the
	// edited message.
	if enc := msg.GetSecretEncryptedMessage(); enc != nil {
		if enc.GetSecretEncType() != waE2E.SecretEncryptedMessage_MESSAGE_EDIT {
			return true
		}
		decrypted, err := b.client.DecryptSecretEncryptedMessage(ctx, evt)
		if err != nil {
			b.log.Warn("failed to decrypt edit", "message_id", enc.GetTargetMessageKey().GetID(), "err", err)
			return true
		}
		target := enc.GetTargetMessageKey().GetID()
		b.applyEdit(ctx, b.chatOf(ctx, info.Chat, target), target, editedText(decrypted), info.Timestamp, b.messageAuthor(ctx, info), live)
		return true
	}
	if pm := msg.GetProtocolMessage(); pm != nil {
		target := pm.GetKey().GetID()
		switch pm.GetType() {
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			b.applyEdit(ctx, b.chatOf(ctx, info.Chat, target), target, content.Text(pm.GetEditedMessage()), info.Timestamp, b.messageAuthor(ctx, info), live)
		case waE2E.ProtocolMessage_REVOKE:
			b.applyRevoke(ctx, info.Chat, b.chatOf(ctx, info.Chat, target), target, info.Timestamp, info, live)
		}
		return true
	}
	return false
}

// storeHistoryExtras stores what history sync carries inside a message:
// its reactions, poll votes, event responses and star.
func (b *Bridge) storeHistoryExtras(ctx context.Context, info types.MessageInfo, chatJID string, web *waWeb.WebMessageInfo) {
	for _, r := range web.GetReactions() {
		sender := b.keySender(info.Chat, r.GetKey())
		b.storeReaction(ctx, store.Reaction{
			MessageID:  info.ID,
			ChatJID:    chatJID,
			SenderJID:  sender.ToNonAD().String(),
			SenderName: b.resolveName(ctx, sender, types.EmptyJID, ""),
			Emoji:      r.GetText(),
			Timestamp:  time.UnixMilli(r.GetSenderTimestampMS()),
		}, false)
	}
	b.storeHistoryPollVotes(ctx, info.Chat, info.ID, web)
	b.storeHistoryEventResponses(ctx, info.Chat, info.ID, web)
	if web.GetStarred() {
		if err := b.store.SetStarred(ctx, info.ID, chatJID, true); err != nil {
			b.log.Warn("failed to store star", "message_id", info.ID, "err", err)
		}
	}
}

// announceMessage queues a new message's attachment for download, echoes
// the message to the log and publishes it.
func (b *Bridge) announceMessage(chatName string, m store.Message) {
	b.autoDownload.Offer(media.Job{MessageID: m.ID, ChatJID: m.ChatJID}, m.Media)
	direction := "←"
	if m.IsFromMe {
		direction = "→"
	}
	echo := []any{"at", m.Timestamp.Format(time.DateTime), "direction", direction, "sender", m.SenderName}
	if m.Media.Type != "" {
		echo = append(echo, "media_type", m.Media.Type, "file", m.Media.Filename)
	}
	b.log.Info("message", append(echo, "text", m.Content)...)
	b.events.Publish(hub.Event{
		Type: "message", ChatJID: m.ChatJID, ChatName: chatName, MessageID: m.ID,
		SenderJID: m.SenderJID, SenderName: m.SenderName, IsFromMe: m.IsFromMe,
		Content: m.Content, MediaType: m.Media.Type, QuotedID: m.QuotedID, Timestamp: m.Timestamp,
	})
}

// onUndecryptable keeps a trace of a message that failed to decrypt.
// Business senders (couriers, banks, one-time codes) often arrive this way:
// their primary device does not honour the automatic retry, and before this
// the message left no row at all. whatsmeow asks the sender to resend, and
// with AutomaticMessageRerequestFromPhone also asks our phone; either answer
// arrives as a normal message event with the same id.
func (b *Bridge) onUndecryptable(ctx context.Context, evt *events.UndecryptableMessage) {
	info := evt.Info
	// "hide" marks messages WhatsApp itself does not show (e.g. some
	// protocol messages), and our own messages arrive on the phone anyway.
	if evt.DecryptFailMode == events.DecryptFailHide || info.IsFromMe {
		return
	}
	chat := b.storedChat(ctx, info.Chat)
	chatJID := chat.String()
	name := b.chatName(ctx, chat, nil, info.Sender.User)
	if err := b.store.SaveChat(ctx, chatJID, name, info.Timestamp); err != nil {
		b.log.Warn("failed to store chat", "err", err)
	}
	stored := store.Message{
		ID:         info.ID,
		ChatJID:    chatJID,
		Sender:     info.Sender.User,
		SenderJID:  info.Sender.ToNonAD().String(),
		SenderName: b.resolveName(ctx, info.Sender, info.SenderAlt, info.PushName),
		Content:    unavailablePlaceholder,
		Timestamp:  info.Timestamp,
	}
	inserted, err := b.store.SavePlaceholder(ctx, stored)
	if err != nil {
		b.log.Warn("failed to store placeholder", "message_id", info.ID, "err", err)
		return
	}
	if !inserted {
		return
	}
	if chat.Server != types.BroadcastServer {
		b.countUnread(ctx, chatJID, false, true)
	}
	b.log.Warn("message could not be decrypted; resend requested", "message_id", info.ID, "sender", stored.SenderName, "chat", chatJID)
	b.events.Publish(hub.Event{
		Type: "unavailable", ChatJID: chatJID, ChatName: name, MessageID: info.ID,
		SenderJID: stored.SenderJID, SenderName: stored.SenderName, Content: unavailablePlaceholder, Timestamp: info.Timestamp,
	})
}

// editedText finds the text of a decrypted edit. Depending on the client that
// made it, the new content sits at different depths.
func editedText(msg *waE2E.Message) string {
	if text := content.Text(msg); text != "" {
		return text
	}
	if text := content.Text(msg.GetEditedMessage().GetMessage()); text != "" {
		return text
	}
	return content.Text(msg.GetProtocolMessage().GetEditedMessage())
}

// chatOf is the chat a message targeted by a reaction, edit, deletion or
// vote is stored in: chat itself, or for a direct chat its phone-number or
// LID form.
func (b *Bridge) chatOf(ctx context.Context, chat types.JID, messageID string) string {
	if chat.Server == types.GroupServer {
		return chat.String()
	}
	aliases := b.jidAliases(ctx, chat)
	names := make([]string, len(aliases))
	for i, a := range aliases {
		names[i] = a.String()
	}
	return b.store.ResolveChat(ctx, messageID, names...)
}

// messageAuthor is who sent an edit or deletion, in every form their JID is
// known by.
func (b *Bridge) messageAuthor(ctx context.Context, info types.MessageInfo) *store.Author {
	if info.IsFromMe {
		return &store.Author{FromMe: true}
	}
	author := &store.Author{}
	for _, jid := range []types.JID{info.Sender, info.SenderAlt} {
		if jid.IsEmpty() {
			continue
		}
		for _, alias := range b.jidAliases(ctx, jid) {
			author.JIDs = append(author.JIDs, alias.String())
		}
	}
	return author
}

func (b *Bridge) applyEdit(ctx context.Context, chatJID, targetID, content string, at time.Time, author *store.Author, live bool) {
	if targetID == "" || content == "" {
		b.log.Warn("ignoring edit without usable text", "message_id", targetID)
		return
	}
	found, err := b.store.EditMessage(ctx, targetID, chatJID, content, at, author)
	if err != nil {
		b.log.Warn("failed to store edit", "message_id", targetID, "err", err)
		return
	}
	if !found {
		b.log.Warn("ignoring edit of an unknown message, or not one of the editor's", "message_id", targetID)
		return
	}
	if live {
		b.log.Info("message edited", "at", at.Format(time.DateTime), "message_id", targetID, "text", content)
		b.events.Publish(hub.Event{Type: "edit", ChatJID: chatJID, MessageID: targetID, Content: content, Timestamp: at})
	}
}

// applyRevoke marks a message deleted when its author deleted it, or a group
// admin did.
func (b *Bridge) applyRevoke(ctx context.Context, chat types.JID, chatJID, targetID string, at time.Time, info types.MessageInfo, live bool) {
	if targetID == "" {
		return
	}
	found, err := b.store.MarkDeleted(ctx, targetID, chatJID, at, b.messageAuthor(ctx, info))
	if err == nil && !found && !info.IsFromMe && chat.Server == types.GroupServer && b.isGroupAdmin(ctx, chat, info.Sender) {
		found, err = b.store.MarkDeleted(ctx, targetID, chatJID, at, nil)
	}
	if err != nil {
		b.log.Warn("failed to store deletion", "message_id", targetID, "err", err)
		return
	}
	if found && live {
		b.log.Info("message deleted", "at", at.Format(time.DateTime), "message_id", targetID)
		b.events.Publish(hub.Event{Type: "delete", ChatJID: chatJID, MessageID: targetID, Timestamp: at})
	}
}

func (b *Bridge) storeReaction(ctx context.Context, r store.Reaction, live bool) {
	if r.MessageID == "" {
		return
	}
	if err := b.store.SaveReaction(ctx, r); err != nil {
		b.log.Warn("failed to store reaction", "err", err)
		return
	}
	if live {
		b.log.Info("reaction", "at", r.Timestamp.Format(time.DateTime), "emoji", r.Emoji, "sender", r.SenderName, "message_id", r.MessageID)
		b.events.Publish(hub.Event{
			Type: "reaction", ChatJID: r.ChatJID, MessageID: r.MessageID, SenderJID: r.SenderJID,
			SenderName: r.SenderName, Emoji: r.Emoji, Timestamp: r.Timestamp,
		})
	}
}

// isGroupAdmin reports whether user is an admin of group, asking WhatsApp.
func (b *Bridge) isGroupAdmin(ctx context.Context, group, user types.JID) bool {
	if b.client == nil || !b.Online() {
		return false
	}
	info, err := b.client.GetGroupInfo(ctx, group)
	if err != nil {
		return false
	}
	user = user.ToNonAD()
	for _, p := range info.Participants {
		if p.JID == user || p.LID == user || p.PhoneNumber == user {
			return p.IsAdmin || p.IsSuperAdmin
		}
	}
	return false
}
