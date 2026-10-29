package resources

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/storetest"
)

const group = storetest.Group

// testDeps are the resources' dependencies with the fixture's current
// settings; bridges answers the bridges' requests in memory.
func testDeps(f *storetest.Fixture, bridges *fakeBridges, log io.Writer) *Deps {
	l := logging.Discard()
	if log != nil {
		l = logging.New(log)
	}
	return &Deps{Accounts: f.Registry(), Pool: f.Pool, HTTP: &http.Client{Transport: bridges}, Scope: f.Scope(), Log: l}
}

// fakeBridges are bridges in memory, by host, so the event pumps' timing
// runs on a synctest bubble's clock. Their event streams stay open, like
// the real ones between keep-alives, and carry what publish sends.
type fakeBridges struct {
	mu      sync.Mutex
	bridges map[string]*fakeBridge
}

type fakeBridge struct {
	down    bool
	streams map[chan string]bool
}

func newFakeBridges(hosts ...string) *fakeBridges {
	b := &fakeBridges{bridges: map[string]*fakeBridge{}}
	for _, h := range hosts {
		b.bridges[h] = &fakeBridge{streams: map[chan string]bool{}}
	}
	return b
}

func (b *fakeBridges) RoundTrip(req *http.Request) (*http.Response, error) {
	b.mu.Lock()
	bridge := b.bridges[req.URL.Host]
	if bridge == nil || bridge.down {
		b.mu.Unlock()
		return nil, errors.New("bridge down")
	}
	if req.URL.Path != "/api/events" {
		b.mu.Unlock()
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"connected": true}`)), Request: req}, nil
	}
	ch := make(chan string, 16)
	bridge.streams[ch] = true
	b.mu.Unlock()
	pr, pw := io.Pipe()
	go func() {
		defer func() {
			b.mu.Lock()
			delete(bridge.streams, ch)
			b.mu.Unlock()
		}()
		for {
			select {
			case line, ok := <-ch:
				if !ok {
					pw.CloseWithError(io.ErrUnexpectedEOF)
					return
				}
				if _, err := pw.Write([]byte("data: " + line + "\n\n")); err != nil {
					return
				}
			case <-req.Context().Done():
				pw.CloseWithError(req.Context().Err())
				return
			}
		}
	}()
	return &http.Response{StatusCode: http.StatusOK, Body: pr, Request: req}, nil
}

// publish sends event on every open stream of host's bridge.
func (b *fakeBridges) publish(host, event string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.bridges[host].streams {
		ch <- event
	}
}

// open is the number of open streams of host's bridge.
func (b *fakeBridges) open(host string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.bridges[host].streams)
}

// setDown stops host's bridge, ending its streams, or starts it again.
func (b *fakeBridges) setDown(host string, down bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	bridge := b.bridges[host]
	bridge.down = down
	if down {
		for ch := range bridge.streams {
			close(ch)
		}
		bridge.streams = map[chan string]bool{}
	}
}

// workAccount adds an account "work" whose bridge publishes a read
// allowlist hiding one of its chats.
func workAccount(t *testing.T, f *storetest.Fixture, apiURL string) {
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
	data, err := json.Marshal(map[string]any{"work": map[string]any{"store_dir": work, "api_url": apiURL}})
	if err != nil {
		t.Fatal(err)
	}
	f.Config.Accounts = string(data)
}
