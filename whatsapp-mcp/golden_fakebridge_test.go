package main

// The fake bridge of the golden scenarios: parity/fakebridge.py (removed in
// 57465cd) ported to Go. POST answers echo the endpoint, payload and
// Authorization header; GETs return fixed bodies; /events streams three
// events and then stays quiet for three seconds. The bodies are the bytes
// Python's json.dumps(..., ensure_ascii=False) wrote, ", " and ": " included.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// bridgeCall is one request the fake bridge received.
type bridgeCall struct {
	Method string          `json:"method"`
	Path   string          `json:"path"`
	Auth   string          `json:"auth"`
	Body   json.RawMessage `json:"body"`
}

type goldenBridge struct {
	srv   *httptest.Server
	image string
	// events are the SSE data lines of /api/events; release, when set,
	// holds them back until it is closed, and hold keeps the stream open
	// afterwards until the client goes (instead of three seconds).
	events  []string
	release chan struct{}
	hold    bool
	quit    chan struct{}

	mu    sync.Mutex
	calls []bridgeCall
}

var parityEvents = []string{
	`{"type": "message", "chat_jid": "` + goldenGroup + `", "id": "X1", "content": "hi"}`,
	`{"type": "session", "state": "logged_out"}`,
	`{"type": "message", "chat_jid": "other@g.us", "id": "X2"}`,
}

func newGoldenBridge(t *testing.T, image string, configure func(b *goldenBridge)) *goldenBridge {
	t.Helper()
	b := &goldenBridge{image: image, events: parityEvents, quit: make(chan struct{})}
	if configure != nil {
		configure(b)
	}
	b.srv = httptest.NewServer(http.HandlerFunc(b.serve))
	t.Cleanup(func() {
		close(b.quit)
		b.srv.Close()
	})
	return b
}

// take returns the requests received since the last call.
func (b *goldenBridge) take() []bridgeCall {
	b.mu.Lock()
	defer b.mu.Unlock()
	calls := b.calls
	b.calls = nil
	if calls == nil {
		calls = []bridgeCall{}
	}
	return calls
}

// rawBody is the body as JSON when it is compact JSON, so it is embedded
// byte for byte, else as a JSON string.
func rawBody(body []byte) json.RawMessage {
	var compact bytes.Buffer
	if len(body) > 0 && json.Compact(&compact, body) == nil && bytes.Equal(compact.Bytes(), body) {
		return body
	}
	s, _ := marshalGolden(string(body))
	return s
}

func (b *goldenBridge) answer(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func (b *goldenBridge) serve(w http.ResponseWriter, r *http.Request) {
	// fakebridge.py: self.path.split("/api/", 1)[-1], query string included.
	path := r.RequestURI
	if _, rest, ok := strings.Cut(path, "/api/"); ok {
		path = rest
	}
	body, _ := io.ReadAll(r.Body)
	// The event pump of every account reopens /events whenever a stream
	// ends, at times that have nothing to do with the request in flight,
	// and it cannot be told apart from wait_for_events: event streams are
	// left out of the record.
	if r.Method != http.MethodGet || path != "events" {
		b.mu.Lock()
		b.calls = append(b.calls, bridgeCall{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: rawBody(body)})
		b.mu.Unlock()
	}
	if r.Method == http.MethodGet {
		b.serveGET(w, r, path)
		return
	}
	b.servePOST(w, r, path, body)
}

func (b *goldenBridge) serveGET(w http.ResponseWriter, r *http.Request, path string) {
	switch path {
	case "status":
		b.answer(w, http.StatusOK, `{"connected": false, "jid": "79966764464@s.whatsapp.net", "session": {"state": "logged_out", "reason": "scan the QR code", "needs_action": true}, "send_allowlist": [], "read_allowlist": []}`)
	case "blocklist":
		b.answer(w, http.StatusOK, `{"success": true, "blocked": [{"jid": "`+goldenGroup+`"}, {"jid": "1@s.whatsapp.net", "name": "<b>"}]}`)
	case "channels":
		b.answer(w, http.StatusOK, `{"success": true, "channels": [{"jid": "1@newsletter", "name": "News", "followers": 12}]}`)
	case "events":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		if flusher != nil {
			flusher.Flush()
		}
		if b.release != nil {
			select {
			case <-b.release:
			case <-r.Context().Done():
				return
			case <-b.quit:
				return
			}
		}
		for _, ev := range b.events {
			io.WriteString(w, "data: "+ev+"\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
		var quiet <-chan time.Time
		if !b.hold {
			quiet = time.After(3 * time.Second)
		}
		select {
		case <-quiet:
		case <-r.Context().Done():
		case <-b.quit:
		}
	default:
		b.answer(w, http.StatusNotFound, `{"success": false, "message": "no such endpoint"}`)
	}
}

func (b *goldenBridge) servePOST(w http.ResponseWriter, r *http.Request, path string, body []byte) {
	// payload = json.loads(body or b"{}")
	if len(body) == 0 {
		body = []byte("{}")
	}
	var payload map[string]json.RawMessage
	json.Unmarshal(body, &payload)
	image, _ := marshalGolden(b.image)
	switch path {
	case "download":
		if string(payload["message_id"]) == `"F1"` {
			b.answer(w, http.StatusInternalServerError, `{"success": false, "message": "media expired"}`)
			return
		}
		b.answer(w, http.StatusOK, `{"success": true, "message": "ok", "path": `+string(image)+`, "media_type": "image", "mimetype": "image/png"}`)
	case "contact/info":
		picture := "null"
		if p, ok := payload["picture"]; ok && pyTruthy(p) {
			picture = string(image)
		}
		b.answer(w, http.StatusOK, `{"success": true, "jid": `+pySpaced(payload["jid"])+`, "name": "David", "picture_path": `+picture+`}`)
	case "edit":
		b.answer(w, http.StatusBadRequest, `{"message": "too late to edit"}`)
	case "star":
		// No Content-Type, as BaseHTTPRequestHandler sent it.
		w.Header()["Content-Type"] = nil
		w.Header().Set("Content-Length", "11")
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "Bad gateway")
	default:
		auth := "null"
		if v := r.Header.Values("Authorization"); len(v) > 0 {
			a, _ := marshalGolden(v[0])
			auth = string(a)
		}
		endpoint, _ := marshalGolden(path)
		b.answer(w, http.StatusOK, `{"success": true, "message": "ok", "message_id": "NEW", "endpoint": `+string(endpoint)+
			`, "payload": `+pySpaced(body)+`, "auth": `+auth+`}`)
	}
}

// pyTruthy is Python's bool() of a decoded JSON value.
func pyTruthy(raw json.RawMessage) bool {
	switch s := strings.TrimSpace(string(raw)); s {
	case "", "null", "false", "0", "0.0", `""`, "[]", "{}":
		return false
	}
	return true
}

// pySpaced re-renders compact JSON the way json.dumps writes it: ", " and
// ": " between items. Strings stay as they are.
func pySpaced(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "null"
	}
	var out strings.Builder
	inString, escaped := false, false
	for _, c := range string(raw) {
		switch {
		case inString && escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case !inString && (c == ' ' || c == '\n' || c == '\t' || c == '\r'):
			continue
		}
		out.WriteRune(c)
		if !inString && (c == ',' || c == ':') {
			out.WriteByte(' ')
		}
	}
	return out.String()
}

// marshalGolden is json.Marshal without HTML escaping.
func marshalGolden(v any) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
