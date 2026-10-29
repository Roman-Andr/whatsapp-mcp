package bridge

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/media"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa/watest"
)

// The accounts the tests use.
var (
	ownPN    = types.NewJID("1000", types.DefaultUserServer)
	ownLID   = types.NewJID("9000", types.HiddenUserServer)
	alicePN  = types.NewJID("2000", types.DefaultUserServer)
	aliceLID = types.NewJID("8000", types.HiddenUserServer)
	bobPN    = types.NewJID("3000", types.DefaultUserServer)
	groupJID = types.NewJID("120", types.GroupServer)
)

// Chats every test store has, since messages.chat_jid references chats.
const (
	testGroup = "g@g.us"
	opsGroup  = "120@g.us"
	carlChat  = "c@s.whatsapp.net"
	aliceChat = "2000@s.whatsapp.net"
)

// openTestStore opens a fresh messages.db in a temporary directory.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	return openStoreIn(t, t.TempDir())
}

func openStoreIn(t *testing.T, dir string) *store.Store {
	t.Helper()
	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, jid := range []string{testGroup, opsGroup, carlChat, aliceChat} {
		if err := s.SaveChat(t.Context(), jid, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func mustStore(t *testing.T, s *store.Store, m store.Message) {
	t.Helper()
	if err := s.SaveMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

// logBuffer collects log lines; tests read them with String.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// testBridge is a bridge on a fresh store in its own directory.
type testBridge struct {
	*Bridge
	dir  string
	fake *watest.Client // nil for a bridge without a client
	logs *logBuffer
}

// newTestBridge is a bridge without a WhatsApp client. opts adjust the
// dependencies before New.
func newTestBridge(t *testing.T, opts ...func(*Deps)) *testBridge {
	t.Helper()
	return buildBridge(t, nil, opts)
}

// newOnlineBridge is a bridge with a connected fake client linked as ownPN
// (LID ownLID). Alice's phone number and LID are mapped and she is a saved
// contact.
func newOnlineBridge(t *testing.T, opts ...func(*Deps)) *testBridge {
	t.Helper()
	fake := watest.New(ownPN, ownLID)
	fake.LIDMap.Put(aliceLID, alicePN)
	fake.LIDMap.Put(ownLID, ownPN)
	fake.Book.Put(alicePN, types.ContactInfo{FullName: "Alice Full", PushName: "Alice Push"})
	return buildBridge(t, fake, opts)
}

func buildBridge(t *testing.T, fake *watest.Client, opts []func(*Deps)) *testBridge {
	t.Helper()
	dir := t.TempDir()
	st := openStoreIn(t, dir)
	auditLog, err := audit.Open(filepath.Join(dir, "audit.log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { auditLog.Close() })
	roots, err := policy.NewMediaRoots([]string{dir}, "")
	if err != nil {
		t.Fatal(err)
	}
	logs := &logBuffer{}
	var client wa.Client
	if fake != nil {
		client = fake
	}
	d := Deps{
		Client:     client,
		Store:      st,
		Media:      &media.Service{Client: client, Store: st, Root: dir},
		Events:     hub.NewHub(nil),
		Audit:      auditLog,
		MediaRoots: roots,
		Session:    wa.NewSessionTracker(),
		Pairing:    wa.NewPairing(filepath.Join(dir, "qr.png")),
		Log:        slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		// The base context ends with the test, as the application's does
		// at shutdown.
		BaseContext: t.Context(),
		StoreDir:    dir,
		Restart:     make(chan struct{}, 1),
	}
	for _, opt := range opts {
		opt(&d)
	}
	return &testBridge{Bridge: New(d), dir: dir, fake: fake, logs: logs}
}

// allowSend restricts sending to entries.
func allowSend(t *testing.T, entries ...string) func(*Deps) {
	t.Helper()
	list, err := policy.NewAllowlist(entries)
	if err != nil {
		t.Fatal(err)
	}
	return func(d *Deps) { d.SendAllowlist = list }
}

// allowRead restricts reading to entries.
func allowRead(t *testing.T, entries ...string) func(*Deps) {
	t.Helper()
	list, err := policy.NewAllowlist(entries)
	if err != nil {
		t.Fatal(err)
	}
	return func(d *Deps) { d.ReadAllowlist = list }
}

// nextEvent waits briefly for the next published event.
func nextEvent(t *testing.T, sub chan hub.Event) hub.Event {
	t.Helper()
	select {
	case evt := <-sub:
		return evt
	case <-time.After(time.Second):
		t.Fatal("no event published")
		return hub.Event{}
	}
}

// noEvent checks that nothing more was published.
func noEvent(t *testing.T, sub chan hub.Event) {
	t.Helper()
	select {
	case evt := <-sub:
		t.Errorf("unexpected event %+v", evt)
	default:
	}
}

func ref(t *testing.T, b *testBridge, id, chat string) *store.MessageRef {
	t.Helper()
	r, err := b.store.Ref(t.Context(), id, chat)
	if err != nil {
		t.Fatalf("message %s in %s: %v", id, chat, err)
	}
	return r
}

func mustParse(t *testing.T, s string) types.JID {
	t.Helper()
	j, err := types.ParseJID(s)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// queryString runs a query that answers one text value ("" for no row).
func queryString(t *testing.T, b *testBridge, query string, args ...any) string {
	t.Helper()
	var v sql.NullString
	if err := b.store.DB().QueryRowContext(t.Context(), query, args...).Scan(&v); err != nil && !errors.Is(err, sql.ErrNoRows) {
		t.Fatal(err)
	}
	return v.String
}

// auditLines is the audit log written so far.
func auditLines(t *testing.T, b *testBridge) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(b.dir, "audit.log"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

func TestNewFillsDefaults(t *testing.T) {
	t.Parallel()
	st := openTestStore(t)
	svc := &media.Service{Store: st}
	b := New(Deps{Store: st, Media: svc})
	if b.log == nil || b.base == nil || b.storeDir != media.DefaultRoot {
		t.Errorf("log %v, base %v, store dir %q", b.log, b.base, b.storeDir)
	}
	if svc.Author == nil {
		t.Error("the media service cannot name a message's author")
	}
	if b.calls == nil || b.presence == nil {
		t.Error("call and presence logs missing")
	}
}

func TestOnline(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                string
		client              bool
		connected, loggedIn bool
		want                bool
	}{
		{"no client", false, false, false, false},
		{"connected and linked", true, true, true, true},
		{"waiting for a QR scan", true, true, false, false},
		{"disconnected", true, false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var client wa.Client
			if c.client {
				fake := watest.New(ownPN, ownLID)
				fake.Connected, fake.LoggedIn = c.connected, c.loggedIn
				client = fake
			}
			if got := New(Deps{Client: client}).Online(); got != c.want {
				t.Errorf("Online() = %v", got)
			}
		})
	}
}

func TestHandleEventUsesTheBaseContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	b := newTestBridge(t, func(d *Deps) { d.BaseContext = ctx })
	cancel()
	// The store refuses work on a cancelled context: the message is lost
	// and the failure logged, as at shutdown.
	b.HandleEvent(groupMessage("LATE", "too late", false))
	if b.store.MessageExists(t.Context(), "LATE", testGroup) {
		t.Error("stored with a cancelled base context")
	}
}
