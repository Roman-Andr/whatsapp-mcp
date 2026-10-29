package bridge

import (
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func TestEdit(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	now := time.Now()
	for _, m := range []store.Message{
		{ID: "MINE", ChatJID: opsGroup, Content: "route A", Timestamp: now, IsFromMe: true},
		{ID: "THEIRS", ChatJID: opsGroup, Sender: "8000", Content: "route A", Timestamp: now},
		{ID: "OLD", ChatJID: opsGroup, Content: "route A", Timestamp: now.Add(-time.Hour), IsFromMe: true},
		{ID: "POLL", ChatJID: opsGroup, Content: "[poll] Depot?: A / B", Timestamp: now, IsFromMe: true},
	} {
		mustStore(t, b.store, m)
	}
	cases := []struct{ name, id, want string }{
		{"own message", "MINE", "Edited MINE"},
		{"someone else's", "THEIRS", "Message THEIRS was sent by someone else; only your own messages can be edited"},
		{"too old", "OLD", "Message OLD was sent 1h0m0s ago; WhatsApp only allows edits within 20m0s"},
		{"a poll", "POLL", "Message POLL is a poll; only text messages can be edited"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if resp := b.Edit(t.Context(), EditRequest{ChatJID: opsGroup, MessageID: c.id, Message: "route B"}); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
		})
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T, admin bool) *testBridge {
		t.Helper()
		b := newOnlineBridge(t)
		b.fake.Groups[groupJID] = &types.GroupInfo{JID: groupJID, Participants: []types.GroupParticipant{{JID: ownLID, IsAdmin: admin}}}
		for _, m := range []store.Message{
			{ID: "MINE", ChatJID: opsGroup, Content: "oops", Timestamp: time.Now(), IsFromMe: true},
			{ID: "THEIRS", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "spam", Timestamp: time.Now()},
			{ID: "DM", ChatJID: aliceChat, Sender: "2000", Content: "hi", Timestamp: time.Now()},
		} {
			mustStore(t, b.store, m)
		}
		return b
	}
	cases := []struct {
		name    string
		admin   bool
		offline bool
		chat    string
		id      string
		want    string
		deleted bool
	}{
		{"own message", false, false, opsGroup, "MINE", "Deleted MINE for everyone", true},
		{"as group admin", true, false, opsGroup, "THEIRS", "Deleted THEIRS for everyone (as group admin)", true},
		{"not an admin", false, false, opsGroup, "THEIRS", "Message THEIRS was sent by someone else; only group admins can delete it", false},
		{"someone else's in a direct chat", false, false, aliceChat, "DM", "Message DM was sent by someone else; in a direct chat only your own messages can be deleted", false},
		{"offline", true, true, opsGroup, "THEIRS", "Not connected to WhatsApp", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := setup(t, c.admin)
			b.fake.Connected = !c.offline
			sub := b.events.Subscribe()
			if resp := b.Delete(t.Context(), DeleteRequest{ChatJID: c.chat, MessageID: c.id}); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
			if got := ref(t, b, c.id, c.chat).Deleted; got != c.deleted {
				t.Errorf("deleted %v", got)
			}
			if c.deleted {
				if evt := nextEvent(t, sub); evt.Type != "delete" || evt.MessageID != c.id {
					t.Errorf("event %+v", evt)
				}
				if again := b.Delete(t.Context(), DeleteRequest{ChatJID: c.chat, MessageID: c.id}); !strings.HasSuffix(again.Message, "was already deleted") {
					t.Errorf("second delete %+v", again)
				}
			}
		})
	}
}
