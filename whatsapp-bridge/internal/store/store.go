package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	// The sqlite3 driver; FTS5 needs the sqlite_fts5 build tag.
	_ "github.com/mattn/go-sqlite3"
)

// Store persists chats, messages and reactions in <dir>/messages.db.
// The MCP server reads the same file directly, so every column added here is
// part of the contract with whatsapp-mcp (store.go and its queries).
type Store struct {
	db  *sql.DB
	log *slog.Logger
	// fts reports whether the messages_fts full-text index is maintained.
	fts bool
}

// Option configures Open.
type Option func(*Store)

// WithLogger sends the store's warnings to l instead of discarding them.
func WithLogger(l *slog.Logger) Option {
	return func(s *Store) { s.log = l }
}

// Open opens <dir>/messages.db, creating dir (mode 0700) when missing, and
// brings its schema up to date. Production passes "store", the bridge's
// store/ directory, which keeps the DSN the MCP server and older bridges
// know.
func Open(ctx context.Context, dir string, opts ...Option) (*Store, error) {
	s := &Store{log: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(s)
	}
	if err := EnsureDir(dir); err != nil {
		return nil, err
	}
	// WAL lets the MCP server read while the bridge writes; the busy timeout
	// covers the short moments where both need the write lock. For "store"
	// the DSN is file:store/messages.db?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL,
	// as it always was.
	db, err := sql.Open("sqlite3", "file:"+dir+"/messages.db?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL")
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %w", err)
	}
	s.db = db
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	RestrictFiles(filepath.Join(dir, "messages.db"), s.log)
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// FullTextSearch reports whether the messages_fts full-text index is
// maintained (the binary was built with FTS5).
func (s *Store) FullTextSearch() bool {
	return s.fts
}

// DB is the underlying database, for tests and the contract harness that
// inspect rows directly. The bridge itself goes through the methods.
func (s *Store) DB() *sql.DB {
	return s.db
}

// withTx runs fn in a transaction, committed when fn returns nil.
func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// EnsureDir creates dir readable by this user only. It holds the session
// keys (whatsapp.db), every message and every downloaded file, so a
// directory other local users can read hands them the account.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create store directory: %w", err)
	}
	// MkdirAll leaves an existing directory alone; tighten one made by an
	// older bridge.
	if err := os.Chmod(dir, 0700); err != nil { //nolint:gosec // a directory: 0700 is the private mode
		return fmt.Errorf("failed to restrict store directory: %w", err)
	}
	return nil
}

// RestrictFiles makes a database and its WAL files private. SQLite creates
// them with the umask's permissions. Failures are logged to log: the
// database works either way.
func RestrictFiles(path string, log *slog.Logger) {
	for _, p := range []string{path, path + "-wal", path + "-shm", path + "-journal"} {
		if err := os.Chmod(p, 0600); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Warn("failed to restrict database file", "path", p, "err", err)
		}
	}
}
