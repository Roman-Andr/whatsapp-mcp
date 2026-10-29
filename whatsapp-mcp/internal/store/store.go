package store

// Database access. The bridge (../whatsapp-bridge) owns messages.db and
// writes it; this program only reads it. Everything that changes WhatsApp
// state goes through the bridge's HTTP API (internal/bridgeapi).

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unicode"

	sqlite3 "github.com/mattn/go-sqlite3"
	"golang.org/x/text/cases"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
)

// MaxLimit is the largest page any query returns.
const MaxLimit = 500

// registerDriver registers the sqlite3_whatsapp driver once per process:
// database/sql panics on a second registration of the name, so every Pool
// shares it.
var registerDriver = sync.OnceFunc(func() {
	sql.Register("sqlite3_whatsapp", &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			// SQLite's LOWER() only folds ASCII: "давид" did not find "Давид".
			return conn.RegisterFunc("ULOWER", fold, true)
		},
	})
})

// fold case-folds text for comparisons, for every script (Python's
// str.casefold); other values pass through.
func fold(v any) any {
	switch x := v.(type) {
	case string:
		return cases.Fold().String(x)
	case []byte:
		return x
	}
	return v
}

type cachedDB struct {
	db       *sql.DB
	dev, ino uint64
}

// Pool keeps the read-only connections of the store databases, one per
// path. The server owns one.
type Pool struct {
	log   *slog.Logger
	mu    sync.Mutex
	cache map[string]*cachedDB
}

// NewPool is an empty pool; the stores read through it log database
// errors to log.
func NewPool(log *slog.Logger) *Pool { return &Pool{log: log, cache: map[string]*cachedDB{}} }

// Close closes every connection.
func (p *Pool) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for path, c := range p.cache {
		c.db.Close()
		delete(p.cache, path)
	}
}

