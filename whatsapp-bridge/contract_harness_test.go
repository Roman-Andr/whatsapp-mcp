package main

// The contract harness: everything the contract tests (contract_*_test.go)
// need to build a bridge, drive it and compare what it does with the goldens
// under testdata/contract/. It is the only contract file that knows how the
// bridge is put together, so code moves rewrite this file and nothing else;
// the goldens never change.
//
// The bridge is built as run() builds it, with a real whatsmeow client on a
// temporary device store, minus the network: the client is never connected,
// its HTTP clients and http.DefaultClient fail every request, and the
// reconnector is a stub.

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAdv"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/api"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/config"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/media"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

var update = flag.Bool("update", false, "rewrite the contract goldens under testdata/contract")

// contractToken is the API token of every contract bridge.
const contractToken = "contract-token-0123456789abcdef"

// Identities of the linked contract device and its contacts.
var (
	ownPN    = types.JID{User: "79990000001", Device: 1, Server: types.DefaultUserServer}
	ownLID   = types.JID{User: "99990000001", Device: 1, Server: types.HiddenUserServer}
	alicePN  = types.NewJID("79990000002", types.DefaultUserServer)
	aliceLID = types.NewJID("99990000002", types.HiddenUserServer)
	bobPN    = types.NewJID("79990000003", types.DefaultUserServer)
	groupJID = types.NewJID("120363000000000001", types.GroupServer)
)

// contractStart is when the test binary started; timestamps near it are
// produced by time.Now() and normalised to <NOW>.
var contractStart = time.Now()

// errNetworkDisabled is what every HTTP request of a contract bridge gets.
var errNetworkDisabled = errors.New("network disabled in contract tests")

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errNetworkDisabled
}

// fakeClock drives the rate limiter and the reconnector.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type contractOpts struct {
	linked      bool // device has an ID (own JID 79990000001@s.whatsapp.net, LID 99990000001@lid)
	readOnly    bool
	sendAllow   []string // WHATSAPP_SEND_ALLOWLIST entries
	readAllow   []string
	rate, burst int      // 0,0 = unlimited
	mediaRoots  []string // relative to dir; default "store"
	presence    bool
	welcomes    map[string]string // group JID -> WHATSAPP_WELCOME_FILE text
}

type contractEnv struct {
	t       *testing.T
	dir     string // temp bridge dir, the working directory
	bridge  *bridge.Bridge
	client  *whatsmeow.Client
	store   *store.Store
	events  *hub.Hub
	pairing *wa.Pairing
	handler http.Handler
	token   string
	clock   *fakeClock
}

