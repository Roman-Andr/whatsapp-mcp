package bridge

import (
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types/events"
)

func webMessage(id, participant, text string, at time.Time) *waHistorySync.HistorySyncMsg {
	return &waHistorySync.HistorySyncMsg{Message: &waWeb.WebMessageInfo{
		Key:              &waCommon.MessageKey{ID: new(id), Participant: new(participant)},
		Message:          &waE2E.Message{Conversation: new(text)},
		MessageTimestamp: new(uint64(at.Unix())),
	}}
}

func TestHistorySyncStoresConversations(t *testing.T) {
	t.Parallel()
	at := time.Unix(1_750_000_000, 0)
	cases := []struct {
		name    string
		sync    waHistorySync.HistorySync_HistorySyncType
		publish bool
	}{
		{"initial sync", waHistorySync.HistorySync_INITIAL_BOOTSTRAP, false},
		{"on demand", waHistorySync.HistorySync_ON_DEMAND, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			sub := b.events.Subscribe()
			starred := webMessage("H2", "8000@lid", "with extras", at.Add(time.Minute))
			starred.Message.Starred = new(true)
			starred.Message.Reactions = []*waWeb.Reaction{{Key: &waCommon.MessageKey{FromMe: new(true)}, Text: new("👍"), SenderTimestampMS: new(at.UnixMilli())}}
			b.HandleEvent(&events.HistorySync{Data: &waHistorySync.HistorySync{
				SyncType: c.sync.Enum(),
				Conversations: []*waHistorySync.Conversation{
					{
						ID: new(opsGroup), DisplayName: new("Snow ops"), ConversationTimestamp: new(uint64(at.Unix())),
						Messages: []*waHistorySync.HistorySyncMsg{
							webMessage("H1", "8000@lid", "first", at),
							starred,
							{Message: &waWeb.WebMessageInfo{}}, // no time stamp: skipped
							{Message: &waWeb.WebMessageInfo{Key: &waCommon.MessageKey{}, MessageTimestamp: new(uint64(1))}}, // unparsable
						},
					},
					{ID: new("a.b.c@s.whatsapp.net")}, // unparsable JID
				},
			}})
			if r := ref(t, b, "H1", opsGroup); r.Content != "first" || r.SenderJID != "8000@lid" {
				t.Errorf("H1 %+v", r)
			}
			if name := b.store.ChatName(t.Context(), opsGroup); name != "Snow ops" {
				t.Errorf("chat name %q", name)
			}
			if got := queryString(t, b, "SELECT starred || emoji FROM messages JOIN reactions ON reactions.message_id = messages.id WHERE id = 'H2'"); got != "1👍" {
				t.Errorf("star and reaction %q", got)
			}
			if logs := b.logs.String(); !strings.Contains(logs, `msg="history sync complete" messages=2`) {
				t.Errorf("summary not logged:\n%s", logs)
			}
			if c.publish {
				if evt := nextEvent(t, sub); evt.Type != "history" || evt.Content != "2 messages" {
					t.Errorf("event %+v", evt)
				}
			}
			noEvent(t, sub)
		})
	}
}
