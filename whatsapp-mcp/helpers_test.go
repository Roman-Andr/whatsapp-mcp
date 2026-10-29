package main

// Helpers of the black-box tests: the store fixture as the binary's
// environment, bridges that stream events, and MCP clients.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/server" //nolint:depguard // the black-box tests wire the server up as main.go does
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/storetest"
)

const group = storetest.Group

var assertContains = storetest.AssertContains

// newStore builds the fixture store. The built binary reads the
// environment, so the settings a developer may have exported are cleared
// for the children the tests start.
func newStore(t *testing.T) *storetest.Fixture {
	t.Helper()
	for _, name := range []string{"WHATSAPP_ACCOUNTS", "WHATSAPP_ACCOUNTS_FILE", "WHATSAPP_DEFAULT_ACCOUNT",
		"WHATSAPP_READ_ALLOWLIST", "WHATSAPP_READ_ALLOWLIST_FILE", "WHATSAPP_BRIDGE_TOKEN", "WHATSAPP_READ_ONLY"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	f := storetest.New(t, storetest.Options{})
	t.Setenv("WHATSAPP_STORE_DIR", f.Dir)
	t.Setenv("WHATSAPP_API_BASE_URL", storetest.Unreachable)
	return f
}

// connectClient serves the fixture's settings in process, with the real
// media conversions.
func connectClient(t *testing.T, f *storetest.Fixture) *mcp.ClientSession {
	t.Helper()
	s, err := server.New(f.Config, server.DefaultDeps(f.Config, logging.Discard()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	ctx := context.Background()
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

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
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

// streamBridge is a bridge whose /events streams stay open (like the real
// one between keepalives).
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

// httpService is a server listening on url.
type httpService struct {
	url string
}

type bearer struct{ base http.RoundTripper }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer test-token-0123456789abcdef")
	return b.base.RoundTrip(r)
}

func (svc *httpService) session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{Endpoint: svc.url,
		HTTPClient: &http.Client{Transport: bearer{http.DefaultTransport}}, MaxRetries: -1}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(context.Background(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}