// Open opens a store database read-only; the bridge is the only writer.
//
// Connections are kept per path and reused by later calls; every statement
// runs in autocommit, so each query sees what the bridge wrote last. A file
// that was replaced (a new store) gets a new connection.
func (p *Pool) Open(ctx context.Context, path string) (*sql.DB, error) {
	registerDriver()
	var dev, ino uint64
	exists := false
	if st, err := os.Stat(path); err == nil {
		if sys, ok := st.Sys().(*syscall.Stat_t); ok {
			dev, ino = uint64(sys.Dev), sys.Ino //nolint:unconvert // Stat_t.Dev is int32 on darwin
		}
		exists = true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := p.cache[path]; c != nil {
		if exists && c.dev == dev && c.ino == ino {
			return c.db, nil
		}
		c.db.Close()
		delete(p.cache, path)
	}
	dsn := "file:" + (&url.URL{Path: path}).EscapedPath() + "?mode=ro&_busy_timeout=10000"
	db, err := sql.Open("sqlite3_whatsapp", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	if exists {
		p.cache[path] = &cachedDB{db: db, dev: dev, ino: ino}
	}
	return db, nil
}

// Contacts opens the bridge's contact store, whatsapp.db in dir: nil
// without an error when there is none (a bridge that never paired).
func (p *Pool) Contacts(ctx context.Context, dir string) (*sql.DB, error) {
	path := filepath.Join(dir, "whatsapp.db")
	if _, err := os.Stat(path); err != nil {
		return nil, nil
	}
	return p.Open(ctx, path)
}

// row is one result row by column name, like sqlite3.Row.
type row map[string]any

func queryRows(ctx context.Context, db *sql.DB, query string, args ...any) ([]row, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []row
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := row{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			r[c] = vals[i]
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func queryRow(ctx context.Context, db *sql.DB, query string, args ...any) (row, error) {
	rows, err := queryRows(ctx, db, query, args...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}

func (r row) str(key string) string { return pyfmt.AsString(r[key]) }

// nullStr is the column as a pointer: nil for NULL.
func (r row) nullStr(key string) *string {
	if r[key] == nil {
		return nil
	}
	s := pyfmt.AsString(r[key])
	return &s
}

func (r row) int(key string) int64 {
	switch x := r[key].(type) {
	case int64:
		return x
	case float64:
		return int64(x)
	case bool:
		if x {
			return 1
		}
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	}
	return 0
}

func (r row) bool(key string) bool {
	switch x := r[key].(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	}
	return r.int(key) != 0
}

func columns(ctx context.Context, db *sql.DB, table string) (map[string]bool, error) {
	rows, err := queryRows(ctx, db, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.str("name")] = true
	}
	return out, nil
}

func hasTable(ctx context.Context, db *sql.DB, name string) (bool, error) {
	r, err := queryRow(ctx, db, "SELECT 1 FROM sqlite_master WHERE name = ?", name)
	return r != nil, err
}

// ftsEnabled: the bridge maintains messages_fts only when built with FTS5.
func ftsEnabled(ctx context.Context, db *sql.DB) bool {
	for _, t := range []string{"bridge_meta", "messages_fts"} {
		if ok, _ := hasTable(ctx, db, t); !ok {
			return false
		}
	}
	r, err := queryRow(ctx, db, "SELECT value FROM bridge_meta WHERE key = 'fts_enabled'")
	return err == nil && r != nil && r.str("value") == "1"
}

var wordRe = regexp.MustCompile(`[\p{L}\p{N}_]+`)

// ftsQuery turns free text into an FTS5 query: every word, as a prefix.
func ftsQuery(text string) string {
	words := wordRe.FindAllString(text, -1)
	for i, w := range words {
		words[i] = `"` + strings.ReplaceAll(w, `"`, `""`) + `"*`
	}
	return strings.Join(words, " ")
}

func clamp(limit, page int) (int, int) {
	limit = max(1, min(limit, MaxLimit))
	page = max(0, page)
	return limit, page * limit
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ---------------------------------------------------------------------------
// Read allowlist

// The kinds of errors callers tell apart with errors.Is. The errors
// returned carry the exact text clients see; these only classify them.
var (
	// ErrReadScope means the read allowlist is misconfigured; nothing is
	// shown until it is fixed.
	ErrReadScope = errors.New("read allowlist misconfigured")
	// ErrInvalidArgument is a bad argument rather than a server fault.
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrNotFound is a missing message; one in a hidden chat is reported
	// exactly the same way.
	ErrNotFound = errors.New("not found")
)

// kindError is an error with a text of its own that is one of the kinds
// above.
type kindError struct {
	kind error
	msg  string
}

func (e *kindError) Error() string { return e.msg }
func (e *kindError) Unwrap() error { return e.kind }

func readScopeError(msg string) error { return &kindError{ErrReadScope, msg} }

// Chats is the set of readable chats; nil means every chat.
type Chats map[string]bool

func normalizeJID(entry string) (string, error) {
	entry = strings.ToLower(strings.TrimSpace(entry))
	user, server, found := strings.Cut(entry, "@")
	if !found {
		number := strings.TrimLeft(entry, "+")
		if number == "" || strings.IndexFunc(number, func(r rune) bool { return !unicode.IsDigit(r) }) >= 0 {
			return "", readScopeError(fmt.Sprintf("invalid read allowlist entry %s: neither a JID nor a phone number", pyfmt.Repr(entry)))
		}
		return number + "@s.whatsapp.net", nil
	}
	// A device suffix ("123:4@s.whatsapp.net") names the same chat.
	user, _, _ = strings.Cut(user, ":")
	return user + "@" + server, nil
}

// Scope is this process's read allowlist: WHATSAPP_READ_ALLOWLIST and
// WHATSAPP_READ_ALLOWLIST_FILE. The file is re-read on every query, so an
// edit takes effect at once.
type Scope struct {
	List, File string
}

// Env is the chats this process's own allowlist allows; nil for all.
func (sc Scope) Env() (Chats, error) {
	split := func(s string) []string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || unicode.IsSpace(r) })
	}
	entries := split(sc.List)
	if path := sc.File; path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // G304: the file WHATSAPP_READ_ALLOWLIST_FILE names, read as configured
		if err != nil {
			return nil, readScopeError(fmt.Sprintf("cannot read WHATSAPP_READ_ALLOWLIST_FILE: %v", err))
		}
		for _, line := range strings.SplitAfter(string(data), "\n") {
			line, _, _ = strings.Cut(line, "#")
			if strings.TrimSpace(line) != "" {
				entries = append(entries, line)
			}
		}
	}
	out := Chats{}
	for _, e := range entries {
		if strings.TrimSpace(e) == "" {
			continue
		}
		jid, err := normalizeJID(e)
		if err != nil {
			return nil, err
		}
		out[jid] = true
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// bridgeAllowlist is the bridge's read allowlist, published in bridge_meta.
func bridgeAllowlist(ctx context.Context, db *sql.DB) (Chats, error) {
	if ok, err := hasTable(ctx, db, "bridge_meta"); err != nil || !ok {
		return nil, err
	}
	r, err := queryRow(ctx, db, "SELECT value FROM bridge_meta WHERE key = 'read_allowlist'")
	if err != nil || r == nil || r.str("value") == "" {
		return nil, err
	}
	var jids []string
	if json.Unmarshal([]byte(r.str("value")), &jids) != nil {
		return nil, readScopeError("the bridge's read_allowlist in messages.db is unreadable")
	}
	out := Chats{}
	for _, j := range jids {
		jid, err := normalizeJID(j)
		if err != nil {
			return nil, readScopeError("the bridge's read_allowlist in messages.db is unreadable")
		}
		out[jid] = true
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// alias is the other form of a direct chat's JID from whatsapp.db's LID
// map: the phone-number JID of a LID and the other way round; "" when the
// map does not know it or jid is neither.
func alias(ctx context.Context, wa *sql.DB, jid string) (string, error) {
	user, server, _ := strings.Cut(jid, "@")
	var query, suffix string
	switch server {
	case "lid":
		query, suffix = "SELECT pn AS other FROM whatsmeow_lid_map WHERE lid = ?", "@s.whatsapp.net"
	case "s.whatsapp.net":
		query, suffix = "SELECT lid AS other FROM whatsmeow_lid_map WHERE pn = ?", "@lid"
	default:
		return "", nil
	}
	r, err := queryRow(ctx, wa, query, user)
	if err != nil || r == nil {
		return "", err
	}
	return r.str("other") + suffix, nil
}

// withAliases adds the other form of each direct chat (LID <-> phone
// number). The contact store is optional: when it is missing or fails,
// the chats keep the aliases found so far.
func (d *DB) withAliases(ctx context.Context, jids Chats) Chats {
	out := Chats{}
	for j := range jids {
		out[j] = true
	}
	wa, err := d.pool.Contacts(ctx, d.dir)
	if err != nil || wa == nil {
		return out
	}
	for jid := range jids {
		other, err := alias(ctx, wa, jid)
		if err != nil {
			return out
		}
		if other != "" {
			out[other] = true
		}
	}
	return out
}

// readScope is the set of chats this server may show, or nil for all.
//
// Both the bridge's list (WHATSAPP_READ_ALLOWLIST in the bridge's
// environment, published in messages.db) and this process's own
// WHATSAPP_READ_ALLOWLIST restrict; with both set a chat must be on both.
func (d *DB) readScope(ctx context.Context, db *sql.DB) (Chats, error) {
	var lists []Chats
	bridge, err := bridgeAllowlist(ctx, db)
	if err != nil {
		return nil, err
	}
	if bridge != nil {
		lists = append(lists, bridge)
	}
	env, err := d.scope.Env()
	if err != nil {
		return nil, err
	}
	if env != nil {
		lists = append(lists, env)
	}
	if len(lists) == 0 {
		return nil, nil
	}
	result := d.withAliases(ctx, lists[0])
	for _, other := range lists[1:] {
		aliased := d.withAliases(ctx, other)
		for j := range result {
			if !aliased[j] {
				delete(result, j)
			}
		}
	}
	return result, nil
}

// scopeClause restricts column to the readable chats ("" when unrestricted).
func scopeClause(column string, s Chats) (string, []any) {
	if s == nil {
		return "", nil
	}
	if len(s) == 0 {
		return "0", nil
	}
	jids := slices.Sorted(maps.Keys(s))
	args := make([]any, len(jids))
	for i, j := range jids {
		args[i] = j
	}
	return fmt.Sprintf("%s IN (%s)", column, placeholders(len(jids))), args
}

// Readable says whether the chat jid is in s.
func Readable(jid string, s Chats) bool {
	if s == nil {
		return true
	}
	if jid == "" {
		return false
	}
	n, err := normalizeJID(jid)
	return err == nil && s[n]
}

// HiddenMessage is the refusal for a chat outside the read allowlist.
func HiddenMessage(jid string) string {
	return fmt.Sprintf("Chat %s is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)", jid)
}

// CheckReadable returns an error message when chatJID may not be read.
func (d *DB) CheckReadable(ctx context.Context, chatJID string) string {
	var s Chats
	var err error
	if db, cerr := d.Open(ctx); cerr == nil {
		s, err = d.readScope(ctx, db)
	} else {
		s, err = d.scope.Env()
	}
	if err != nil {
		return err.Error()
	}
	if Readable(chatJID, s) {
		return ""
	}
	return HiddenMessage(chatJID)
}

// ScopeOrNil is the read Chats, nil when it cannot be determined.
func (d *DB) ScopeOrNil(ctx context.Context) Chats {
	db, err := d.Open(ctx)
	if err != nil {
		return nil
	}
	s, err := d.readScope(ctx, db)
	if err != nil {
		return nil
	}
	return s
}
