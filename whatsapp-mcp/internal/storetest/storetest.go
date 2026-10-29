package storetest

import (
	"database/sql"
	_ "embed"
	"encoding/json"
	"path/filepath"
	"testing"

	// The plain sqlite3 driver: the fixture writes the stores as the bridge does.
	_ "github.com/mattn/go-sqlite3"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/account"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
)

// BridgeSchema is the schema of a fresh bridge store (the bridge's own
// DDL, dumped from a store it created).
//
//go:embed bridge-schema.sql
var BridgeSchema string

// UpstreamSchema is the schema of the original upstream bridge, without
// any of the columns and tables added since.
const UpstreamSchema = `
CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);
CREATE TABLE messages (id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
    is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT, media_key BLOB, file_sha256 BLOB,
    file_enc_sha256 BLOB, file_length INTEGER, PRIMARY KEY (id, chat_jid));
`

// Group is the fixture's group chat.
const Group = "120363431597928274@g.us"

// Rows are the fixture rows of messages.db. Every INSERT names its
// columns: the real schema's column order is the bridge's, not the order
// a hand-written schema would have.
const Rows = `
INSERT INTO bridge_meta (key, value) VALUES ('fts_enabled', '1');
INSERT INTO chats (jid, name, last_message_time) VALUES ('` + Group + `', 'Snow operations system', '2026-10-27 12:05:00+03:00');
INSERT INTO chats (jid, name, last_message_time) VALUES ('32547396399312@lid', '32547396399312', '2026-10-27 11:00:00+03:00');
INSERT INTO messages (id, chat_jid, sender, sender_jid, sender_name, content, timestamp, is_from_me)
    VALUES ('A1', '` + Group + `', '32547396399312', '32547396399312@lid', NULL,
            'The plow route on Main street is wrong', '2026-10-27 12:00:00.123456789+03:00', 0);
INSERT INTO messages (id, chat_jid, sender, sender_jid, sender_name, content, timestamp, is_from_me,
                      quoted_id, quoted_sender, quoted_text, edited_at)
    VALUES ('A2', '` + Group + `', '79966764464', '79966764464@s.whatsapp.net', 'Roman',
            'Fixed it', '2026-10-27 12:01:00+03:00', 1,
            'A1', '32547396399312@lid', 'The plow route on Main street is wrong',
            '2026-10-27 12:02:00+03:00');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, deleted_at)
    VALUES ('A3', '` + Group + `', '28952508764308', 'see screenshot', '2026-10-27 12:05:00+03:00', 0,
            'image', 'image_1.jpg', '2026-10-27 12:06:00+03:00');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('D1', '32547396399312@lid', '32547396399312', 'direct hello', '2026-10-27 11:00:00+03:00', 0);
INSERT INTO reactions (message_id, chat_jid, sender_jid, sender_name, emoji, timestamp)
    VALUES ('A2', '` + Group + `', '32547396399312@lid', 'David Bender', '👍', '2026-10-27 12:03:00+03:00');
`

// ContactsSchema is whatsmeow's contact store and LID map, with the
// fixture's contacts.
const ContactsSchema = `
CREATE TABLE whatsmeow_contacts (our_jid TEXT, their_jid TEXT, first_name TEXT, full_name TEXT,
    push_name TEXT, business_name TEXT, redacted_phone TEXT);
CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL);
INSERT INTO whatsmeow_lid_map VALUES ('32547396399312', '18046727669');
INSERT INTO whatsmeow_lid_map VALUES ('28952508764308', '375293714259');
INSERT INTO whatsmeow_contacts VALUES ('me', '18046727669@s.whatsapp.net', 'David', 'David Bender', NULL, NULL, NULL);
INSERT INTO whatsmeow_contacts VALUES ('me', '28952508764308@lid', NULL, NULL, 'Antonina', NULL, NULL);
`

// Unreachable is an API URL nothing listens on.
const Unreachable = "http://127.0.0.1:1/api"

