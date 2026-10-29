package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// markReadRecent is how many of a chat's latest incoming messages MarkRead
// acknowledges when no ids are given: enough to clear the unread badge,
// which only counts back to the last read message.
const markReadRecent = 20

// MarkRead sends read receipts. WhatsApp takes one receipt per sender, so
// the messages are grouped by author.
func (b *Bridge) MarkRead(ctx context.Context, req ReadRequest) SendMessageResponse {
	var refs []*store.MessageRef
	if len(req.MessageIDs) == 0 {
		recent, err := b.store.RecentIncoming(ctx, req.ChatJID, markReadRecent)
		if err != nil {
			return failed("Failed to read messages of %s: %v", req.ChatJID, err)
		}
		if len(recent) == 0 {
			return failed("No incoming messages stored for %s", req.ChatJID)
		}
		refs = recent
	} else {
		for _, id := range req.MessageIDs {
			ref, err := b.store.Ref(ctx, id, req.ChatJID)
			if err != nil {
				return failed("Message %s not found in chat %s", id, req.ChatJID)
			}
			refs = append(refs, ref)
		}
	}
	chat, err := types.ParseJID(refs[0].ChatJID)
	if err != nil {
		return failed("Invalid chat JID: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}

	bySender := map[types.JID][]types.MessageID{}
	var order []types.JID
	var skipped []string
	for _, ref := range refs {
		if ref.IsFromMe {
			continue
		}
		sender, err := b.authorJID(ctx, ref)
		if err != nil {
			// Old rows may lack the sender; acknowledge the others anyway.
			skipped = append(skipped, ref.ID)
			continue
		}
		if _, seen := bySender[sender]; !seen {
			order = append(order, sender)
		}
		bySender[sender] = append(bySender[sender], ref.ID)
	}
	if len(order) == 0 {
		if len(skipped) > 0 {
			return failed("Cannot mark %s as read: their senders were not recorded", strings.Join(skipped, ", "))
		}
		return failed("Nothing to mark as read: the messages are your own")
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	count := 0
	for _, sender := range order {
		ids := bySender[sender]
		if err := b.client.MarkRead(ctx, ids, time.Now(), chat, sender); err != nil {
			return failed("Error sending read receipt (after %d of %d messages): %v", count, len(refs), err)
		}
		count += len(ids)
	}
	if err := b.store.SetUnread(ctx, refs[0].ChatJID, 0); err != nil {
		b.log.Warn("failed to clear unread count", "chat", refs[0].ChatJID, "err", err)
	}
	message := fmt.Sprintf("Marked %d message(s) in %s as read", count, chat)
	if len(skipped) > 0 {
		message += fmt.Sprintf("; skipped %s (sender not recorded)", strings.Join(skipped, ", "))
	}
	return SendMessageResponse{Success: true, Message: message}
}

// parseChatPresence maps the API's typing states to whatsmeow's.
func parseChatPresence(state string) (types.ChatPresence, types.ChatPresenceMedia, error) {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "", "composing", "typing":
		return types.ChatPresenceComposing, types.ChatPresenceMediaText, nil
	case "recording":
		return types.ChatPresenceComposing, types.ChatPresenceMediaAudio, nil
	case "paused", "stop", "stopped":
		return types.ChatPresencePaused, types.ChatPresenceMediaText, nil
	}
	return "", "", fmt.Errorf("unknown typing state %q (want composing, recording or paused)", state)
}

// SendTyping shows or clears the "typing…" indicator in a chat.
func (b *Bridge) SendTyping(ctx context.Context, req TypingRequest) SendMessageResponse {
	presence, media, err := parseChatPresence(req.State)
	if err != nil {
		return failed("%v", err)
	}
	chat, err := jid.ParseRecipient(req.ChatJID)
	if err != nil {
		return failed("Error parsing chat: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	if err := b.client.SendChatPresence(ctx, chat, presence, media); err != nil {
		return failed("Error sending typing state: %v", err)
	}
	state := string(presence)
	if media == types.ChatPresenceMediaAudio {
		state = "recording"
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Typing state %s sent to %s", state, chat)}
}
