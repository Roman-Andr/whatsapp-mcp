package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/storetest"
)

const group = storetest.Group

var (
	assertContains    = storetest.AssertContains
	assertNotContains = storetest.AssertNotContains
)

func newStore(t *testing.T) *storetest.Fixture {
	t.Helper()
	return storetest.New(t, storetest.Options{})
}

// newTestServer builds the server from the fixture's settings as they are
// now: the server reads them once.
func newTestServer(t *testing.T, f *storetest.Fixture) *Server {
	t.Helper()
	s, err := New(f.Config, DefaultDeps(f.Config, logging.Discard()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func connectClient(t *testing.T, f *storetest.Fixture) *mcp.ClientSession {
	t.Helper()
	s := newTestServer(t, f)
	ctx := t.Context()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func listTools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		out[tool.Name] = tool
	}
	return out
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func structured(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	data, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	json.Unmarshal(data, &out)
	return out
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	w, h := 64, 32
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, x%h, color.NRGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// streamBridge is a bridge whose /events streams stay open (like the real
// one between keepalives) and carry whatever publish sends.
type streamBridge struct {
	open    atomic.Int32
	mu      sync.Mutex
	streams map[chan string]bool
	srv     *http.Server
	l       net.Listener
	url     string
}

func newStreamBridge(t *testing.T, addr string) *streamBridge {
	t.Helper()
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	b := &streamBridge{streams: map[chan string]bool{}, l: l, url: "http://" + l.Addr().String() + "/api"}
	b.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/events" {
			w.Write([]byte(`{"success": true, "message": "ok"}`))
			return
		}
		ch := make(chan string, 16)
		b.mu.Lock()
		b.streams[ch] = true
		b.mu.Unlock()
		b.open.Add(1)
		defer func() {
			b.open.Add(-1)
			b.mu.Lock()
			delete(b.streams, ch)
			b.mu.Unlock()
		}()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case line := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", line)
				w.(http.Flusher).Flush()
			}
		}
	})}
	go b.srv.Serve(l)
	t.Cleanup(b.close)
	return b
}

func (b *streamBridge) publish(event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.streams {
		ch <- event
	}
}

func (b *streamBridge) close() { b.srv.Close() }

func waitUntil(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// workAccount adds an account "work" whose bridge publishes a read
// allowlist hiding one of its chats.
func workAccount(t *testing.T, f *storetest.Fixture, apiURL string) string {
	t.Helper()
	work := t.TempDir()
	storetest.Exec(t, filepath.Join(work, "messages.db"), storetest.BridgeSchema+`
		INSERT INTO chats (jid, name, last_message_time) VALUES ('w@g.us', 'Work crew', '2026-10-27 10:00:00+03:00');
		INSERT INTO chats (jid, name, last_message_time) VALUES ('secret@g.us', 'Board', '2026-10-27 09:00:00+03:00');
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
			VALUES ('W1', 'w@g.us', '1', 'Shift starts at six', '2026-10-27 10:00:00+03:00', 0);
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
			VALUES ('S1', 'secret@g.us', '1', 'Quarterly numbers', '2026-10-27 09:00:00+03:00', 0);
		INSERT INTO bridge_meta (key, value) VALUES ('read_allowlist', '["w@g.us"]');`)
	data, _ := json.Marshal(map[string]any{"work": map[string]any{"store_dir": work, "api_url": apiURL}})
	f.Config.Accounts = string(data)
	return work
}
