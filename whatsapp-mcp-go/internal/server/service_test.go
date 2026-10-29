package server

// The server as a long-running service: event streams released when calls
// end, quick exits and per-account resources.

import (
	"context"
	"io"
	"net"
	"net/http"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
)

func TestStdioServerReturnsAtOnceWhenInputCloses(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	b := newStreamBridge(t, "")
	f.Config.APIBaseURL = b.url
	s := newTestServer(t, f)
	in, inW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- s.ServeStdio(t.Context(), &mcp.IOTransport{Reader: in, Writer: nopWriteCloser{io.Discard}})
	}()
	waitUntil(t, 5*time.Second, "the event pump", func() bool { return b.open.Load() == 1 })
	started := time.Now()
	inW.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("still serving after stdin closed")
	}
	if d := time.Since(started); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
	waitUntil(t, 2*time.Second, "the stream to close", func() bool { return b.open.Load() == 0 })
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestWaitForEventsLetsGoOfTheBridgeWhenItReturns(t *testing.T) {
	t.Parallel()
	newStore(t)
	b := newStreamBridge(t, "")
	c := &bridgeapi.Client{BaseURL: b.url, HTTP: bridgeapi.NewHTTPClient()}
	r := c.WaitForEvents(t.Context(), 1, "", 20, nil)
	if !pyfmt.Truthy(r.Get("success")) || len(r.Get("events").([]any)) != 0 {
		t.Fatalf("got %s", pyfmt.Indent(r, ""))
	}
	waitUntil(t, 600*time.Millisecond, "the stream to close", func() bool { return b.open.Load() == 0 })
}

// httpService runs ServeHTTP against a stream bridge.
type httpService struct {
	url    string
	stop   context.CancelFunc
	done   chan error
	bridge *streamBridge
}

func startService(t *testing.T) *httpService {
	t.Helper()
	f := newStore(t)
	b := newStreamBridge(t, "")
	f.Config.APIBaseURL = b.url
	f.Config.Token = "test-token-0123456789abcdef"
	s := newTestServer(t, f)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	svc := &httpService{url: "http://" + l.Addr().String() + "/mcp", stop: cancel, done: make(chan error, 1), bridge: b}
	go func() { svc.done <- s.ServeHTTP(ctx, l) }()
	t.Cleanup(func() { cancel(); <-svc.done })
	// The event pump holds one stream for the whole life of the server.
	waitUntil(t, 5*time.Second, "the event pump", func() bool { return b.open.Load() == 1 })
	return svc
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
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	return cs
}

func TestConcurrentSessionsAreNotBlockedByAWaitingOne(t *testing.T) {
	t.Parallel()
	svc := startService(t)
	waiter := svc.session(t)
	defer waiter.Close()
	result := make(chan *mcp.CallToolResult, 1)
	go func() {
		res, _ := waiter.CallTool(t.Context(), &mcp.CallToolParams{Name: "wait_for_events", Arguments: map[string]any{"timeout_seconds": 30}})
		result <- res
	}()
	waitUntil(t, 5*time.Second, "the waiting call's stream", func() bool { return svc.bridge.open.Load() == 2 })
	started := time.Now()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			cs := svc.session(t)
			defer cs.Close()
			res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_chats", Arguments: map[string]any{}})
			if err != nil || res.IsError || len(structured(t, res)["result"].([]any)) != 2 {
				t.Errorf("list_chats: %v %+v", err, res)
			}
		})
	}
	wg.Wait()
	if d := time.Since(started); d > 5*time.Second {
		t.Errorf("blocked for %v", d)
	}
	svc.bridge.publish(`{"type": "message", "chat_jid": "` + group + `", "content": "hi"}`)
	res := <-result
	events := structured(t, res)["result"].(map[string]any)["events"].([]any)
	if len(events) != 1 || events[0].(map[string]any)["content"] != "hi" {
		t.Errorf("events %v", events)
	}
	// Its bridge stream is closed again; the pump's stays.
	waitUntil(t, 5*time.Second, "the waiting call's stream to close", func() bool { return svc.bridge.open.Load() == 1 })
}

