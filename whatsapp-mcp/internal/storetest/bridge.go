package storetest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Post is one request the fake bridge received.
type Post struct {
	Endpoint string
	Payload  map[string]any
	Auth     string
}

// Bridge records requests and answers with Answer (default: success).
type Bridge struct {
	mu    sync.Mutex
	posts []Post
	// Answer writes the answer; nil answers success with message_id NEW.
	// Set it before the requests it answers.
	Answer func(w http.ResponseWriter, r *http.Request, payload map[string]any)
	Srv    *httptest.Server
	// URL is the bridge's API URL (ending in /api).
	URL string
}

// NewBridge starts a fake bridge and, when f is not nil, makes it the API
// of the fixture's default account.
func NewBridge(tb testing.TB, f *Fixture) *Bridge {
	tb.Helper()
	t := tb
	b := &Bridge{}
	b.Srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(body, &payload)
			b.mu.Lock()
			b.posts = append(b.posts, Post{strings.TrimPrefix(r.URL.Path, "/api/"), payload, r.Header.Get("Authorization")})
			b.mu.Unlock()
		}
		b.mu.Lock()
		answer := b.Answer
		b.mu.Unlock()
		if answer != nil {
			answer(w, r, payload)
			return
		}
		w.Write([]byte(`{"success": true, "message": "ok", "message_id": "NEW"}`))
	}))
	t.Cleanup(b.Srv.Close)
	b.URL = b.Srv.URL + "/api"
	if f != nil {
		f.Config.APIBaseURL = b.URL
	}
	return b
}

// SetAnswer replaces the answer while requests may be running.
func (b *Bridge) SetAnswer(answer func(w http.ResponseWriter, r *http.Request, payload map[string]any)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.Answer = answer
}

// Calls are the POST requests received so far.
func (b *Bridge) Calls() []Post {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Post{}, b.posts...)
}

// Summary are the endpoints and payloads of the recorded posts, as JSON.
func (b *Bridge) Summary() string {
	var out []any
	for _, p := range b.Calls() {
		out = append(out, []any{p.Endpoint, p.Payload})
	}
	data, _ := json.Marshal(out)
	return string(data)
}

// SSE answers every request with the given server-sent event lines and
// keeps the stream open until the request ends.
func (b *Bridge) SSE(lines ...string) {
	b.SetAnswer(func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, l := range lines {
			w.Write([]byte(l + "\n\n"))
		}
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		<-r.Context().Done()
	})
}
