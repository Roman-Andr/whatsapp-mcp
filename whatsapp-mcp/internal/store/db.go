package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/media/transcribe"
)

// DB is one account's store directory, read through a Pool and limited by
// the read allowlists.
type DB struct {
	pool  *Pool
	dir   string
	scope Scope
}

// NewDB reads the store in dir (messages.db, whatsapp.db, transcripts/).
func NewDB(pool *Pool, dir string, scope Scope) *DB {
	return &DB{pool: pool, dir: dir, scope: scope}
}

// Dir is the store directory.
func (d *DB) Dir() string { return d.dir }

// Log is the logger of the store's pool.
func (d *DB) Log() *slog.Logger { return d.pool.log }

func (d *DB) messagesDB() string { return filepath.Join(d.dir, "messages.db") }

// TranscriptsDir is where voice transcripts are cached.
func (d *DB) TranscriptsDir() string { return filepath.Join(d.dir, "transcripts") }

// CachedTranscript is the cached transcript of a voice message, "" when
// there is none.
func (d *DB) CachedTranscript(messageID, chatJID string) string {
	return transcribe.ReadCached(d.TranscriptsDir(), chatJID, messageID)
}

// Open is the connection to messages.db.
func (d *DB) Open(ctx context.Context) (*sql.DB, error) { return d.pool.Open(ctx, d.messagesDB()) }

// ReadScope is the set of chats this server may show, or nil for all.
func (d *DB) ReadScope(ctx context.Context, db *sql.DB) (Chats, error) { return d.readScope(ctx, db) }

// NewResolver resolves names for one tool call.
func (d *DB) NewResolver(ctx context.Context, db *sql.DB) *Names { return d.newResolver(ctx, db) }

// Meta is a bridge_meta value, "" when missing or unreadable.
func (d *DB) Meta(ctx context.Context, key string) string {
	db, err := d.Open(ctx)
	if err != nil {
		return ""
	}
	if ok, err := hasTable(ctx, db, "bridge_meta"); err != nil || !ok {
		return ""
	}
	r, err := queryRow(ctx, db, "SELECT value FROM bridge_meta WHERE key = ?", key)
	if err != nil || r == nil {
		return ""
	}
	return r.str("value")
}

// EventFilter is what the bridge's event stream may pass on: the bridge
// filters by its own read allowlist; this process's list is applied with
// the filter. A nil filter passes every chat.
func (d *DB) EventFilter(ctx context.Context) (func(chatJID string) bool, error) {
	envScope, err := d.scope.Env()
	if err != nil {
		return nil, err
	}
	if envScope == nil {
		return nil, nil
	}
	s := d.withAliases(ctx, envScope)
	return func(chatJID string) bool { return Readable(chatJID, s) }, nil
}
