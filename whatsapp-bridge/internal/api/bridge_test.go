package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// readAudit reads the audit log of a test bridge.
func readAudit(t *testing.T) []audit.Entry {
	t.Helper()
	f, err := os.Open("store/audit.log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var entries []audit.Entry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var e audit.Entry
		if err := json.Unmarshal(scanner.Bytes(), &e); err != nil {
			t.Fatalf("bad audit line %q: %v", scanner.Text(), err)
		}
		entries = append(entries, e)
	}
	return entries
}

// testBridge is a bridge without a WhatsApp client: enough for everything
// that is decided before anything is sent. It serves the API built from its
// current fields, so a test can change the configuration between calls.
type testBridge struct {
	store           *store.Store
	events          *hub.Hub
	audit           *audit.Log
	allowlist       *policy.Allowlist
	readAllowlist   *policy.Allowlist
	readOnly        bool
	limiter         *policy.RateLimiter
	mediaRoots      *policy.MediaRoots
	reconnector     *wa.Reconnector
	pairing         *wa.Pairing
	presenceEnabled bool
}

// service is the bridge service as configured now.
func (b *testBridge) service() *bridge.Bridge {
	return bridge.New(bridge.Deps{
		Store: b.store, Events: b.events, Audit: b.audit,
		SendAllowlist: b.allowlist, ReadAllowlist: b.readAllowlist, ReadOnly: b.readOnly,
		MediaRoots: b.mediaRoots, Limiter: b.limiter, Reconnector: b.reconnector, Pairing: b.pairing,
		Presence: b.presenceEnabled,
	})
}

func (b *testBridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	NewHandler(Deps{
		Bridge: b.service(), Token: "tok", ReadOnly: b.readOnly, Limiter: b.limiter, Audit: b.audit, Events: b.events,
	}).ServeHTTP(w, r)
}

// newTestBridge is a bridge without a WhatsApp client and its API.
func newTestBridge(t *testing.T, allow ...string) (*testBridge, http.Handler) {
	t.Helper()
	st := openTestStore(t)
	allowlist, err := policy.NewAllowlist(allow)
	if err != nil {
		t.Fatal(err)
	}
	auditLog, err := audit.Open("store/audit.log", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { auditLog.Close() })
	b := &testBridge{store: st, events: hub.NewHub(nil), allowlist: allowlist, audit: auditLog}
	return b, b
}

