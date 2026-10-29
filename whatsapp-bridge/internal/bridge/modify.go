package bridge

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// ownMessage looks up a message this account sent, the only kind that can be
// edited or deleted for everyone here.
func (b *Bridge) ownMessage(ctx context.Context, chatJID, messageID, action string) (*store.MessageRef, types.JID, SendMessageResponse) {
	ref, chat, refused := b.targetMessage(ctx, chatJID, messageID)
	if ref == nil {
		return nil, chat, refused
	}
	if !ref.IsFromMe {
		return nil, chat, failed("Message %s was sent by someone else; only your own messages can be %s", ref.ID, action)
	}
	if ref.Deleted {
		return nil, chat, failed("Message %s was already deleted", ref.ID)
	}
	return ref, chat, SendMessageResponse{}
}

// Edit replaces the text of one of our messages. WhatsApp accepts
// edits for 20 minutes after sending (whatsmeow.EditWindow).
func (b *Bridge) Edit(ctx context.Context, req EditRequest) SendMessageResponse {
	ref, chat, refused := b.ownMessage(ctx, req.ChatJID, req.MessageID, "edited")
	if ref == nil {
		return refused
	}
	if ref.MediaType != "" {
		return failed("Message %s is a %s; only text messages can be edited", ref.ID, ref.MediaType)
	}
	// Polls, locations and contacts are stored as text like "[poll] ...";
	// WhatsApp would not apply a text edit to them.
	for _, prefix := range []string{"[poll] ", "[location] ", "[contact] ", "[contacts] "} {
		if strings.HasPrefix(ref.Content, prefix) {
			return failed("Message %s is a %s; only text messages can be edited", ref.ID, strings.Trim(prefix, "[] "))
		}
	}
	if age := time.Since(ref.Timestamp); age > whatsmeow.EditWindow {
		return failed("Message %s was sent %s ago; WhatsApp only allows edits within %s",
			ref.ID, age.Round(time.Minute), whatsmeow.EditWindow)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	edit := b.client.BuildEdit(chat, ref.ID, &waE2E.Message{Conversation: new(req.Message)})
	resp, err := b.client.SendMessage(ctx, chat, edit)
	if err != nil {
		return failed("Error sending edit: %v", err)
	}
	b.applyEdit(ctx, ref.ChatJID, ref.ID, req.Message, resp.Timestamp, &store.Author{FromMe: true}, true)
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Edited %s", ref.ID), MessageID: resp.ID, Timestamp: resp.Timestamp}
}

// Delete revokes a message for everyone: one of ours, or, in a group
// where we are an admin, anyone's. The local copy is marked deleted and keeps
// its text, like messages others delete.
func (b *Bridge) Delete(ctx context.Context, req DeleteRequest) SendMessageResponse {
	ref, chat, refused := b.targetMessage(ctx, req.ChatJID, req.MessageID)
	if ref == nil {
		return refused
	}
	if ref.Deleted {
		return failed("Message %s was already deleted", ref.ID)
	}
	if !ref.IsFromMe && chat.Server != types.GroupServer {
		return failed("Message %s was sent by someone else; in a direct chat only your own messages can be deleted", ref.ID)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	sender := types.EmptyJID
	if !ref.IsFromMe {
		if !b.amGroupAdmin(ctx, chat) {
			return failed("Message %s was sent by someone else; only group admins can delete it", ref.ID)
		}
		var err error
		if sender, err = b.authorJID(ctx, ref); err != nil {
			return failed("Cannot determine the author of %s: %v", ref.ID, err)
		}
	}
	resp, err := b.client.SendMessage(ctx, chat, b.client.BuildRevoke(chat, sender, ref.ID))
	if err != nil {
		return failed("Error deleting message: %v", err)
	}
	if ref.IsFromMe {
		b.applyRevoke(ctx, chat, ref.ChatJID, ref.ID, resp.Timestamp, types.MessageInfo{MessageSource: types.MessageSource{IsFromMe: true}}, true)
	} else {
		// An admin deletion: no author check applies.
		if _, err := b.store.MarkDeleted(ctx, ref.ID, ref.ChatJID, resp.Timestamp, nil); err != nil {
			b.log.Warn("failed to store deletion", "message_id", ref.ID, "err", err)
		}
		b.events.Publish(hub.Event{Type: "delete", ChatJID: ref.ChatJID, MessageID: ref.ID, IsFromMe: true, Timestamp: resp.Timestamp})
		return SendMessageResponse{Success: true, Message: fmt.Sprintf("Deleted %s for everyone (as group admin)", ref.ID), MessageID: resp.ID, Timestamp: resp.Timestamp}
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Deleted %s for everyone", ref.ID), MessageID: resp.ID, Timestamp: resp.Timestamp}
}

// amGroupAdmin reports whether this account is an admin of group, by phone
// number or LID.
func (b *Bridge) amGroupAdmin(ctx context.Context, group types.JID) bool {
	if id := b.client.OwnID(); id != nil && b.isGroupAdmin(ctx, group, id.ToNonAD()) {
		return true
	}
	lid := b.client.OwnLID()
	return !lid.IsEmpty() && b.isGroupAdmin(ctx, group, lid.ToNonAD())
}
