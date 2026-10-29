package wa

import (
	"testing"

	"go.mau.fi/whatsmeow/types/events"
)

func TestSessionTracker(t *testing.T) {
	t.Parallel()
	tr := NewSessionTracker()
	if got := tr.Get(); got.State != SessionStarting || got.Since.IsZero() {
		t.Errorf("start %+v", got)
	}
	steps := []struct {
		name, state, reason string
		action, changed     bool
	}{
		{"connects", SessionConnected, "", false, true},
		{"same state again", SessionConnected, "", false, false},
		{"logged out", SessionLoggedOut, ReasonLoggedOut, true, true},
		{"other reason", SessionLoggedOut, "other", true, true},
	}
	for _, s := range steps {
		status, changed := tr.Set(s.state, s.reason, s.action)
		if changed != s.changed || status.State != s.state || status.Reason != s.reason || status.NeedsAction != s.action {
			t.Errorf("%s: %+v changed=%v", s.name, status, changed)
		}
	}
	var none *SessionTracker
	if none.Get().State != SessionStarting {
		t.Error("nil tracker is not starting")
	}
}

func TestLoggedOutReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		onConnect bool
		want      string
	}{
		{"while running", false, ReasonLoggedOut},
		{"while connecting", true, ReasonLoggedOut + " (401: logged out from another device)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := LoggedOutReason(events.ConnectFailureLoggedOut, c.onConnect); got != c.want {
				t.Errorf("got %q", got)
			}
		})
	}
}