// openTestStore opens a fresh store/messages.db in a temporary working
// directory, the way the bridge opens it: media and avatar paths are
// relative to the bridge directory.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	t.Chdir(t.TempDir())
	s, err := store.Open(t.Context(), "store")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// messages.chat_jid references chats; the bridge always stores the chat first.
	for _, jid := range []string{"g@g.us", "120@g.us", "c@s.whatsapp.net"} {
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

func call(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("X-API-Token", "tok")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestAPIRequiresToken(t *testing.T) {
	handler := NewHandler(Deps{Bridge: bridge.New(bridge.Deps{Events: hub.NewHub(nil)}), Token: "secret", Events: hub.NewHub(nil)})
	for _, h := range []map[string]string{
		{},
		{"Authorization": "Bearer wrong"},
		{"X-API-Token": "secre"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(`{}`))
		for k, v := range h {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("headers %v: status %d, want 401", h, rec.Code)
		}
	}
	// With a valid token the request reaches the handler (and fails validation).
	for _, h := range []map[string]string{{"Authorization": "Bearer secret"}, {"X-API-Token": "secret"}} {
		req := httptest.NewRequest(http.MethodPost, "/api/send", strings.NewReader(`{}`))
		for k, v := range h {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("headers %v: status %d, want 400 from validation", h, rec.Code)
		}
	}
}

func TestAllowlistBlocksStateChangesAndIsAudited(t *testing.T) {
	b, handler := newTestBridge(t, "79966764464")
	mustStore(t, b.store, store.Message{ID: "G1", ChatJID: "120@g.us", Sender: "1", SenderJID: "1@lid", Content: "hi", Timestamp: time.Now()})

	rec := call(handler, "/api/send", `{"recipient":"120@g.us","message":"leak"}`)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "send allowlist") {
		t.Errorf("send: %d %s", rec.Code, rec.Body)
	}
	rec = call(handler, "/api/react", `{"chat_jid":"120@g.us","message_id":"G1","emoji":"👍"}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("react: %d %s", rec.Code, rec.Body)
	}
	// An allowed chat passes the allowlist and fails later (no connection).
	rec = call(handler, "/api/send", `{"recipient":"79966764464","message":"note to self"}`)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "Not connected") {
		t.Errorf("allowed send: %d %s", rec.Code, rec.Body)
	}
	call(handler, "/api/send", `{"recipient":"","message":"x"}`)

	entries := readAudit(t)
	if len(entries) != 4 {
		t.Fatalf("%d audit entries: %+v", len(entries), entries)
	}
	if e := entries[0]; e.Endpoint != "send" || e.Chat != "120@g.us" || e.Text != "leak" || e.Success || !strings.Contains(e.Result, "allowlist") {
		t.Errorf("blocked send entry %+v", e)
	}
	if e := entries[1]; e.Endpoint != "react" || e.TargetID != "G1" || e.Text != "👍" {
		t.Errorf("react entry %+v", e)
	}
	if e := entries[3]; e.Result != "Recipient is required" {
		t.Errorf("rejected entry %+v", e)
	}
	if st, _ := os.Stat("store/audit.log"); st.Mode().Perm() != 0600 {
		t.Errorf("audit log mode %v", st.Mode().Perm())
	}
}

func TestReadAllowlistHidesChatsFromBridgeRoutes(t *testing.T) {
	b, handler := newTestBridge(t)
	b.readAllowlist, _ = policy.NewAllowlist([]string{"79966764464"})
	for path, body := range map[string]string{
		"/api/download": `{"message_id":"G1","chat_jid":"120@g.us"}`,
		"/api/group":    `{"chat_jid":"120@g.us"}`,
		"/api/history":  `{"chat_jid":"120@g.us"}`,
	} {
		rec := call(handler, path, body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "read allowlist") {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	if err := b.service().CheckReadable(context.Background(), "79966764464@s.whatsapp.net"); err != nil {
		t.Errorf("allowed chat refused: %v", err)
	}

	// The MCP server reads the list from messages.db.
	if err := b.service().PublishReadAllowlist(context.Background()); err != nil {
		t.Fatal(err)
	}
	var published string
	b.store.DB().QueryRow("SELECT value FROM bridge_meta WHERE key = 'read_allowlist'").Scan(&published)
	if published != `["79966764464@s.whatsapp.net"]` {
		t.Errorf("published %q", published)
	}
	b.readAllowlist, _ = policy.NewAllowlist(nil)
	b.service().PublishReadAllowlist(context.Background())
	if err := b.store.DB().QueryRow("SELECT value FROM bridge_meta WHERE key = 'read_allowlist'").Scan(&published); err == nil {
		t.Errorf("unrestricted list left %q behind", published)
	}
}

func TestEventStreamSkipsUnreadableChats(t *testing.T) {
	b, handler := newTestBridge(t)
	b.readAllowlist, _ = policy.NewAllowlist([]string{"120@g.us"})
	server := httptest.NewServer(handler)
	defer server.Close()
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/events", nil)
	req.Header.Set("X-API-Token", "tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	reader.ReadString('\n') // ": connected"
	reader.ReadString('\n')
	b.events.Publish(hub.Event{Type: "message", ChatJID: "999@g.us", Content: "secret"})
	b.events.Publish(hub.Event{Type: "session", State: "connected"})
	b.events.Publish(hub.Event{Type: "message", ChatJID: "120@g.us", Content: "visible"})
	var got []string
	for len(got) < 2 {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "data: ") {
			got = append(got, line)
		}
	}
	if strings.Contains(got[0], "secret") || !strings.Contains(got[0], "session") || !strings.Contains(got[1], "visible") {
		t.Errorf("events %q", got)
	}
}

func TestSendOutsideMediaRootsIsForbidden(t *testing.T) {
	b, handler := newTestBridge(t)
	root := t.TempDir()
	var err error
	if b.mediaRoots, err = policy.NewMediaRoots([]string{root}, ""); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret.pdf")
	os.WriteFile(outside, []byte("x"), 0600)
	inside := filepath.Join(root, "report.pdf")
	os.WriteFile(inside, []byte("x"), 0600)

	for _, route := range []string{"/api/send", "/api/sticker"} {
		rec := call(handler, route, `{"recipient":"79966764464","media_path":"`+outside+`"}`)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "outside the media roots") {
			t.Errorf("%s outside: %d %s", route, rec.Code, rec.Body)
		}
	}
	// Inside the root the file passes the check and fails later (no connection).
	rec := call(handler, "/api/send", `{"recipient":"79966764464","media_path":"`+inside+`"}`)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "Not connected") {
		t.Errorf("inside: %d %s", rec.Code, rec.Body)
	}
	rec = call(handler, "/api/send", `{"recipient":"79966764464","media_path":"`+root+`/missing.pdf"}`)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "not found") {
		t.Errorf("missing: %d %s", rec.Code, rec.Body)
	}
}

func TestStructuredMessagesCannotBeEdited(t *testing.T) {
	b, handler := newTestBridge(t)
	mustStore(t, b.store, store.Message{ID: "POLL", ChatJID: "c@s.whatsapp.net", Content: "[poll] Depot?: A / B", Timestamp: time.Now(), IsFromMe: true})
	rec := call(handler, "/api/edit", `{"chat_jid":"c@s.whatsapp.net","message_id":"POLL","message":"x"}`)
	if !strings.Contains(rec.Body.String(), "is a poll") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestEditAndDeleteRefuseWhatCannotBeChanged(t *testing.T) {
	b, handler := newTestBridge(t)
	now := time.Now()
	mustStore(t, b.store, store.Message{ID: "THEIRS", ChatJID: "c@s.whatsapp.net", Sender: "c", Content: "hi", Timestamp: now})
	mustStore(t, b.store, store.Message{ID: "OLD", ChatJID: "c@s.whatsapp.net", Content: "typo", Timestamp: now.Add(-21 * time.Minute), IsFromMe: true})
	mustStore(t, b.store, store.Message{ID: "PIC", ChatJID: "c@s.whatsapp.net", Timestamp: now, IsFromMe: true, Media: content.MediaInfo{Type: "image"}})
	mustStore(t, b.store, store.Message{ID: "GONE", ChatJID: "c@s.whatsapp.net", Content: "x", Timestamp: now, IsFromMe: true})
	mustStore(t, b.store, store.Message{ID: "MINE", ChatJID: "c@s.whatsapp.net", Content: "fresh", Timestamp: now, IsFromMe: true})
	b.store.MarkDeleted(context.Background(), "GONE", "c@s.whatsapp.net", now, nil)

	cases := []struct {
		path, body, want string
	}{
		{"/api/edit", `{"chat_jid":"c@s.whatsapp.net","message_id":"THEIRS","message":"x"}`, "only your own messages"},
		{"/api/delete", `{"chat_jid":"c@s.whatsapp.net","message_id":"THEIRS"}`, "only your own messages"},
		{"/api/edit", `{"chat_jid":"c@s.whatsapp.net","message_id":"OLD","message":"fixed"}`, "within 20m"},
		{"/api/edit", `{"chat_jid":"c@s.whatsapp.net","message_id":"PIC","message":"caption"}`, "only text messages"},
		{"/api/delete", `{"chat_jid":"c@s.whatsapp.net","message_id":"GONE"}`, "already deleted"},
		{"/api/delete", `{"chat_jid":"c@s.whatsapp.net","message_id":"NOPE"}`, "not found"},
		// Everything checked; only the missing connection stops it.
		{"/api/edit", `{"chat_jid":"c@s.whatsapp.net","message_id":"MINE","message":"better"}`, "Not connected"},
	}
	for _, c := range cases {
		rec := call(handler, c.path, c.body)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s, want %q", c.path, c.body, rec.Code, rec.Body, c.want)
		}
	}
	if rec := call(handler, "/api/edit", `{"chat_jid":"c@s.whatsapp.net","message_id":"MINE"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("edit without text: %d", rec.Code)
	}

	b.allowlist, _ = policy.NewAllowlist([]string{"79966764464"})
	if rec := call(handler, "/api/delete", `{"chat_jid":"c@s.whatsapp.net","message_id":"MINE"}`); rec.Code != http.StatusForbidden {
		t.Errorf("delete outside allowlist: %d %s", rec.Code, rec.Body)
	}
	entries := readAudit(t)
	if last := entries[len(entries)-1]; last.Endpoint != "delete" || last.TargetID != "MINE" || last.Success {
		t.Errorf("audit %+v", last)
	}
}

func TestMarkReadAndTypingChecks(t *testing.T) {
	b, handler := newTestBridge(t)
	now := time.Now()
	mustStore(t, b.store, store.Message{ID: "R1", ChatJID: "120@g.us", Sender: "1", SenderJID: "1@lid", Content: "a", Timestamp: now})
	mustStore(t, b.store, store.Message{ID: "R2", ChatJID: "120@g.us", Sender: "2", SenderJID: "2@lid", Content: "b", Timestamp: now.Add(time.Second)})
	mustStore(t, b.store, store.Message{ID: "R3", ChatJID: "120@g.us", Content: "mine", Timestamp: now.Add(2 * time.Second), IsFromMe: true})

	refs, err := b.store.RecentIncoming(context.Background(), "120@g.us", 20)
	if err != nil || len(refs) != 2 || refs[0].ID != "R2" || refs[0].SenderJID != "2@lid" {
		t.Fatalf("recent incoming %+v %v", refs, err)
	}

	cases := []struct{ path, body, want string }{
		{"/api/read", `{"chat_jid":"120@g.us","message_ids":["R3"]}`, "your own"},
		{"/api/read", `{"chat_jid":"120@g.us","message_ids":["R9"]}`, "not found"},
		{"/api/read", `{"chat_jid":"c@s.whatsapp.net"}`, "No incoming messages"},
		{"/api/read", `{"chat_jid":"120@g.us"}`, "Not connected"},
		{"/api/typing", `{"chat_jid":"120@g.us","state":"dancing"}`, "unknown typing state"},
		{"/api/typing", `{"chat_jid":"120@g.us","state":"composing"}`, "Not connected"},
	}
	for _, c := range cases {
		rec := call(handler, c.path, c.body)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s, want %q", c.path, c.body, rec.Code, rec.Body, c.want)
		}
	}

	b.allowlist, _ = policy.NewAllowlist([]string{"79966764464"})
	for _, path := range []string{"/api/read", "/api/typing"} {
		if rec := call(handler, path, `{"chat_jid":"120@g.us"}`); rec.Code != http.StatusForbidden {
			t.Errorf("%s outside allowlist: %d %s", path, rec.Code, rec.Body)
		}
	}
}

func TestPollAndLocationRoutesCheckBeforeSending(t *testing.T) {
	_, handler := newTestBridge(t, "79966764464")
	cases := []struct {
		path, body string
		code       int
		want       string
	}{
		{"/api/poll", `{"recipient":"79966764464","question":"Q","options":["A"]}`, 500, "2 to 12 options"},
		{"/api/poll", `{"recipient":"120@g.us","question":"Q","options":["A","B"]}`, 403, "allowlist"},
		{"/api/poll", `{"recipient":"79966764464","question":"Q","options":["A","B"]}`, 500, "Not connected"},
		{"/api/location", `{"recipient":"79966764464","latitude":100,"longitude":0}`, 500, "latitude"},
		{"/api/location", `{"recipient":"120@g.us","latitude":55.7,"longitude":37.6}`, 403, "allowlist"},
		{"/api/location", `{"latitude":55.7,"longitude":37.6}`, 400, "recipient"},
	}
	for _, c := range cases {
		rec := call(handler, c.path, c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
	entries := readAudit(t)
	if e := entries[1]; e.Endpoint != "poll" || e.Text != "Q [A / B]" {
		t.Errorf("poll audit %+v", e)
	}
	if e := entries[4]; e.Endpoint != "location" || !strings.HasPrefix(e.Text, "[location] 55.700000,37.600000") {
		t.Errorf("location audit %+v", e)
	}

}

func TestGroupInfoValidatesJID(t *testing.T) {
	_, handler := newTestBridge(t)
	rec := call(handler, "/api/group", `{"chat_jid":"79966764464@s.whatsapp.net"}`)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "not a group") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if entries := readAudit(t); len(entries) != 0 {
		t.Errorf("read-only call audited: %+v", entries)
	}
}
