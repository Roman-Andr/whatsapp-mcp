package bridge

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types/events"
)

type chatRow struct {
	archived, pinned bool
	mutedUntil       int64
	deleted          bool
	unread           int
}

// chatState is the stored state of testGroup.
func chatState(t *testing.T, b *testBridge) chatRow {
	t.Helper()
	var r chatRow
	var deleted any
	err := b.store.DB().QueryRowContext(t.Context(), "SELECT archived, pinned, muted_until, deleted_at, unread_count FROM chats WHERE jid = ?", testGroup).
		Scan(&r.archived, &r.pinned, &r.mutedUntil, &deleted, &r.unread)
	if err != nil {
		t.Fatal(err)
	}
	r.deleted = deleted != nil
	return r
}

func TestChatStateFollowsOtherDevices(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	g := testGroupJID
	steps := []struct {
		name string
		evt  any
		want chatRow
	}{
		{"archived", &events.Archive{JID: g, Action: &waSyncAction.ArchiveChatAction{Archived: new(true)}}, chatRow{archived: true}},
		{"pinned", &events.Pin{JID: g, Action: &waSyncAction.PinAction{Pinned: new(true)}}, chatRow{archived: true, pinned: true}},
		{"muted forever", &events.Mute{JID: g, Action: &waSyncAction.MuteAction{Muted: new(true), MuteEndTimestamp: new(int64(-1))}}, chatRow{archived: true, pinned: true, mutedUntil: -1}},
		{"muted without an end", &events.Mute{JID: g, Action: &waSyncAction.MuteAction{Muted: new(true)}}, chatRow{archived: true, pinned: true, mutedUntil: -1}},
		{"unmuted", &events.Mute{JID: g, Action: &waSyncAction.MuteAction{Muted: new(false)}}, chatRow{archived: true, pinned: true}},
		{"a message", groupMessage("M1", "hello", false), chatRow{archived: true, pinned: true, unread: 1}},
		{"deleted", &events.DeleteChat{JID: g, Timestamp: time.Now()}, chatRow{archived: true, deleted: true}},
		{"a new message brings it back", groupMessage("M2", "back", false), chatRow{archived: true, unread: 1}},
		{"deleted without a time", &events.DeleteChat{JID: g}, chatRow{archived: true, deleted: true}},
	}
	// The steps build on each other, so they run in order.
	for _, s := range steps {
		b.HandleEvent(s.evt)
		if r := chatState(t, b); r != s.want {
			t.Errorf("%s: %+v, want %+v", s.name, r, s.want)
		}
	}
	if !b.store.MessageExists(t.Context(), "M1", testGroup) {
		t.Error("delete dropped the messages")
	}

	b.applyConversationState(t.Context(), testGroup, &waHistorySync.Conversation{Archived: new(false), Pinned: new(uint32(1700000000)), MuteEndTime: new(uint64(1900000000))}, waHistorySync.HistorySync_RECENT)
	if r := chatState(t, b); r.archived || !r.pinned || r.mutedUntil != 1900000000000 {
		t.Errorf("after history sync: %+v", r)
	}
	b.applyConversationState(t.Context(), testGroup, &waHistorySync.Conversation{MuteEndTime: new(^uint64(0))}, waHistorySync.HistorySync_RECENT)
	if r := chatState(t, b); r.mutedUntil != -1 {
		t.Errorf("muted forever in history sync: %+v", r)
	}
}

func TestLabelsFollowOtherDevices(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	st := b.store
	count := func(query string) int {
		t.Helper()
		var n int
		if err := st.DB().QueryRowContext(t.Context(), query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	b.HandleEvent(&events.LabelEdit{LabelID: "3", Action: &waSyncAction.LabelEditAction{Name: new("Paid"), Color: new(int32(2))}})
	b.HandleEvent(&events.LabelAssociationChat{JID: testGroupJID, LabelID: "3", Action: &waSyncAction.LabelAssociationAction{Labeled: new(true)}})
	if id, err := st.FindLabel(t.Context(), "paid"); err != nil || id != "3" {
		t.Errorf("FindLabel by name: %q %v", id, err)
	}
	if n := count("SELECT COUNT(*) FROM chat_labels WHERE chat_jid = 'g@g.us' AND label_id = '3'"); n != 1 {
		t.Errorf("chat label rows: %d", n)
	}
	if next, _ := st.NextLabelID(t.Context()); next != "4" {
		t.Errorf("next id %q", next)
	}
	b.HandleEvent(&events.LabelEdit{LabelID: "3", Action: &waSyncAction.LabelEditAction{Deleted: new(true)}})
	if _, err := st.FindLabel(t.Context(), "Paid"); err == nil {
		t.Error("deleted label still found")
	}
	if n := count("SELECT COUNT(*) FROM chat_labels"); n != 0 {
		t.Errorf("deleted label still on %d chats", n)
	}
}