// newContractEnv builds a bridge in a new temporary directory.
func newContractEnv(t *testing.T, o contractOpts) *contractEnv {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	t.Chdir(dir)
	disableNetwork(t)
	fixLocalZone(t)

	ctx := context.Background()
	messages, err := store.Open(ctx, "store")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { messages.Close() })
	auditLog, err := audit.Open("store/audit.log", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { auditLog.Close() })

	container, err := sqlstore.New(ctx, "sqlite3", "file:store/whatsapp.db?_foreign_keys=on", waLog.Noop)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { container.Close() })
	device := container.NewDevice()
	if o.linked {
		id, lid := ownPN, ownLID
		device.ID, device.LID, device.PushName = &id, lid, "Me"
		device.Account = &waAdv.ADVSignedDeviceIdentity{Details: []byte{}, AccountSignature: make([]byte, 64), AccountSignatureKey: make([]byte, 32), DeviceSignature: make([]byte, 64)}
		if err := device.Save(ctx); err != nil {
			t.Fatal(err)
		}
		for _, m := range [][2]types.JID{{ownLID.ToNonAD(), ownPN.ToNonAD()}, {aliceLID, alicePN}} {
			if err := device.LIDs.PutLIDMapping(ctx, m[0], m[1]); err != nil {
				t.Fatal(err)
			}
		}
		if _, _, err := device.Contacts.PutPushName(ctx, alicePN, "Alice Push"); err != nil {
			t.Fatal(err)
		}
		if err := device.Contacts.PutContactName(ctx, alicePN, "Alice", "Alice Full"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := device.Contacts.PutPushName(ctx, bobPN, "Bob"); err != nil {
			t.Fatal(err)
		}
	}

	client := whatsmeow.NewClient(device, waLog.Noop)
	offline := &http.Client{Transport: failingTransport{}}
	client.SetWebsocketHTTPClient(offline)
	client.SetMediaHTTPClient(offline)
	client.SetPreLoginHTTPClient(offline)
	t.Cleanup(func() {
		if client.IsConnected() {
			t.Error("the contract client connected to WhatsApp")
		}
	})

	sendAllow, err := policy.NewAllowlist(o.sendAllow)
	if err != nil {
		t.Fatal(err)
	}
	readAllow, err := policy.NewAllowlist(o.readAllow)
	if err != nil {
		t.Fatal(err)
	}
	clock := &fakeClock{t: time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)}
	limiter := policy.NewRateLimiter(o.rate, o.burst)
	if limiter != nil {
		limiter.Now = clock.now
	}
	rootDirs := o.mediaRoots
	if rootDirs == nil {
		rootDirs = []string{"store"}
	}
	roots, err := policy.NewMediaRoots(rootDirs, "")
	if err != nil {
		t.Fatal(err)
	}

	waClient := wa.New(client)
	hubs := hub.NewHub(nil)
	pairing := wa.NewPairing("store/qr.png")
	reconnector := wa.NewReconnector(
		func() bool { return client.Store.ID != nil },
		func() error { return errors.New("reconnect disabled in contract tests") },
	)
	reconnector.Now = clock.now
	b := bridge.New(bridge.Deps{
		Client: waClient,
		Store:  messages,
		// A host nothing listens on: the bridge's own media downloads fail
		// without leaving the machine.
		Media:         &media.Service{Client: waClient, Store: messages, Host: "http://127.0.0.1:1"},
		Events:        hubs,
		Audit:         auditLog,
		SendAllowlist: sendAllow,
		ReadAllowlist: readAllow,
		ReadOnly:      o.readOnly,
		MediaRoots:    roots,
		Limiter:       limiter,
		Session:       wa.NewSessionTracker(),
		Pairing:       pairing,
		Reconnector:   reconnector,
		Welcomes:      o.welcomes,
		Presence:      o.presence,
		Restart:       make(chan struct{}, 1),
	})
	b.MergeLIDChats(ctx)
	if err := b.PublishReadAllowlist(ctx); err != nil {
		t.Fatal(err)
	}
	if err := roots.Publish(ctx, messages); err != nil {
		t.Fatal(err)
	}

	handler := api.NewHandler(api.Deps{
		Bridge: b, Token: contractToken, ReadOnly: o.readOnly, Limiter: limiter, Audit: auditLog, Events: hubs,
	})
	return &contractEnv{
		t: t, dir: dir, bridge: b, client: client, store: messages, events: hubs, pairing: pairing,
		handler: handler, token: contractToken, clock: clock,
	}
}

// openContractStore opens the bridge's message store in dir (dir/store/
// messages.db), creating or migrating it as the bridge does at start, and
// returns its database handle. The store is closed when the test ends;
// closeStore closes it earlier.
func openContractStore(t *testing.T, dir string) (db *sql.DB, closeStore func()) {
	t.Helper()
	t.Chdir(dir)
	messages, err := store.Open(t.Context(), "store")
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	closeStore = func() { once.Do(func() { messages.Close() }) }
	t.Cleanup(closeStore)
	return messages.DB(), closeStore
}

// fixLocalZone runs the test in UTC: history sync times, event texts and
// stored timestamps are rendered in the local zone.
func fixLocalZone(t *testing.T) {
	t.Helper()
	old := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = old })
}

