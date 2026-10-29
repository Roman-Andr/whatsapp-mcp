package bridge

import (
	"strings"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func TestParseChatPresence(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, presence, media string }{
		{"", "composing", ""},
		{"composing", "composing", ""},
		{"Typing", "composing", ""},
		{"recording", "composing", "audio"},
		{"paused", "paused", ""},
		{"stop", "paused", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			p, m, err := parseChatPresence(c.in)
			if err != nil || string(p) != c.presence || string(m) != c.media {
				t.Errorf("%q -> %s %s %v", c.in, p, m, err)
			}
		})
	}
	if _, _, err := parseChatPresence("dancing"); err == nil {
		t.Error("unknown state accepted")
	}
}

func TestMarkRead(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) *testBridge {
		t.Helper()
		b := newOnlineBridge(t)
		for _, m := range []store.Message{
			{ID: "A1", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "one", Timestamp: time.Now()},
			{ID: "A2", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "two", Timestamp: time.Now()},
			{ID: "OLD", ChatJID: opsGroup, Content: "no sender", Timestamp: time.Now()},
			{ID: "MINE", ChatJID: opsGroup, Content: "mine", Timestamp: time.Now(), IsFromMe: true},
		} {
			mustStore(t, b.store, m)
		}
		if err := b.store.AddUnread(t.Context(), opsGroup, 3); err != nil {
			t.Fatal(err)
		}
		return b
	}
	cases := []struct {
		name string
		ids  []string
		want string
	}{
		{"recent messages", nil, "Marked 2 message(s) in 120@g.us as read; skipped OLD (sender not recorded)"},
		{"given ids", []string{"A1"}, "Marked 1 message(s) in 120@g.us as read"},
		{"own messages", []string{"MINE"}, "Nothing to mark as read: the messages are your own"},
		{"senders unknown", []string{"OLD"}, "Cannot mark OLD as read: their senders were not recorded"},
		{"unknown id", []string{"NOPE"}, "Message NOPE not found in chat 120@g.us"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := setup(t)
			resp := b.MarkRead(t.Context(), ReadRequest{ChatJID: opsGroup, MessageIDs: c.ids})
			if resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
			if resp.Success && queryString(t, b, "SELECT unread_count FROM chats WHERE jid = ?", opsGroup) != "0" {
				t.Error("unread count not cleared")
			}
		})
	}
	if resp := newOnlineBridge(t).MarkRead(t.Context(), ReadRequest{ChatJID: testGroup}); resp.Message != "No incoming messages stored for g@g.us" {
		t.Errorf("%+v", resp)
	}
}

func TestSendTyping(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	cases := []struct{ name, state, want string }{
		{"typing", "", "Typing state composing sent to 2000@s.whatsapp.net"},
		{"recording", "recording", "Typing state recording sent to 2000@s.whatsapp.net"},
		{"unknown state", "dancing", `unknown typing state "dancing" (want composing, recording or paused)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if resp := b.SendTyping(t.Context(), TypingRequest{ChatJID: aliceChat, State: c.state}); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
		})
	}
	if resp := newTestBridge(t).SendTyping(t.Context(), TypingRequest{ChatJID: aliceChat}); !strings.Contains(resp.Message, "Not connected") {
		t.Errorf("offline %+v", resp)
	}
}
