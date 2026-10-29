package bridge

import (
	"testing"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

var testGroupJID = types.NewJID("g", types.GroupServer)

// unread is the unread count of testGroup.
func unread(t *testing.T, b *testBridge) int {
	t.Helper()
	var n int
	if err := b.store.DB().QueryRowContext(t.Context(), "SELECT unread_count FROM chats WHERE jid = ?", testGroup).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// groupMessage is a text message in testGroup.
func groupMessage(id, text string, fromMe bool) *events.Message {
	evt := messageEvent(testGroupJID, types.NewJID("5", types.HiddenUserServer), id, &waE2E.Message{Conversation: new(text)})
	evt.Info.IsFromMe = fromMe
	return evt
}

func TestUnreadCountFollowsMessagesAndReads(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	markRead := func(read bool) *events.MarkChatAsRead {
		return &events.MarkChatAsRead{JID: testGroupJID, Action: &waSyncAction.MarkChatAsReadAction{Read: new(read)}}
	}
	steps := []struct {
		name string
		evt  any
		want int
	}{
		{"first message", groupMessage("M1", "one", false), 1},
		{"second message", groupMessage("M2", "two", false), 2},
		{"resend of a stored message", groupMessage("M2", "two", false), 2},
		{"own reply reads the chat", groupMessage("M3", "reply", true), 0},
		{"new message", groupMessage("M4", "again", false), 1},
		{"other receipts", &events.Receipt{MessageSource: types.MessageSource{Chat: testGroupJID}, Type: types.ReceiptTypeRead}, 1},
		{"read on the phone", &events.Receipt{MessageSource: types.MessageSource{Chat: testGroupJID}, Type: types.ReceiptTypeReadSelf}, 0},
		{"marked unread", markRead(false), 1},
		{"marked read", markRead(true), 0},
	}
	// The steps build on each other, so they run in order.
	for _, s := range steps {
		b.HandleEvent(s.evt)
		if n := unread(t, b); n != s.want {
			t.Errorf("%s: unread %d, want %d", s.name, n, s.want)
		}
	}
}

func TestHistorySyncSetsUnreadFromThePhone(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	conv := &waHistorySync.Conversation{ID: new(testGroup), UnreadCount: new(uint32(7))}
	steps := []struct {
		name string
		conv *waHistorySync.Conversation
		sync waHistorySync.HistorySync_HistorySyncType
		want int
	}{
		{"on-demand batches carry no state", conv, waHistorySync.HistorySync_ON_DEMAND, 0},
		{"recent sync", conv, waHistorySync.HistorySync_RECENT, 7},
		{"marked unread", &waHistorySync.Conversation{MarkedAsUnread: new(true)}, waHistorySync.HistorySync_RECENT, 1},
	}
	for _, s := range steps {
		b.applyConversationState(t.Context(), testGroup, s.conv, s.sync)
		if n := unread(t, b); n != s.want {
			t.Errorf("%s: unread %d, want %d", s.name, n, s.want)
		}
	}
}