// disableNetwork makes the bridge's own downloads (avatars, and media through
// http.DefaultClient) fail without leaving the machine, for the duration of
// the test. The media host is pointed away in newContractEnv.
func disableNetwork(t *testing.T) {
	t.Helper()
	oldTransport := http.DefaultClient.Transport
	http.DefaultClient.Transport = failingTransport{}
	t.Cleanup(func() { http.DefaultClient.Transport = oldTransport })
}

// The names the contract tests use for the bridge's parts, mapped to where
// the code lives now.
type (
	SendMessageResponse   = bridge.SendMessageResponse
	DownloadMediaResponse = bridge.DownloadMediaResponse
	SessionStatus         = wa.SessionStatus
	PairingInfo           = wa.PairingInfo
)

const (
	SessionConnected  = wa.SessionConnected
	SessionQRRequired = wa.SessionQRRequired
	SessionLoggedOut  = wa.SessionLoggedOut
	reasonQRRequired  = wa.ReasonQRRequired
	reasonLoggedOut   = wa.ReasonLoggedOut
)

var (
	mediaDir      = media.Dir
	mediaFileName = media.FileName
)

// newAutoDownloadConfig is the auto-download configuration main builds.
func newAutoDownloadConfig(c config.AutoDownload) media.AutoDownloadConfig {
	return media.NewAutoDownloadConfig(c.Enabled, c.MaxVideoBytes, c.MaxFileBytes)
}

// callRejecter is what the config contract reports of the call rejection
// main passes to the bridge: nil unless calls are rejected.
type callRejecter struct{ message string }

func newCallRejecter(c config.RejectCalls) *callRejecter {
	if !c.Enabled {
		return nil
	}
	return &callRejecter{message: c.Message}
}

// configureHistorySync and configureDeviceName apply one setting each, as
// wa.ApplyDeviceProps does both at start.
func configureHistorySync(days int) error { return wa.ApplyDeviceProps(days, "") }

func configureDeviceName(name string) { _ = wa.ApplyDeviceProps(0, name) }

// recorded is one HTTP exchange.
type recorded struct {
	method, path, auth, reqBody string
	status                      int
	header                      http.Header
	body                        string
}

// recordedHeaders are the response headers that are part of the contract.
var recordedHeaders = []string{"Content-Type", "Retry-After", "Allow", "WWW-Authenticate", "Cache-Control", "X-Content-Type-Options"}

func (r recorded) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "### %s %s  (auth: %s)\n", r.method, r.path, r.auth)
	if r.reqBody != "" {
		if len(r.reqBody) > 4096 {
			fmt.Fprintf(&sb, "> <%d bytes>\n", len(r.reqBody))
		} else {
			fmt.Fprintf(&sb, "> %s\n", r.reqBody)
		}
	}
	fmt.Fprintf(&sb, "< %d\n", r.status)
	for _, h := range recordedHeaders {
		for _, v := range r.header.Values(h) {
			fmt.Fprintf(&sb, "< %s: %s\n", h, v)
		}
	}
	body := r.body
	// Images are pinned by size and signature, not bytes.
	if strings.HasPrefix(r.header.Get("Content-Type"), "image/") {
		body = fmt.Sprintf("<%d bytes, starting %x>\n", len(body), body[:min(8, len(body))])
	}
	sb.WriteString(body)
	if body != "" && !strings.HasSuffix(body, "\n") {
		sb.WriteString("\n\\ no newline at end of body\n")
	}
	sb.WriteString("\n")
	return sb.String()
}

// bearer is the default authentication of do: nil headers send it.
var bearer = map[string]string{"Authorization": "Bearer " + contractToken}

