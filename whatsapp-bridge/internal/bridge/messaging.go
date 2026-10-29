package bridge

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// VotePoll votes in a poll, or withdraws the vote with no options.
func (b *Bridge) VotePoll(ctx context.Context, req VoteRequest) SendMessageResponse {
	ref, chat, refused := b.targetMessage(ctx, req.ChatJID, req.MessageID)
	if ref == nil {
		return refused
	}
	options, err := b.store.PollOptions(ctx, ref.ID, ref.ChatJID)
	if err != nil {
		return failed("Message %s is not a poll the bridge knows", ref.ID)
	}
	known := map[string]bool{}
	for _, o := range options {
		known[o] = true
	}
	for _, o := range req.Options {
		if !known[o] {
			return failed("%q is not an option of this poll (options: %s)", o, strings.Join(options, " / "))
		}
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	author, err := b.authorJID(ctx, ref)
	if err != nil {
		return failed("Cannot determine the author of poll %s: %v", ref.ID, err)
	}
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, Sender: author, IsFromMe: ref.IsFromMe, IsGroup: chat.Server == types.GroupServer},
		ID:            ref.ID,
	}
	vote, err := b.client.BuildPollVote(ctx, info, req.Options)
	if err != nil {
		return failed("Cannot vote in poll %s: %v", ref.ID, err)
	}
	resp, err := b.client.SendMessage(ctx, chat, vote)
	if err != nil {
		return failed("Error sending vote: %v", err)
	}
	own := b.ownJID(chat)
	if err := b.store.SavePollVote(ctx, store.PollVote{
		MessageID: ref.ID, ChatJID: ref.ChatJID, VoterJID: own.String(), VoterName: b.client.PushName(),
		Options: req.Options, Timestamp: resp.Timestamp,
	}); err != nil {
		b.log.Warn("failed to store own vote", "err", err)
	}
	what := "Voted " + strings.Join(req.Options, ", ")
	if len(req.Options) == 0 {
		what = "Withdrew vote"
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("%s in poll %s", what, ref.ID), MessageID: resp.ID, Timestamp: resp.Timestamp}
}

// vcardFor builds a minimal vCard for a name and phone number.
func vcardFor(name, phone string) (string, error) {
	digits := strings.TrimPrefix(strings.TrimSpace(phone), "+")
	digits = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(digits)
	if digits == "" || strings.Trim(digits, "0123456789") != "" {
		return "", fmt.Errorf("phone %q is not a phone number", phone)
	}
	name = strings.NewReplacer("\n", " ", "\r", " ").Replace(strings.TrimSpace(name))
	if name == "" {
		return "", fmt.Errorf("name is required")
	}
	return fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nFN:%s\nTEL;type=CELL;waid=%s:+%s\nEND:VCARD", name, digits, digits), nil
}

