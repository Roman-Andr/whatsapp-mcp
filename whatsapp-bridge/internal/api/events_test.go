package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
)

// streamRecorder is a flushing ResponseWriter that the stream goroutine and
// the test may use at the same time.
type streamRecorder struct {
	mu      sync.Mutex
	header  http.Header
	body    strings.Builder
	flushes int
}

func (s *streamRecorder) Header() http.Header { return s.header }
func (s *streamRecorder) WriteHeader(int)     {}

func (s *streamRecorder) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body.Write(p)
}

func (s *streamRecorder) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushes++
}

func (s *streamRecorder) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.body.String()
}

func eventFrame(t *testing.T, evt events.Event) string {
	t.Helper()
	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatal(err)
	}
	return "event: " + evt.Type + "\ndata: " + string(data) + "\n\n"
}

func TestEventStream(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		hub := events.NewHub(nil)
		fake := &fakeService{}
		unreadable("999@g.us")(fake)
		handler := NewHandler(Deps{Bridge: fake, Token: "tok", Events: hub})

		ctx, cancel := context.WithCancel(t.Context())
		req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
		req.Header.Set("X-API-Token", "tok")
		w := &streamRecorder{header: http.Header{}}
		done := make(chan struct{})
		go func() {
			defer close(done)
			handler.ServeHTTP(w, req)
		}()
		synctest.Wait()
		want := ": connected\n\n"
		if got := w.String(); got != want {
			t.Fatalf("start %q", got)
		}
		for k, v := range map[string]string{"Content-Type": "text/event-stream", "Cache-Control": "no-cache", "Connection": "keep-alive"} {
			if got := w.header.Get(k); got != v {
				t.Errorf("header %s = %q, want %q", k, got, v)
			}
		}

		hidden := events.Event{Type: "message", ChatJID: "999@g.us", Content: "secret"}
		session := events.Event{Type: "session", State: "connected"}
		visible := events.Event{Type: "message", ChatJID: "120@g.us", Content: "visible"}
		hub.Publish(hidden)
		hub.Publish(session)
		hub.Publish(visible)
		synctest.Wait()
		want += eventFrame(t, session) + eventFrame(t, visible)
		if got := w.String(); got != want {
			t.Errorf("events\n%q\nwant\n%q", got, want)
		}

		time.Sleep(keepAliveInterval - time.Second)
		synctest.Wait()
		if got := w.String(); got != want {
			t.Errorf("keep-alive too early: %q", got)
		}
		time.Sleep(time.Second)
		synctest.Wait()
		want += ": keepalive\n\n"
		if got := w.String(); got != want {
			t.Errorf("keep-alive %q", got)
		}

		cancel()
		<-done
		hub.Publish(visible) // no subscriber left; must not block
		if got := w.String(); got != want {
			t.Errorf("written after the end: %q", got)
		}
	})
}

func TestEventStreamNeedsFlusher(t *testing.T) {
	t.Parallel()
	handler := NewHandler(Deps{Bridge: &fakeService{}, Token: "tok", Events: events.NewHub(nil)})
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	req.Header.Set("X-API-Token", "tok")
	rec := httptest.NewRecorder()
	// Hide the recorder's Flush behind a type that only has the
	// ResponseWriter methods.
	handler.ServeHTTP(struct{ http.ResponseWriter }{rec}, req)
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "Streaming unsupported\n" {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}