// do sends one request through the API handler. nil headers authenticate
// with "Authorization: Bearer <token>"; an empty map sends no credentials.
func (e *contractEnv) do(method, path string, headers map[string]string, body string) recorded {
	e.t.Helper()
	if headers == nil {
		headers = bearer
	}
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	keys := make([]string, 0, len(headers))
	for k, v := range headers {
		req.Header.Set(k, v)
		keys = append(keys, k)
	}
	sort.Strings(keys)
	auth := "none"
	if len(keys) > 0 {
		parts := make([]string, len(keys))
		for i, k := range keys {
			v := headers[k]
			switch {
			case k == "Authorization" && v == "Bearer "+e.token:
				parts[i] = "bearer"
			case k == "X-API-Token" && v == e.token:
				parts[i] = "x-api-token"
			default:
				parts[i] = k + ": " + strings.ReplaceAll(v, e.token, "<token>")
			}
		}
		auth = strings.Join(parts, ", ")
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return recorded{
		method: method, path: path, auth: auth, reqBody: body,
		status: rec.Code, header: rec.Result().Header, body: rec.Body.String(),
	}
}

// post is do with POST and the default credentials.
func (e *contractEnv) post(path, body string) recorded {
	e.t.Helper()
	return e.do(http.MethodPost, path, nil, body)
}

// exec runs SQL against messages.db, for fixtures. Statements name their
// columns, so they work on any column order.
func (e *contractEnv) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.store.DB().Exec(query, args...); err != nil {
		e.t.Fatalf("%v\n%s", err, query)
	}
}

// subscribe returns a channel of the events the bridge publishes from now on.
func (e *contractEnv) subscribe() chan hub.Event {
	ch := e.events.Subscribe()
	e.t.Cleanup(func() { e.events.Unsubscribe(ch) })
	return ch
}

// publish sends an event to the bridge's subscribers (SSE, webhook).
func (e *contractEnv) publish(evt hub.Event) {
	e.events.Publish(evt)
}

// setQR, setPairingCode and setSession drive the pairing and session state
// that /api/qr and /api/status report.
func (e *contractEnv) setQR(code string) {
	e.t.Helper()
	if err := e.pairing.SetQR(code); err != nil {
		e.t.Fatal(err)
	}
}

func (e *contractEnv) setPairingCode(code string, err error) {
	e.pairing.SetPairingCode(code, err)
}

func (e *contractEnv) setSession(state, reason string, needsAction bool) {
	e.bridge.SetSession(state, reason, needsAction)
}

// putMessageSecret records the secret of a message, as whatsmeow does when
// it decrypts one; votes, event responses and encrypted edits need it.
func (e *contractEnv) putMessageSecret(chat, sender types.JID, id string, secret []byte) {
	e.t.Helper()
	if err := e.client.Store.MsgSecrets.PutMessageSecret(context.Background(), chat, sender, id, secret); err != nil {
		e.t.Fatal(err)
	}
}

// drainEvents returns the events published to ch so far.
func drainEvents(ch chan hub.Event) []hub.Event {
	var out []hub.Event
	for {
		select {
		case evt := <-ch:
			out = append(out, evt)
		default:
			return out
		}
	}
}

// newWebhookDeliverer returns a function that delivers one event to url
// the way the bridge's webhook queue does, synchronously.
func newWebhookDeliverer(t *testing.T, url, secret string) func(hub.Event) error {
	t.Helper()
	w := hub.NewWebhook(url, secret, nil)
	t.Cleanup(func() { w.Close(context.Background()) })
	return func(evt hub.Event) error { return w.Deliver(context.Background(), evt) }
}

// ingest feeds one whatsmeow event to the bridge.
func (e *contractEnv) ingest(evt any) {
	e.bridge.HandleEvent(evt)
}

// dumpedTables are the tables of messages.db the ingest goldens show.
var dumpedTables = []string{"chats", "messages", "reactions", "polls", "poll_votes", "event_responses", "labels", "chat_labels", "bridge_meta"}

