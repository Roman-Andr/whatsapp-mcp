package content

import (
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

func TestEventsBecomeText(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 1, 18, 0, 0, 0, time.Local)
	event := func(cancelled bool) *waE2E.Message {
		return &waE2E.Message{EventMessage: &waE2E.EventMessage{
			Name: new("Depot meeting"), Description: new("Bring the route sheets"),
			StartTime: new(start.Unix()), EndTime: new(start.Add(2 * time.Hour).Unix()),
			Location: &waE2E.LocationMessage{Name: new("North depot")}, IsCanceled: new(cancelled),
		}}
	}
	t.Run("scheduled", func(t *testing.T) {
		t.Parallel()
		want := "[event] Depot meeting, " + start.Format("2006-01-02 15:04 -07:00") + " – 20:00 at North depot\nBring the route sheets"
		if got := Text(event(false)); got != want {
			t.Errorf("got %q\nwant %q", got, want)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		if got := Text(event(true)); !strings.HasPrefix(got, "[event] (cancelled) Depot meeting") {
			t.Errorf("cancelled: %q", got)
		}
	})
}

func TestEventResponseName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		answer waE2E.EventResponseMessage_EventResponseType
		want   string
		ok     bool
	}{
		{"going", waE2E.EventResponseMessage_GOING, "going", true},
		{"not going", waE2E.EventResponseMessage_NOT_GOING, "not going", true},
		{"maybe", waE2E.EventResponseMessage_MAYBE, "maybe", true},
		{"unknown", waE2E.EventResponseMessage_UNKNOWN, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, ok := EventResponseName(c.answer); got != c.want || ok != c.ok {
				t.Errorf("got %q %v", got, ok)
			}
		})
	}
}
