package bridge

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
)

// onHistorySync stores the messages of a history sync batch, including
// the on-demand batches requested through /api/history.
func (b *Bridge) onHistorySync(ctx context.Context, historySync *events.HistorySync) {
	data := historySync.Data
	b.log.Info("history sync received", "type", data.GetSyncType().String(), "conversations", len(data.GetConversations()))

	syncedCount := 0
	for _, conversation := range data.GetConversations() {
		chatJID, err := types.ParseJID(conversation.GetID())
		if err != nil {
			b.log.Warn("failed to parse JID", "jid", conversation.GetID(), "err", err)
			continue
		}
		stored := b.storedChat(ctx, chatJID)
		name := b.chatName(ctx, stored, conversation, "")
		if ts := conversation.GetConversationTimestamp(); ts != 0 {
			if err := b.store.SaveChat(ctx, stored.String(), name, time.Unix(int64(ts), 0)); err != nil { //nolint:gosec // G115: seconds since 1970 fit an int64
				b.log.Warn("failed to store chat", "err", err)
			}
		}

		for _, historyMsg := range conversation.GetMessages() {
			webMsg := historyMsg.GetMessage()
			if webMsg == nil || webMsg.GetMessageTimestamp() == 0 {
				continue
			}
			// ParseWebMessage resolves the sender the way live events do. The
			// hand-rolled version this replaced read only Key.Participant, so
			// group messages whose participant sits on the WebMessageInfo were
			// stored with the group itself as the sender.
			evt, err := b.client.ParseWebMessage(chatJID, webMsg)
			if err != nil {
				b.log.Warn("failed to parse history message", "err", err)
				continue
			}
			b.processMessage(ctx, evt, false)
			if err := b.store.SaveChat(ctx, stored.String(), name, evt.Info.Timestamp); err != nil {
				b.log.Warn("failed to store chat", "err", err)
			}
			syncedCount++
		}
		b.applyConversationState(ctx, stored.String(), conversation, data.GetSyncType())
	}

	b.log.Info("history sync complete", "messages", syncedCount)
	if data.GetSyncType() == waHistorySync.HistorySync_ON_DEMAND {
		b.events.Publish(hub.Event{Type: "history", Content: fmt.Sprintf("%d messages", syncedCount), Timestamp: time.Now()})
	}
}

// applyConversationState takes a chat's unread count from the phone's
// snapshot in a history sync. On-demand batches are old pages of a chat and
// carry no current state.
func (b *Bridge) applyConversationState(ctx context.Context, chatJID string, conv *waHistorySync.Conversation, syncType waHistorySync.HistorySync_HistorySyncType) {
	if syncType == waHistorySync.HistorySync_ON_DEMAND {
		return
	}
	if conv.Archived != nil {
		if err := b.store.SetChatFlag(ctx, chatJID, "archived", conv.GetArchived()); err != nil {
			b.log.Warn("failed to store chat state", "chat", chatJID, "err", err)
		}
	}
	if conv.Pinned != nil {
		if err := b.store.SetChatFlag(ctx, chatJID, "pinned", conv.GetPinned() != 0); err != nil {
			b.log.Warn("failed to store chat state", "chat", chatJID, "err", err)
		}
	}
	if conv.MuteEndTime != nil {
		// History sync gives seconds; -1 (as uint64 max) is forever.
		until := int64(0)
		if end := conv.GetMuteEndTime(); end == ^uint64(0) {
			until = -1
		} else if end > 0 {
			until = int64(end) * 1000 //nolint:gosec // G115: seconds since 1970 (the forever value is handled above)
		}
		if err := b.store.SetMutedUntil(ctx, chatJID, until); err != nil {
			b.log.Warn("failed to store chat state", "chat", chatJID, "err", err)
		}
	}
	unread := int(conv.GetUnreadCount())
	if conv.GetMarkedAsUnread() && unread == 0 {
		unread = 1
	}
	if conv.UnreadCount != nil || conv.MarkedAsUnread != nil {
		if err := b.store.SetUnread(ctx, chatJID, unread); err != nil {
			b.log.Warn("failed to store unread count", "chat", chatJID, "err", err)
		}
	}
}