// dumpDB renders every row of messages.db: one JSON object per row, keys
// sorted by column name (column order differs between fresh and upgraded
// databases), rows ordered by primary key, BLOBs as hex.
func (e *contractEnv) dumpDB() string {
	e.t.Helper()
	return e.normalise(dumpTables(e.t, e.store.DB(), dumpedTables))
}

func dumpTables(t *testing.T, db *sql.DB, tables []string) string {
	t.Helper()
	var sb strings.Builder
	for _, table := range tables {
		cols, pk := tableColumns(t, db, table)
		if len(cols) == 0 {
			continue
		}
		fmt.Fprintf(&sb, "== %s\n", table)
		sorted := append([]string(nil), cols...)
		sort.Strings(sorted)
		exprs := make([]string, 0, 2*len(sorted))
		for _, c := range sorted {
			exprs = append(exprs, fmt.Sprintf("typeof(%q)", c),
				fmt.Sprintf("CASE typeof(%[1]q) WHEN 'blob' THEN hex(%[1]q) ELSE CAST(%[1]q AS TEXT) END", c))
		}
		order := "rowid"
		if len(pk) > 0 {
			order = strings.Join(quoteAll(pk), ", ")
		}
		query := fmt.Sprintf("SELECT %s FROM %q ORDER BY %s", strings.Join(exprs, ", "), table, order)
		eachRow(t, db, query, func(rows *sql.Rows) {
			vals := make([]sql.NullString, len(exprs))
			ptrs := make([]any, len(exprs))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			fields := make([]string, len(sorted))
			for i, c := range sorted {
				typ, val := vals[2*i].String, vals[2*i+1]
				var v string
				switch typ {
				case "null":
					v = "null"
				case "integer", "real":
					v = val.String
				case "blob":
					v = jsonString("x'" + val.String + "'")
				default:
					v = jsonString(val.String)
				}
				fields[i] = jsonString(c) + ":" + v
			}
			sb.WriteString("{" + strings.Join(fields, ",") + "}\n")
		})
	}
	return sb.String()
}

// eachRow runs query and calls scan for every row.
func eachRow(t *testing.T, db *sql.DB, query string, scan func(*sql.Rows)) {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("%v\n%s", err, query)
	}
	defer rows.Close()
	for rows.Next() {
		scan(rows)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

// tableColumns returns the columns of table and its primary key in key order.
func tableColumns(t *testing.T, db *sql.DB, table string) (cols, pk []string) {
	t.Helper()
	byPK := map[int]string{}
	eachRow(t, db, fmt.Sprintf("SELECT name, pk FROM pragma_table_info(%s)", sqlQuote(table)), func(rows *sql.Rows) {
		var name string
		var k int
		if err := rows.Scan(&name, &k); err != nil {
			t.Fatal(err)
		}
		cols = append(cols, name)
		if k > 0 {
			byPK[k] = name
		}
	})
	for i := 1; i <= len(byPK); i++ {
		pk = append(pk, byPK[i])
	}
	return cols, pk
}

func quoteAll(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = fmt.Sprintf("%q", n)
	}
	return out
}

func sqlQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func jsonString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			sb.WriteByte('\\')
			sb.WriteRune(r)
		case r == '\n':
			sb.WriteString(`\n`)
		case r < 0x20:
			fmt.Fprintf(&sb, `\u%04x`, r)
		default:
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}