// Options change the store New builds.
type Options struct {
	// Schema replaces BridgeSchema (UpstreamSchema for an old store).
	Schema string
	// Empty leaves out Rows and whatsapp.db.
	Empty bool
}

// Fixture is a store directory used as the default account.
type Fixture struct {
	t   testing.TB
	Dir string
	// Config are the settings the server would read from the
	// environment: the store is the default account's and its bridge is
	// Unreachable until a fake bridge is attached (NewBridge). Tests set
	// the other fields (read allowlist, accounts) as the environment
	// would.
	Config *config.Config
	// Pool is the fixture's connection pool, closed at the end of the test.
	Pool *store.Pool
}

// New builds the store in a temp dir.
func New(tb testing.TB, opts Options) *Fixture {
	tb.Helper()
	t := tb
	dir := t.TempDir()
	f := &Fixture{t: t, Dir: dir, Config: &config.Config{StoreDir: dir, APIBaseURL: Unreachable}, Pool: store.NewPool(logging.Discard())}
	t.Cleanup(f.Pool.Close)
	schema := opts.Schema
	if schema == "" {
		schema = BridgeSchema
	}
	if opts.Empty {
		f.Exec(schema)
		return f
	}
	f.Exec(schema + Rows)
	Exec(t, f.WhatsappDB(), ContactsSchema)
	return f
}

// MessagesDB is the path of messages.db.
func (f *Fixture) MessagesDB() string { return filepath.Join(f.Dir, "messages.db") }

// WhatsappDB is the path of whatsapp.db.
func (f *Fixture) WhatsappDB() string { return filepath.Join(f.Dir, "whatsapp.db") }

// Exec runs statements against a store database with a writable connection.
func Exec(tb testing.TB, path, statements string, args ...any) {
	tb.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		tb.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(tb.Context(), statements, args...); err != nil {
		tb.Fatalf("%v\n%s", err, statements)
	}
}

// Exec runs statements against messages.db.
func (f *Fixture) Exec(statements string, args ...any) {
	f.t.Helper()
	Exec(f.t, f.MessagesDB(), statements, args...)
}

// PublishReadAllowlist stores the bridge's read allowlist as the bridge does.
func (f *Fixture) PublishReadAllowlist(jids ...string) {
	data, _ := json.Marshal(jids)
	f.Exec("INSERT OR REPLACE INTO bridge_meta (key, value) VALUES ('read_allowlist', ?)", string(data))
}

// PublishMediaRoots stores the bridge's media roots and outbox.
func (f *Fixture) PublishMediaRoots(roots []string, outbox string) {
	data, _ := json.Marshal(roots)
	f.Exec("INSERT OR REPLACE INTO bridge_meta (key, value) VALUES ('media_roots', ?)", string(data))
	if outbox != "" {
		f.Exec("INSERT OR REPLACE INTO bridge_meta (key, value) VALUES ('media_outbox', ?)", outbox)
	}
}

// Registry resolves accounts with the fixture's current settings.
func (f *Fixture) Registry() *account.Registry {
	c := f.Config
	return &account.Registry{StoreDir: c.StoreDir, APIURL: c.APIBaseURL, BridgeToken: c.BridgeToken,
		Accounts: c.Accounts, AccountsFile: c.AccountsFile, Default: c.DefaultAccount}
}

// Account is the default account with the current settings.
func (f *Fixture) Account() *account.Account {
	f.t.Helper()
	a, err := f.Registry().Resolve("")
	if err != nil {
		f.t.Fatal(err)
	}
	return a
}

// Scope is the read allowlist of the current settings.
func (f *Fixture) Scope() store.Scope {
	return store.Scope{List: f.Config.ReadAllowlist, File: f.Config.ReadAllowlistFile}
}

// DB is the default account's store with the current settings.
func (f *Fixture) DB() *store.DB { return store.NewDB(f.Pool, f.Account().StoreDir, f.Scope()) }
