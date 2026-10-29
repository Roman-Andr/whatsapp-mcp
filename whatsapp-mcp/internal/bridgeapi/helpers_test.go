package bridgeapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/account"
)

const group = "120363431597928274@g.us"

// post is one request the fake bridge received.
type post struct {
	Endpoint string
	Payload  map[string]any
	Auth     string
}

// fakeBridge records requests and answers with answer (default: success).
type fakeBridge struct {
	mu     sync.Mutex
	posts  []post
	answer func(w http.ResponseWriter, r *http.Request, payload map[string]any)
	srv    *httptest.Server
}

func newBridge(t *testing.T) *fakeBridge {
	t.Helper()
	b := &fakeBridge{}
	b.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if r.Method == http.MethodPost {
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &payload)
			b.mu.Lock()
			b.posts = append(b.posts, post{strings.TrimPrefix(r.URL.Path, "/api/"), payload, r.Header.Get("Authorization")})
			b.mu.Unlock()
		}
		if b.answer != nil {
			b.answer(w, r, payload)
			return
		}
		w.Write([]byte(`{"success": true, "message": "ok", "message_id": "NEW"}`))
	}))
	t.Cleanup(b.srv.Close)
	return b
}

// client is a client of the fake bridge for an account stored in dir
// ("": a temp dir), reading its token as the server does.
func (b *fakeBridge) client(t *testing.T, dir string) *Client {
	t.Helper()
	if dir == "" {
		dir = t.TempDir()
	}
	a := &account.Account{Name: "default", StoreDir: dir, APIURL: b.srv.URL + "/api"}
	return &Client{BaseURL: a.APIURL, Token: a.Token, HTTP: NewHTTPClient()}
}

func (b *fakeBridge) calls() []post {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]post{}, b.posts...)
}

// scriptedLine is a line of an event stream and when, from the start of
// the stream, it is sent.
type scriptedLine struct {
	at   time.Duration
	line string
}

// scripted is lines, all sent at at.
func scripted(at time.Duration, lines ...string) []scriptedLine {
	out := make([]scriptedLine, len(lines))
	for i, l := range lines {
		out[i] = scriptedLine{at, l}
	}
	return out
}

// scriptTransport answers every request with an event stream in memory,
// so the stream's timing runs on a synctest bubble's clock. After the
// script it sends a keep-alive comment every 25 s, as the bridge does
// (keepAlive), or nothing; the stream ends with the request.
type scriptTransport struct {
	lines     []scriptedLine
	keepAlive bool
}

func (s scriptTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	pr, pw := io.Pipe()
	ctx := req.Context()
	go func() {
		start := time.Now()
		wait := func(until time.Time) bool {
			timer := time.NewTimer(time.Until(until))
			defer timer.Stop()
			select {
			case <-timer.C:
				return true
			case <-ctx.Done():
				return false
			}
		}
		send := func(line string) bool {
			_, err := pw.Write([]byte(line + "\n\n"))
			return err == nil
		}
		for _, l := range s.lines {
			if !wait(start.Add(l.at)) || !send(l.line) {
				pw.CloseWithError(ctx.Err())
				return
			}
		}
		for next := time.Now(); s.keepAlive; {
			next = next.Add(25 * time.Second)
			if !wait(next) || !send(": keepalive") {
				break
			}
		}
		<-ctx.Done()
		pw.CloseWithError(ctx.Err())
	}()
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: pr, Request: req}, nil
}

// scriptClient is a client whose bridge streams lines, then keep-alives.
func scriptClient(lines ...scriptedLine) *Client {
	return &Client{BaseURL: "http://bridge.invalid/api", HTTP: &http.Client{Transport: scriptTransport{lines, true}}}
}

// silentClient is a client whose bridge opens the stream and sends nothing.
func silentClient() *Client {
	return &Client{BaseURL: "http://bridge.invalid/api", HTTP: &http.Client{Transport: scriptTransport{}}}
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	var decoded any
	if s, ok := v.(string); ok {
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			t.Fatalf("bad JSON %s: %v", s, err)
		}
	} else {
		data, _ := json.Marshal(v)
		json.Unmarshal(data, &decoded)
	}
	data, _ := json.Marshal(decoded)
	return string(data)
}
