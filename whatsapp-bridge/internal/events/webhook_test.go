package events

import (
	"context"
	"crypto/hmac"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWebhookIsSigned(t *testing.T) {
	t.Parallel()
	got := make(chan *http.Request, 1)
	bodies := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- r
		bodies <- string(body)
	}))
	defer server.Close()

	w := NewWebhook(server.URL, "secret", nil)
	defer w.Close(t.Context())
	if err := w.Deliver(t.Context(), Event{Type: "message", MessageID: "X"}); err != nil {
		t.Fatal(err)
	}
	r, body := <-got, <-bodies
	ts := r.Header.Get("X-Bridge-Timestamp")
	want := Sign("secret", ts, []byte(body))
	t.Run("signature over timestamp and body", func(t *testing.T) {
		if ts == "" || !hmac.Equal([]byte(r.Header.Get("X-Bridge-Signature")), []byte(want)) {
			t.Errorf("signature %q for timestamp %q, want %q", r.Header.Get("X-Bridge-Signature"), ts, want)
		}
	})
	t.Run("signature depends on the secret", func(t *testing.T) {
		if !strings.HasPrefix(want, "sha256=") || Sign("other", ts, []byte(body)) == want {
			t.Errorf("signature does not depend on the secret: %s", want)
		}
	})
	t.Run("no API token", func(t *testing.T) {
		if r.Header.Get("X-API-Token") != "" || r.Header.Get("Authorization") != "" {
			t.Error("the API token reached the webhook")
		}
	})
}

func TestWebhookDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()
	var followed atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed.Store(true)
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()

	w := NewWebhook(server.URL, "s", nil)
	defer w.Close(t.Context())
	err := w.Deliver(t.Context(), Event{Type: "message"})
	if err == nil || !strings.Contains(err.Error(), "307") {
		t.Errorf("redirect answered %v", err)
	}
	if followed.Load() {
		t.Error("redirect followed")
	}
}

func TestWebhookTimesOut(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	w := newWebhook(server.URL, "s", 100*time.Millisecond, nil)
	defer w.Close(t.Context())
	start := time.Now()
	if err := w.Deliver(t.Context(), Event{Type: "message"}); err == nil {
		t.Error("hanging webhook did not time out")
	}
	if time.Since(start) > 5*time.Second {
		t.Error("timeout not applied")
	}
}

func TestWebhookCloseDrainsTheQueue(t *testing.T) {
	t.Parallel()
	var delivered atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered.Add(1)
	}))
	defer server.Close()

	w := NewWebhook(server.URL, "s", nil)
	for range 5 {
		w.enqueue(Event{Type: "message"})
	}
	if err := w.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if n := delivered.Load(); n != 5 {
		t.Errorf("%d of 5 queued events delivered", n)
	}
	w.enqueue(Event{Type: "message"}) // dropped, not a panic
	if err := w.Close(t.Context()); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestWebhookCloseGivesUpAtTheDeadline(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	defer close(release)

	w := NewWebhook(server.URL, "s", nil)
	w.enqueue(Event{Type: "message"})
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := w.Close(ctx); err == nil {
		t.Error("Close waited for a hanging receiver")
	}
}