func TestCancelledWaitAndClosedSessionReleaseTheBridge(t *testing.T) {
	t.Parallel()
	svc := startService(t)
	cs := svc.session(t)
	ctx, cancel := context.WithCancel(t.Context())
	go cs.CallTool(ctx, &mcp.CallToolParams{Name: "wait_for_events", Arguments: map[string]any{"timeout_seconds": 600}})
	waitUntil(t, 5*time.Second, "the waiting call's stream", func() bool { return svc.bridge.open.Load() == 2 })
	// The client sends notifications/cancelled.
	cancel()
	waitUntil(t, 3*time.Second, "the cancelled call's stream to close", func() bool { return svc.bridge.open.Load() == 1 })

	other := svc.session(t)
	go other.CallTool(t.Context(), &mcp.CallToolParams{Name: "wait_for_events", Arguments: map[string]any{"timeout_seconds": 600}})
	waitUntil(t, 5*time.Second, "the second call's stream", func() bool { return svc.bridge.open.Load() == 2 })
	// A client that ends its session (Claude Code exiting) cancels its calls.
	req, _ := http.NewRequest(http.MethodDelete, svc.url, nil)
	req.Header.Set("Mcp-Session-Id", other.ID())
	if resp, err := (&http.Client{Transport: bearer{http.DefaultTransport}}).Do(req); err != nil || resp.StatusCode >= 300 {
		t.Fatalf("DELETE: %v %v", err, resp)
	}
	waitUntil(t, 3*time.Second, "the closed session's stream to close", func() bool { return svc.bridge.open.Load() == 1 })
	if res := callTool(t, cs, "list_chats", nil); res.IsError {
		t.Errorf("server unusable: %+v", res)
	}
	cs.Close()
}

func TestShutdownIsQuickWithOpenCalls(t *testing.T) {
	t.Parallel()
	svc := startService(t)
	cs := svc.session(t)
	go cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "wait_for_events", Arguments: map[string]any{"timeout_seconds": 600}})
	waitUntil(t, 5*time.Second, "the waiting call's stream", func() bool { return svc.bridge.open.Load() == 2 })
	started := time.Now()
	svc.stop()
	select {
	case err := <-svc.done:
		svc.done <- err
		if err != nil {
			t.Errorf("serveHTTP: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server still running")
	}
	if d := time.Since(started); d > 3*time.Second {
		t.Errorf("shutdown took %v", d)
	}
	waitUntil(t, 3*time.Second, "every bridge stream to close", func() bool { return svc.bridge.open.Load() == 0 })
}

func TestResourcesOfEveryAccount(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	workAccount(t, f, "http://127.0.0.1:1/api")
	cs := connectClient(t, f)
	ctx := t.Context()
	listed, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	uris := map[string]bool{}
	for _, r := range listed.Resources {
		uris[r.URI] = true
	}
	for _, u := range []string{"whatsapp://chats", "whatsapp://work/chats", "whatsapp://work/chats/unread",
		"whatsapp://work/status", "whatsapp://default/chats"} {
		if !uris[u] {
			t.Errorf("%s not listed", u)
		}
	}
	templates, _ := cs.ListResourceTemplates(ctx, nil)
	var tmpl []string
	for _, rt := range templates.ResourceTemplates {
		tmpl = append(tmpl, rt.URITemplate)
	}
	sort.Strings(tmpl)
	if want := []string{"whatsapp://chat/{chat_jid}", "whatsapp://default/chat/{chat_jid}", "whatsapp://work/chat/{chat_jid}"}; !reflect.DeepEqual(tmpl, want) {
		t.Errorf("templates %v", tmpl)
	}
	read := func(uri string) string {
		res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatalf("%s: %v", uri, err)
		}
		return res.Contents[0].Text
	}
	// The plain URIs stay the default account's.
	plain := read("whatsapp://chats")
	assertContains(t, plain, group)
	assertNotContains(t, plain, "w@g.us")
	assertContains(t, read("whatsapp://default/chats"), group)
	// The work account's bridge publishes its read allowlist, which hides the board.
	work := read("whatsapp://work/chats")
	assertContains(t, work, "w@g.us")
	assertNotContains(t, work, "secret@g.us", group)
	assertContains(t, read("whatsapp://work/chat/w@g.us"), "Shift starts at six")
	assertNotContains(t, read("whatsapp://work/chat/secret@g.us"), "Quarterly numbers")
	assertContains(t, read("whatsapp://chat/"+group), "Main street")
	assertNotContains(t, read("whatsapp://work/chat/"+group), "Main street")
	if _, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "whatsapp://home/chats"}); err == nil {
		t.Error("an unknown account's resource was read")
	}
}