// auditLines returns store/audit.log, normalised, and empties it so the next
// group of cases starts with a clean log.
func (e *contractEnv) auditLines() []string {
	e.t.Helper()
	data, err := os.ReadFile(filepath.Join(e.dir, "store", "audit.log"))
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(e.dir, "store", "audit.log"), 0); err != nil {
		e.t.Fatal(err)
	}
	text := strings.TrimSuffix(e.normalise(string(data)), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func (e *contractEnv) normalise(s string) string {
	return normalise(s, e.dir)
}

var (
	timestampRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?`)
	// whatsmeow's generated message ids.
	msgIDRE = regexp.MustCompile(`\b3EB0[0-9A-F]{8,}\b`)
)

// normalise is the only rewriting applied before comparing with a golden:
//   - dir (the temporary bridge directory) becomes <DIR>;
//   - RFC 3339 and SQLite timestamps within ten minutes of the test start
//     become <NOW> (fixture times are in 2025, far from any test run);
//   - message ids generated by whatsmeow (3EB0…) become <MSGID>.
//
// Status codes, headers, JSON key order and whitespace are left alone: they
// are the contract.
func normalise(s, dir string) string {
	if dir != "" {
		s = strings.ReplaceAll(s, dir, "<DIR>")
	}
	s = timestampRE.ReplaceAllStringFunc(s, func(m string) string {
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05.999999999Z07:00", "2006-01-02 15:04:05.999999999"} {
			if ts, err := time.Parse(layout, m); err == nil {
				if d := ts.Sub(contractStart); d > -10*time.Minute && d < 10*time.Minute {
					return "<NOW>"
				}
				return m
			}
		}
		return m
	})
	return msgIDRE.ReplaceAllString(s, "<MSGID>")
}

// checkGolden compares got with testdata/contract/<name>, or rewrites the
// file with -update.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, filepath.FromSlash(name))
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden (-want +got):\n%s", name, lineDiff(string(want), got))
	}
}

// goldenDir is absolute: the tests change the working directory.
var goldenDir = func() string {
	abs, err := filepath.Abs(filepath.Join("testdata", "contract"))
	if err != nil {
		panic(err)
	}
	return abs
}()

// lineDiff is a minimal unified-style diff of two texts, with three lines of
// context around each change.
func lineDiff(a, b string) string {
	x, y := strings.SplitAfter(a, "\n"), strings.SplitAfter(b, "\n")
	// Common prefix and suffix keep the quadratic part small.
	pre := 0
	for pre < len(x) && pre < len(y) && x[pre] == y[pre] {
		pre++
	}
	suf := 0
	for suf < len(x)-pre && suf < len(y)-pre && x[len(x)-1-suf] == y[len(y)-1-suf] {
		suf++
	}
	xm, ym := x[pre:len(x)-suf], y[pre:len(y)-suf]
	type op struct {
		kind byte
		line string
	}
	var ops []op
	if len(xm)*len(ym) > 4_000_000 {
		for _, l := range xm {
			ops = append(ops, op{'-', l})
		}
		for _, l := range ym {
			ops = append(ops, op{'+', l})
		}
	} else {
		lcs := make([][]int, len(xm)+1)
		for i := range lcs {
			lcs[i] = make([]int, len(ym)+1)
		}
		for i := len(xm) - 1; i >= 0; i-- {
			for j := len(ym) - 1; j >= 0; j-- {
				if xm[i] == ym[j] {
					lcs[i][j] = lcs[i+1][j+1] + 1
				} else {
					lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
				}
			}
		}
		i, j := 0, 0
		for i < len(xm) || j < len(ym) {
			switch {
			case i < len(xm) && j < len(ym) && xm[i] == ym[j]:
				ops = append(ops, op{' ', xm[i]})
				i++
				j++
			case j < len(ym) && (i == len(xm) || lcs[i][j+1] >= lcs[i+1][j]):
				ops = append(ops, op{'+', ym[j]})
				j++
			default:
				ops = append(ops, op{'-', xm[i]})
				i++
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "@@ line %d @@\n", pre+1)
	for k := max(0, pre-3); k < pre; k++ {
		sb.WriteString(" " + strings.TrimSuffix(x[k], "\n") + "\n")
	}
	for _, o := range ops {
		sb.WriteString(string(o.kind) + strings.TrimSuffix(o.line, "\n") + "\n")
	}
	for k := len(x) - suf; k < len(x) && k < len(x)-suf+3; k++ {
		sb.WriteString(" " + strings.TrimSuffix(x[k], "\n") + "\n")
	}
	return sb.String()
}
