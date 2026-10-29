package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// setupFullTextSearch maintains messages_fts, an FTS5 index over message text.
//
// FTS5 is only compiled into go-sqlite3 with the sqlite_fts5 build tag. A
// binary built without it must not leave the triggers in place, or every
// insert into messages would fail with "no such module: fts5". The
// bridge_meta row fts_enabled tells the MCP server whether the index is
// current; when it is not, the server falls back to LIKE search.
func (s *Store) setupFullTextSearch(ctx context.Context) error {
	_, probeErr := s.db.ExecContext(ctx, "CREATE VIRTUAL TABLE IF NOT EXISTS temp.fts_probe USING fts5(x)")
	if probeErr != nil {
		_, err := s.db.ExecContext(ctx, `
			DROP TRIGGER IF EXISTS messages_fts_insert;
			DROP TRIGGER IF EXISTS messages_fts_delete;
			DROP TRIGGER IF EXISTS messages_fts_update;
			INSERT INTO bridge_meta (key, value) VALUES ('fts_enabled', '0')
				ON CONFLICT(key) DO UPDATE SET value = excluded.value;
		`)
		if err != nil {
			return fmt.Errorf("failed to disable full-text search: %w", err)
		}
		s.log.Info("full-text search disabled: bridge built without the sqlite_fts5 tag")
		return nil
	}
	if _, err := s.db.ExecContext(ctx, "DROP TABLE IF EXISTS temp.fts_probe"); err != nil {
		return fmt.Errorf("failed to drop the FTS5 probe table: %w", err)
	}

	var wasEnabled string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM bridge_meta WHERE key = 'fts_enabled'").Scan(&wasEnabled)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
			content, content='messages', content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		);
		CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
		END;
		CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
		END;
		CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE OF content ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
			INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
		END;
	`)
	if err != nil {
		return fmt.Errorf("failed to create full-text index: %w", err)
	}

	// The index is stale whenever rows were written without the triggers:
	// on first creation, and after running a binary built without FTS5.
	if wasEnabled != "1" {
		if _, err := s.db.ExecContext(ctx, "INSERT INTO messages_fts(messages_fts) VALUES ('rebuild')"); err != nil {
			return fmt.Errorf("failed to build full-text index: %w", err)
		}
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO bridge_meta (key, value) VALUES ('fts_enabled', '1')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	s.fts = true
	return nil
}
