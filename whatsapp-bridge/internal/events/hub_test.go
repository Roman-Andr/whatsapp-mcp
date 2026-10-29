package events

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHubFansOutWithoutBlocking(t *testing.T) {
	t.Parallel()
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case received <- string(body):
		default:
		}
	}))
	defer server.Close()

	webhook := NewWebhook(server.URL, "s", nil)
	t.Cleanup(func() { webhook.Close(t.Context()) })
	hub := NewHub(webhook)
	sub := hub.Subscribe()
	slow := hub.Subscribe() // never read: must not block Publish
	for range 100 {
		hub.Publish(Event{Type: "message", MessageID: "X"})
	}
	t.Run("subscriber receives", func(t *testing.T) {
		if evt := <-sub; evt.MessageID != "X" {
			t.Errorf("subscriber got %+v", evt)
		}
	})
	hub.Unsubscribe(slow)
	t.Run("webhook receives", func(t *testing.T) {
		select {
		case body := <-received:
			if !strings.Contains(body, `"message_id":"X"`) {
				t.Errorf("webhook body %s", body)
			}
		case <-time.After(5 * time.Second):
			t.Error("webhook not called")
		}
	})
}

func TestHubWithoutWebhook(t *testing.T) {
	t.Parallel()
	hub := NewHub(nil)
	sub := hub.Subscribe()
	hub.Publish(Event{Type: "session", State: "connected"})
	if evt := <-sub; evt.State != "connected" {
		t.Errorf("got %+v", evt)
	}
	hub.Unsubscribe(sub)
	hub.Publish(Event{Type: "session"})
	select {
	case evt := <-sub:
		t.Errorf("unsubscribed channel got %+v", evt)
	default:
	}
}
