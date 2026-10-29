package bridge

import (
	"context"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// countUnread keeps chats.unread_count current for a live message: one more
// for a new message from someone else, none left once we write in the chat
// (from the phone or through the bridge), as WhatsApp itself does.
func (b *Bridge) countUnread(ctx context.Context, chatJID string, fromMe, isNew bool) {
	var err error
	switch {
	case fromMe:
		err = b.store.SetUnread(ctx, chatJID, 0)
	case isNew:
		err = b.store.AddUnread(ctx, chatJID, 1)
	}
	if err != nil {
		b.log.Warn("failed to update unread count", "chat", chatJID, "err", err)
	}
}

// onReceipt clears the unread count when the chat was read on another
// device of this account ("read-self" receipts).
func (b *Bridge) onReceipt(ctx context.Context, evt *events.Receipt) {
	if evt.Type != types.ReceiptTypeReadSelf {
		return
	}
	chatJID := b.storedChat(ctx, evt.Chat).String()
	if err := b.store.SetUnread(ctx, chatJID, 0); err != nil {
		b.log.Warn("failed to clear unread count", "chat", chatJID, "err", err)
	}
}

// onMarkChatAsRead follows "mark as read/unread" from another device.
func (b *Bridge) onMarkChatAsRead(ctx context.Context, evt *events.MarkChatAsRead) {
	chatJID := b.storedChat(ctx, evt.JID).String()
	var err error
	if evt.Action.GetRead() {
		err = b.store.SetUnread(ctx, chatJID, 0)
	} else {
		err = b.store.MarkUnread(ctx, chatJID)
	}
	if err != nil {
		b.log.Warn("failed to update unread state", "chat", chatJID, "err", err)
	}
}