// SendContact sends a contact card, built from name and phone or given as a vCard.
func (b *Bridge) SendContact(ctx context.Context, req ContactRequest) SendMessageResponse {
	vcard := strings.TrimSpace(req.VCard)
	name := strings.TrimSpace(req.Name)
	if vcard == "" {
		var err error
		if vcard, err = vcardFor(req.Name, req.Phone); err != nil {
			return failed("%v", err)
		}
	} else if !strings.HasPrefix(strings.ToUpper(vcard), "BEGIN:VCARD") {
		return failed("vcard must start with BEGIN:VCARD")
	}
	if name == "" {
		name = "Contact"
	}
	chat, err := jid.ParseRecipient(req.Recipient)
	if err != nil {
		return failed("Error parsing recipient: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	msg := &waE2E.Message{ContactMessage: &waE2E.ContactMessage{DisplayName: new(name), Vcard: new(vcard)}}
	resp, err := b.client.SendMessage(ctx, chat, msg)
	if err != nil {
		return failed("Error sending contact: %v", err)
	}
	b.recordSent(ctx, chat, resp, store.Message{Content: content.Text(msg)})
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Contact %s sent to %s", name, req.Recipient), MessageID: resp.ID, Timestamp: resp.Timestamp}
}

// Forward sends a stored message to another chat, marked forwarded as
// WhatsApp does. Media is downloaded and uploaded again, which also works
// when the original's CDN copy is about to expire.
func (b *Bridge) Forward(ctx context.Context, req ForwardRequest) SendMessageResponse {
	if err := b.CheckReadable(ctx, req.ChatJID); err != nil {
		return blockedResponse(err)
	}
	ref, err := b.store.Ref(ctx, req.MessageID, req.ChatJID)
	if err != nil {
		return failed("Message %s not found in chat %s", req.MessageID, req.ChatJID)
	}
	if ref.Deleted {
		return failed("Message %s was deleted by its sender", ref.ID)
	}
	chat, err := jid.ParseRecipient(req.Recipient)
	if err != nil {
		return failed("Error parsing recipient: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	ci := &waE2E.ContextInfo{IsForwarded: new(true), ForwardingScore: proto.Uint32(1)}
	msg := &waE2E.Message{}
	var media content.MediaInfo
	switch ref.MediaType {
	case "":
		if ref.Content == "" {
			return failed("Message %s has nothing to forward", ref.ID)
		}
		msg.ExtendedTextMessage = &waE2E.ExtendedTextMessage{Text: new(ref.Content), ContextInfo: ci}
	default:
		dl, err := b.DownloadMedia(ctx, ref.ID, ref.ChatJID)
		if err != nil {
			return failed("Cannot forward %s: downloading its %s failed: %v", ref.ID, ref.MediaType, err)
		}
		path, err := filepath.Abs(dl.Path)
		if err != nil {
			return failed("%v", err)
		}
		if ref.MediaType == "sticker" {
			media, err = b.buildStickerMessage(ctx, msg, path, ci)
		} else {
			media, err = b.buildMediaMessage(ctx, msg, path, ref.Content, ci)
		}
		if err != nil {
			return failed("%v", err)
		}
	}
	resp, err := b.client.SendMessage(ctx, chat, msg)
	if err != nil {
		return failed("Error forwarding message: %v", err)
	}
	b.recordSent(ctx, chat, resp, store.Message{Content: ref.Content, Media: media})
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Forwarded %s to %s", ref.ID, req.Recipient), MessageID: resp.ID, Timestamp: resp.Timestamp}
}

// buildStickerMessage uploads a .webp file as a sticker.
func (b *Bridge) buildStickerMessage(ctx context.Context, msg *waE2E.Message, path string, ci *waE2E.ContextInfo) (content.MediaInfo, error) {
	if !strings.EqualFold(filepath.Ext(path), ".webp") {
		return content.MediaInfo{}, fmt.Errorf("stickers must be .webp files (send_sticker converts other images)")
	}
	tmp := &waE2E.Message{}
	info, err := b.buildMediaMessage(ctx, tmp, path, "", nil)
	if err != nil {
		return content.MediaInfo{}, err
	}
	img := tmp.GetImageMessage()
	if img == nil {
		return content.MediaInfo{}, fmt.Errorf("%s is not an image", path)
	}
	msg.StickerMessage = &waE2E.StickerMessage{
		URL: img.URL, DirectPath: img.DirectPath, MediaKey: img.GetMediaKey(), Mimetype: new("image/webp"),
		FileEncSHA256: img.GetFileEncSHA256(), FileSHA256: img.GetFileSHA256(), FileLength: img.FileLength, ContextInfo: ci,
	}
	info.Type = "sticker"
	return info, nil
}

// Star stars or unstars a message on every device of the account.
func (b *Bridge) Star(ctx context.Context, req StarRequest) SendMessageResponse {
	ref, chat, refused := b.targetMessage(ctx, req.ChatJID, req.MessageID)
	if ref == nil {
		return refused
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	sender := types.EmptyJID
	if !ref.IsFromMe && chat.Server == types.GroupServer {
		var err error
		if sender, err = b.authorJID(ctx, ref); err != nil {
			return failed("Cannot determine the author of %s: %v", ref.ID, err)
		}
	}
	if err := b.client.SendAppState(ctx, appstate.BuildStar(chat, sender, ref.ID, ref.IsFromMe, req.Starred)); err != nil {
		return failed("Failed to star %s: %v", ref.ID, err)
	}
	if err := b.store.SetStarred(ctx, ref.ID, ref.ChatJID, req.Starred); err != nil {
		b.log.Warn("failed to record star", "message_id", ref.ID, "err", err)
	}
	verb := "Starred"
	if !req.Starred {
		verb = "Unstarred"
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("%s %s", verb, ref.ID)}
}

func (b *Bridge) onStar(ctx context.Context, evt *events.Star) {
	chatJID := b.chatOf(ctx, evt.ChatJID, evt.MessageID)
	if err := b.store.SetStarred(ctx, evt.MessageID, chatJID, evt.Action.GetStarred()); err != nil {
		b.log.Warn("failed to record star", "message_id", evt.MessageID, "err", err)
	}
}
