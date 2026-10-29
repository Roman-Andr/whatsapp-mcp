package resources

// Resource updates from bridge events, the per-account event pumps, and
// reading the resources through an MCP client.

import (
	"bytes"
	"context"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	acct "github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/account"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/storetest"
)

const (
	defaultHost = "default.invalid"
	workHost    = "work.invalid"
)

func TestEventsMapToUpdatedResources(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, event, account string
		want                 []string
	}{
		{"a message", `{"type": "message", "chat_jid": "` + group + `"}`, "",
			[]string{"whatsapp://chat/" + group, "whatsapp://chats", "whatsapp://chats/unread"}},
		{"a reaction", `{"type": "reaction", "chat_jid": "` + group + `"}`, "", []string{"whatsapp://chat/" + group}},
		{"the session", `{"type": "session", "state": "logged_out"}`, "", []string{"whatsapp://status"}},
		{"presence", `{"type": "presence", "chat_jid": "1@s.whatsapp.net"}`, "", nil},
		{"no chat", `{"type": "message"}`, "", nil},
		{"a named account", `{"type": "call", "chat_jid": "w@g.us"}`, "work",
			[]string{"whatsapp://work/chat/w@g.us", "whatsapp://work/chats", "whatsapp://work/chats/unread"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev, err := pyfmt.DecodeOrdered([]byte(tc.event))
			if err != nil {
				t.Fatal(err)
			}
			if got := updatedURIs(ev.(*pyfmt.OMap), tc.account); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseChatURI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		uri, account, jid string
		ok                bool
	}{
		{"whatsapp://chat/" + group, "", group, true},
		{"whatsapp://chat/a%40b", "", "a@b", true},
		{"whatsapp://chat/%zz", "", "%zz", true},
		{"whatsapp://work/chat/w@g.us", "work", "w@g.us", true},
		{"whatsapp://other/chat/w@g.us", "", "", false},
		{"whatsapp://chats", "", "", false},
		{"https://chat/x", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.uri, func(t *testing.T) {
			t.Parallel()
			account, jid, ok := parseChatURI(tc.uri, []string{"work"})
			if account != tc.account || jid != tc.jid || ok != tc.ok {
				t.Errorf("got %q %q %v", account, jid, ok)
			}
		})
	}
}

func TestReservedAccountNamesGetNoResources(t *testing.T) {
	t.Parallel()
	var logged bytes.Buffer
	accounts := map[string]*acct.Account{"default": {}, "chats": {}, "a/b": {}, "work": {}}
	if got := NamedAccounts(accounts, logging.New(&logged)); !reflect.DeepEqual(got, []string{"default", "work"}) {
		t.Errorf("got %v", got)
	}
	const want = "WARN account gets no resources: its URIs would clash with the default account's account=a/b\n" +
		"WARN account gets no resources: its URIs would clash with the default account's account=chats\n"
	if logged.String() != want {
		t.Errorf("logged:\n%s\nwant:\n%s", logged.String(), want)
	}
	if got := NamedAccounts(map[string]*acct.Account{"default": {}}, logging.Discard()); got != nil {
		t.Errorf("one account: %v", got)
	}
}

func TestDefaultAccountURIs(t *testing.T) {
	t.Parallel()
	if got := urisOf(""); got != (resourceURIs{chatsURI, unreadURI, statusURI, chatURI}) {
		t.Errorf("got %+v", got)
	}
}

// collect receives n URIs, sorted.
func collect(t *testing.T, published <-chan string, n int) []string {
	t.Helper()
	var got []string
	for range n {
		select {
		case uri := <-published:
			got = append(got, uri)
		case <-time.After(time.Minute):
			t.Fatalf("published %v", got)
		}
	}
	slices.Sort(got)
	return got
}

func TestEventPumpReconnectsAfterABridgeRestart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := storetest.New(t, storetest.Options{})
		f.Config.APIBaseURL = "http://" + defaultHost + "/api"
		bridges := newFakeBridges(defaultHost)
		var logged syncBuffer
		ctx, cancel := context.WithCancel(t.Context())
		published := make(chan string, 10)
		p := NewPump(testDeps(f, bridges, &logged), nil, func(uri string) { published <- uri })
		var wg sync.WaitGroup
		wg.Go(func() { p.Run(ctx) })
		synctest.Wait()
		bridges.publish(defaultHost, `{"type": "reaction", "chat_jid": "`+group+`"}`)
		if got := collect(t, published, 1); !reflect.DeepEqual(got, []string{"whatsapp://chat/" + group}) {
			t.Errorf("got %v", got)
		}
		// Down for a minute: many failed attempts, one log line.
		bridges.setDown(defaultHost, true)
		time.Sleep(time.Minute)
		bridges.setDown(defaultHost, false)
		time.Sleep(DefaultRetryInterval)
		synctest.Wait()
		if n := bridges.open(defaultHost); n != 1 {
			t.Fatalf("%d streams open", n)
		}
		bridges.publish(defaultHost, `{"type": "session", "state": "connected"}`)
		if got := collect(t, published, 1); !reflect.DeepEqual(got, []string{"whatsapp://status"}) {
			t.Errorf("got %v", got)
		}
		cancel()
		wg.Wait()
		const want = `WARN event stream down retry_every=10s err="Get \"http://default.invalid/api/events\": bridge down"` + "\n" +
			"event stream back\n"
		if got := logged.String(); got != want {
			t.Errorf("logged:\n%s\nwant:\n%s", got, want)
		}
	})
}

