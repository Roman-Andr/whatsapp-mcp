package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/media"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// Send sends text and/or a file, then stores the sent message so it
// shows up in the chat history like any other (whatsmeow does not echo
// messages sent by this device back as events).
func (b *Bridge) Send(ctx context.Context, req SendMessageRequest) SendMessageResponse {
	chat, err := jid.ParseRecipient(req.Recipient)
	if err != nil {
		return failed("Error parsing recipient: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	mediaPath := req.MediaPath
	if mediaPath != "" {
		if mediaPath, err = b.mediaRoots.Resolve(mediaPath); err != nil {
			if errors.Is(err, policy.ErrOutsideMediaRoots) {
				return blockedResponse(err)
			}
			return failed("%v", err)
		}
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}

	text := req.Message
	ci := &waE2E.ContextInfo{}
	hasContext := false
	var quoted *store.MessageRef

	if req.ReplyToMessageID != "" {
		// A DM can be stored under the contact's LID while the caller
		// addressed it by phone number. Other chats are never searched: a
		// quote embeds the quoted text, which must not leak across chats.
		quoted, err = b.store.Ref(ctx, req.ReplyToMessageID, b.chatOf(ctx, chat, req.ReplyToMessageID))
		if err != nil {
			return failed("Message to reply to not found: %s", req.ReplyToMessageID)
		}
		participant, err := b.authorJID(ctx, quoted)
		if err != nil {
			return failed("Cannot determine the author of %s: %v", quoted.ID, err)
		}
		ci.StanzaID = new(quoted.ID)
		ci.Participant = new(participant.String())
		ci.QuotedMessage = &waE2E.Message{Conversation: new(quoted.Content)}
		hasContext = true
	}

	if len(req.Mentions) > 0 {
		var mentioned []string
		text, mentioned, err = b.applyMentions(ctx, chat, text, req.Mentions)
		if err != nil {
			return failed("Error resolving mentions: %v", err)
		}
		ci.MentionedJID = mentioned
		hasContext = true
	}
	if !hasContext {
		ci = nil
	}

	msg, media, err := b.buildMessage(ctx, chat, req.Sticker, mediaPath, text, ci)
	if err != nil {
		return failed("%v", err)
	}

	resp, err := b.client.SendMessage(ctx, chat, msg)
	if err != nil {
		return failed("Error sending message: %v", err)
	}

	stored := store.Message{Content: text, Media: media}
	if ci != nil {
		stored.QuotedID = ci.GetStanzaID()
		stored.QuotedSender = ci.GetParticipant()
		if quoted != nil {
			stored.QuotedText = quoted.Content
		}
		stored.Mentions = ci.GetMentionedJID()
	}
	b.recordSent(ctx, chat, resp, stored)

	return SendMessageResponse{
		Success: true, Message: fmt.Sprintf("Message sent to %s", req.Recipient),
		MessageID: resp.ID, Timestamp: resp.Timestamp,
	}
}

// buildMessage builds what Send sends: a sticker, a file with text as its
// caption, a text status, or plain text, quoting and mentioning through ci.
func (b *Bridge) buildMessage(ctx context.Context, chat types.JID, sticker bool, mediaPath, text string, ci *waE2E.ContextInfo) (*waE2E.Message, content.MediaInfo, error) {
	msg := &waE2E.Message{}
	switch {
	case sticker:
		media, err := b.buildStickerMessage(ctx, msg, mediaPath, ci)
		return msg, media, err
	case mediaPath != "":
		media, err := b.buildMediaMessage(ctx, msg, mediaPath, text, ci)
		return msg, media, err
	case chat == types.StatusBroadcastJID:
		return statusTextMessage(text), content.MediaInfo{}, nil
	case ci != nil:
		msg.ExtendedTextMessage = &waE2E.ExtendedTextMessage{Text: new(text), ContextInfo: ci}
	default:
		msg.Conversation = new(text)
	}
	return msg, content.MediaInfo{}, nil
}

// recordSent stores a message this bridge sent and publishes it, so it shows
// up in the chat history like any other (whatsmeow does not echo messages
// sent by this device back as events). stored carries the content; the
// identity fields are filled in here.
func (b *Bridge) recordSent(ctx context.Context, chat types.JID, resp whatsmeow.SendResponse, stored store.Message) {
	// The message is out: recording it must not depend on the caller still
	// waiting for the answer.
	ctx = context.WithoutCancel(ctx)
	own := b.ownJID(chat)
	chat = b.storedChat(ctx, chat)
	stored.ID, stored.ChatJID, stored.Timestamp, stored.IsFromMe = resp.ID, chat.String(), resp.Timestamp, true
	stored.Sender, stored.SenderJID, stored.SenderName = own.User, own.String(), b.client.PushName()
	name := b.chatName(ctx, chat, nil, "")
	if err := b.store.SaveChat(ctx, chat.String(), name, resp.Timestamp); err != nil {
		b.log.Warn("failed to store chat of sent message", "err", err)
	}
	if err := b.store.SaveMessage(ctx, stored); err != nil {
		b.log.Warn("failed to store sent message", "err", err)
	}
	b.events.Publish(hub.Event{
		Type: "message", ChatJID: chat.String(), ChatName: name, MessageID: resp.ID, SenderJID: own.String(),
		IsFromMe: true, Content: stored.Content, MediaType: stored.Media.Type, QuotedID: stored.QuotedID, Timestamp: resp.Timestamp,
	})
}

// authorJID is the full JID of the author of a stored message, as needed for
// a reply's Participant and a reaction's key.
func (b *Bridge) authorJID(ctx context.Context, ref *store.MessageRef) (types.JID, error) {
	chat, err := types.ParseJID(ref.ChatJID)
	if err != nil {
		return types.EmptyJID, err
	}
	if ref.IsFromMe {
		return b.ownJID(chat), nil
	}
	if ref.SenderJID != "" {
		return types.ParseJID(ref.SenderJID)
	}
	if chat.Server != types.GroupServer {
		return chat, nil
	}
	if ref.Sender == "" || ref.Sender == chat.User {
		return types.EmptyJID, fmt.Errorf("sender of group message %s was not recorded", ref.ID)
	}
	// Rows stored before sender_jid existed only have the user part. Group
	// members are addressed by LID unless the group still uses phone numbers.
	server := types.HiddenUserServer
	if info, err := b.client.GetGroupInfo(ctx, chat); err == nil && info.AddressingMode == types.AddressingModePN {
		server = types.DefaultUserServer
	}
	return types.NewJID(ref.Sender, server), nil
}

// applyMentions resolves mention targets and makes the text reference them.
// WhatsApp renders a mention only when the text contains "@<user>" for a JID
// listed in MentionedJID; in LID-addressed groups that user is the LID.
func (b *Bridge) applyMentions(ctx context.Context, chat types.JID, text string, mentions []string) (string, []string, error) {
	useLID := false
	if chat.Server == types.GroupServer {
		if info, err := b.client.GetGroupInfo(ctx, chat); err == nil {
			useLID = info.AddressingMode == types.AddressingModeLID
		}
	}
	var jids []string
	for _, m := range mentions {
		target, err := jid.ParseRecipient(m)
		if err != nil {
			return "", nil, err
		}
		if useLID && target.Server == types.DefaultUserServer {
			if lid, err := b.client.LIDs().GetLIDForPN(ctx, target); err == nil && !lid.IsEmpty() {
				original := target.User
				target = lid
				text = strings.ReplaceAll(text, "@"+original, "@"+target.User)
			}
		}
		if !strings.Contains(text, "@"+target.User) {
			text = strings.TrimSpace(text + " @" + target.User)
		}
		jids = append(jids, target.String())
	}
	return text, jids, nil
}

// buildMediaMessage uploads a file and fills the matching message type.
func (b *Bridge) buildMediaMessage(ctx context.Context, msg *waE2E.Message, mediaPath, caption string, ci *waE2E.ContextInfo) (content.MediaInfo, error) {
	if !filepath.IsAbs(mediaPath) {
		return content.MediaInfo{}, fmt.Errorf("media path must be absolute: %s", mediaPath)
	}
	st, err := os.Stat(mediaPath)
	if err != nil {
		return content.MediaInfo{}, fmt.Errorf("Error reading media file: %w", err) //nolint:staticcheck // the capitalised text is the API response
	}
	if !st.Mode().IsRegular() {
		return content.MediaInfo{}, fmt.Errorf("media path is not a regular file: %s", mediaPath)
	}
	if isStoreSecret(b.storeDir, mediaPath) {
		return content.MediaInfo{}, fmt.Errorf("refusing to send %s: files in the bridge's store directory hold the session, messages and API token", mediaPath)
	}
	mediaData, err := os.ReadFile(mediaPath) //nolint:gosec // G304: mediaPath passed the media roots and the store check above
	if err != nil {
		return content.MediaInfo{}, fmt.Errorf("Error reading media file: %w", err) //nolint:staticcheck // the capitalised text is the API response
	}

	mimeType := content.MIMEType(mediaPath)
	fileName := filepath.Base(mediaPath)
	var mediaType whatsmeow.MediaType
	var kind string
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		mediaType, kind = whatsmeow.MediaImage, "image"
	case mimeType == "audio/ogg; codecs=opus":
		mediaType, kind = whatsmeow.MediaAudio, "audio"
	case strings.HasPrefix(mimeType, "video/"):
		mediaType, kind = whatsmeow.MediaVideo, "video"
	default:
		mediaType, kind = whatsmeow.MediaDocument, "document"
	}

	resp, err := b.client.Upload(ctx, mediaData, mediaType)
	if err != nil {
		return content.MediaInfo{}, fmt.Errorf("Error uploading media: %w", err) //nolint:staticcheck // the capitalised text is the API response
	}
	info := content.MediaInfo{
		Type: kind, Filename: fileName, Mimetype: mimeType, URL: resp.URL, DirectPath: resp.DirectPath,
		MediaKey: resp.MediaKey, FileSHA256: resp.FileSHA256, FileEncSHA256: resp.FileEncSHA256, FileLength: resp.FileLength,
	}

	switch kind {
	case "image":
		msg.ImageMessage = &waE2E.ImageMessage{
			Caption: new(caption), Mimetype: new(mimeType),
			URL: &resp.URL, DirectPath: &resp.DirectPath, MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: &resp.FileLength,
			ContextInfo: ci,
		}
	case "audio":
		seconds, waveform, err := media.AnalyzeOggOpus(mediaData, b.log)
		if err != nil {
			return content.MediaInfo{}, fmt.Errorf("failed to analyze Ogg Opus file: %w", err)
		}
		msg.AudioMessage = &waE2E.AudioMessage{
			Mimetype: new(mimeType), URL: &resp.URL, DirectPath: &resp.DirectPath, MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: &resp.FileLength,
			Seconds: new(seconds), PTT: new(true), Waveform: waveform, ContextInfo: ci,
		}
	case "video":
		msg.VideoMessage = &waE2E.VideoMessage{
			Caption: new(caption), Mimetype: new(mimeType),
			URL: &resp.URL, DirectPath: &resp.DirectPath, MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: &resp.FileLength,
			ContextInfo: ci,
		}
	default:
		// FileName is what the recipient sees and what gives the document a
		// preview; without it the file arrives as an untitled .bin.
		msg.DocumentMessage = &waE2E.DocumentMessage{
			Title: new(fileName), FileName: new(fileName),
			Caption: new(caption), Mimetype: new(mimeType),
			URL: &resp.URL, DirectPath: &resp.DirectPath, MediaKey: resp.MediaKey,
			FileEncSHA256: resp.FileEncSHA256, FileSHA256: resp.FileSHA256, FileLength: &resp.FileLength,
			ContextInfo: ci,
		}
	}
	return info, nil
}

// React reacts to a stored message; an empty emoji removes our reaction.
func (b *Bridge) React(ctx context.Context, req ReactRequest) SendMessageResponse {
	ref, chat, refused := b.targetMessage(ctx, req.ChatJID, req.MessageID)
	if ref == nil {
		return refused
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	author, err := b.authorJID(ctx, ref)
	if err != nil {
		return failed("Cannot determine the author of %s: %v", ref.ID, err)
	}

	resp, err := b.client.SendMessage(ctx, chat, b.client.BuildReaction(chat, author, ref.ID, req.Emoji))
	if err != nil {
		return failed("Error sending reaction: %v", err)
	}
	b.storeReaction(ctx, store.Reaction{
		MessageID: ref.ID, ChatJID: ref.ChatJID, SenderJID: b.ownJID(chat).String(),
		SenderName: b.client.PushName(), Emoji: req.Emoji, Timestamp: resp.Timestamp,
	}, true)

	verb := "Reacted " + req.Emoji
	if req.Emoji == "" {
		verb = "Removed reaction"
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("%s on %s", verb, ref.ID), MessageID: resp.ID, Timestamp: resp.Timestamp}
}

// targetMessage looks up a stored message that a call acts on (reacting,
// editing, deleting, marking read) and checks that its chat may be changed.
// On failure ref is nil and resp says why.
func (b *Bridge) targetMessage(ctx context.Context, chatJID, messageID string) (ref *store.MessageRef, chat types.JID, resp SendMessageResponse) {
	ref, err := b.store.Ref(ctx, messageID, chatJID)
	if err != nil {
		return nil, chat, failed("Message %s not found in chat %s", messageID, chatJID)
	}
	chat, err = types.ParseJID(ref.ChatJID)
	if err != nil {
		return nil, chat, failed("Invalid chat JID: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return nil, chat, blockedResponse(err)
	}
	return ref, chat, SendMessageResponse{}
}

// History request sizes: the default is one screenful of a chat, and the
// phone answers larger requests only partly, so more than maxHistoryCount
// would just be pages that never arrive.
const (
	defaultHistoryCount = 50
	maxHistoryCount     = 500
)

// RequestHistory asks the phone for messages older than the oldest one stored
// for a chat. The answer arrives later as an ON_DEMAND history sync, which
// onHistorySync stores like any other batch.
func (b *Bridge) RequestHistory(ctx context.Context, req HistoryRequest) SendMessageResponse {
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	count := req.Count
	if count <= 0 {
		count = defaultHistoryCount
	}
	count = min(count, maxHistoryCount)
	var oldest *store.MessageRef
	var err error
	if req.BeforeMessageID != "" {
		oldest, err = b.store.Ref(ctx, req.BeforeMessageID, req.ChatJID)
		if err != nil {
			return failed("Message %s not found in %s", req.BeforeMessageID, req.ChatJID)
		}
	} else if oldest, err = b.store.OldestRef(ctx, req.ChatJID); err != nil {
		return failed("No stored messages for %s to page back from", req.ChatJID)
	}
	chat, err := types.ParseJID(oldest.ChatJID)
	if err != nil {
		return failed("Invalid chat JID: %v", err)
	}
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, IsFromMe: oldest.IsFromMe, IsGroup: chat.Server == types.GroupServer},
		ID:            oldest.ID,
		Timestamp:     oldest.Timestamp,
	}
	if _, err := b.client.SendPeerMessage(ctx, b.client.BuildHistorySyncRequest(info, count)); err != nil {
		return failed("Error requesting history: %v", err)
	}
	return SendMessageResponse{
		Success: true,
		Message: fmt.Sprintf("Requested up to %d messages before %s (%s). They arrive asynchronously, usually within a minute, and only if the phone is online.",
			count, oldest.Timestamp.Format(time.RFC3339), oldest.ID),
	}
}

// isStoreSecret reports whether path is a file directly in the store
// directory storeDir (the session database, messages.db, api_token,
// audit.log), which a prompt-injected agent could otherwise send away.
// Downloaded media lives in subdirectories and may be forwarded.
func isStoreSecret(storeDir, path string) bool {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return true
	}
	storeDir, err = filepath.Abs(storeDir)
	if err != nil {
		return true
	}
	if target, err := filepath.EvalSymlinks(storeDir); err == nil {
		storeDir = target
	}
	return filepath.Dir(resolved) == storeDir
}
