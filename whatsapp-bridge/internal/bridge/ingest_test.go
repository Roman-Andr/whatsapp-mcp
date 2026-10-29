package bridge

import (
	"encoding/json"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

func TestSessionEventsArePublishedOnce(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	sub := b.events.Subscribe()

	b.HandleEvent(&events.Connected{})
	b.HandleEvent(&events.Connected{}) // unchanged: no second event
	b.HandleEvent(&events.LoggedOut{OnConnect: true, Reason: events.ConnectFailureLoggedOut})

	if evt := nextEvent(t, sub); evt.Type != "session" || evt.State != wa.SessionConnected {
		t.Errorf("first event %+v", evt)
	}
	if evt := nextEvent(t, sub); evt.State != wa.SessionLoggedOut || evt.Content == "" {
		t.Errorf("logout event %+v", evt)
	}
	noEvent(t, sub)
	if logs := b.logs.String(); !strings.Contains(logs, `level=ERROR msg="WhatsApp session needs action" state=logged_out`) {
		t.Errorf("logout not logged as an error:\n%s", logs)
	}

	body, err := json.Marshal(b.Status())
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Connected bool          `json:"connected"`
		Session   SessionStatus `json:"session"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if status.Connected || status.Session.State != wa.SessionLoggedOut || !status.Session.NeedsAction {
		t.Errorf("status %s", body)
	}
}

func TestSessionStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		evt    any
		state  string
		action bool
	}{
		{"stream replaced", &events.StreamReplaced{}, wa.SessionStreamReplaced, true},
		{"connection lost", &events.Disconnected{}, wa.SessionDisconnected, false},
		{"QR code shown", &events.QR{}, wa.SessionQRRequired, true},
		{"banned", &events.TemporaryBan{}, wa.SessionTemporaryBan, true},
		{"outdated", &events.ClientOutdated{}, wa.SessionClientOutdated, true},
		{"connect failure", &events.ConnectFailure{Reason: events.ConnectFailureServiceUnavailable, Message: "later"}, wa.SessionConnectFailure, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBridge(t)
			b.HandleEvent(c.evt)
			if s := b.session.Get(); s.State != c.state || s.NeedsAction != c.action {
				t.Errorf("state %+v", s)
			}
		})
	}
}

func TestConnectedRepublishesTheReadAllowlist(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t, allowRead(t, aliceChat))
	b.HandleEvent(&events.Connected{})
	if got := queryString(t, b, "SELECT value FROM bridge_meta WHERE key = 'read_allowlist'"); got != `["2000@s.whatsapp.net","8000@lid"]` {
		t.Errorf("published %q", got)
	}
}

func TestMediaRetryWithoutMediaService(t *testing.T) {
	t.Parallel()
	b := New(Deps{})
	b.HandleEvent(&events.MediaRetry{MessageID: "M1"}) // must not panic
}