func TestEventPumpReopensItsStream(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := storetest.New(t, storetest.Options{})
		f.Config.APIBaseURL = "http://" + defaultHost + "/api"
		bridges := newFakeBridges(defaultHost)
		ctx, cancel := context.WithCancel(t.Context())
		p := NewPump(testDeps(f, bridges, nil), nil, func(string) {})
		var wg sync.WaitGroup
		wg.Go(func() { p.Run(ctx) })
		// Each connection lasts 25 s; an hour later there is still exactly
		// one, and no busy loop ran in between.
		time.Sleep(time.Hour)
		synctest.Wait()
		if n := bridges.open(defaultHost); n != 1 {
			t.Errorf("%d streams open", n)
		}
		cancel()
		wg.Wait()
	})
}

func TestEventPumpPerAccount(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		f := storetest.New(t, storetest.Options{})
		f.Config.APIBaseURL = "http://" + defaultHost + "/api"
		workAccount(t, f, "http://"+workHost+"/api")
		bridges := newFakeBridges(defaultHost, workHost)
		accounts, err := f.Registry().List()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		published := make(chan string, 20)
		p := NewPump(testDeps(f, bridges, nil), NamedAccounts(accounts, logging.Discard()), func(uri string) { published <- uri })
		var wg sync.WaitGroup
		wg.Go(func() { p.Run(ctx) })
		synctest.Wait()
		// Each pump reads the bridge of its own account.
		if d, w := bridges.open(defaultHost), bridges.open(workHost); d != 1 || w != 1 {
			t.Fatalf("streams open: default %d, work %d", d, w)
		}
		bridges.publish(workHost, `{"type": "message", "chat_jid": "w@g.us"}`)
		bridges.publish(defaultHost, `{"type": "session", "state": "logged_out"}`)
		// The default account's events update its plain and its named URIs.
		want := []string{"whatsapp://default/status", "whatsapp://status", "whatsapp://work/chat/w@g.us", "whatsapp://work/chats", "whatsapp://work/chats/unread"}
		if got := collect(t, published, 5); !reflect.DeepEqual(got, want) {
			t.Errorf("got %v", got)
		}
		cancel()
		wg.Wait()
	})
}

// syncBuffer is a bytes.Buffer safe for the pump's goroutines.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// connect serves the resources of the fixture's settings to an in-memory
// client.
func connect(t *testing.T, f *storetest.Fixture, bridges *fakeBridges) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "whatsapp", Version: "test"}, nil)
	accounts, err := f.Registry().List()
	if err != nil {
		t.Fatal(err)
	}
	Register(srv, testDeps(f, bridges, nil), NamedAccounts(accounts, logging.Discard()))
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func TestReadResources(t *testing.T) {
	t.Parallel()
	f := storetest.New(t, storetest.Options{})
	f.Config.APIBaseURL = "http://" + defaultHost + "/api"
	workAccount(t, f, "http://"+workHost+"/api")
	cs := connect(t, f, newFakeBridges(defaultHost, workHost))
	cases := []struct {
		uri, mime, want string
	}{
		{"whatsapp://status", "application/json", "{\n \"connected\": true\n}"},
		{"whatsapp://work/chats", "application/json", "[\n {\n  \"jid\": \"w@g.us\",\n  \"name\": \"Work crew\",\n" +
			"  \"last_message_time\": \"2026-10-27 10:00:00+03:00\",\n  \"last_message\": \"Shift starts at six\",\n" +
			"  \"last_sender\": \"1\",\n  \"last_is_from_me\": false,\n  \"unread_count\": 0,\n  \"archived\": false,\n" +
			"  \"pinned\": false,\n  \"muted_until\": null,\n  \"labels\": []\n }\n]"},
		{"whatsapp://chats/unread", "application/json", "[]"},
		{"whatsapp://work/chat/secret@g.us", "text/plain", "No messages to display."},
		{"whatsapp://chat/32547396399312%40lid", "text/plain",
			"[2026-10-27 11:00:00 +03:00] Chat: 32547396399312 From: David Bender: [id: D1 | chat: 32547396399312@lid] direct hello\n"},
	}
	for _, tc := range cases {
		t.Run(tc.uri, func(t *testing.T) {
			t.Parallel()
			res, err := cs.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: tc.uri})
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Contents) != 1 {
				t.Fatalf("got %d contents", len(res.Contents))
			}
			c := res.Contents[0]
			if c.URI != tc.uri || c.MIMEType != tc.mime || c.Text != tc.want {
				t.Errorf("got %s %s:\n%s\nwant:\n%s", c.URI, c.MIMEType, c.Text, tc.want)
			}
		})
	}
}
