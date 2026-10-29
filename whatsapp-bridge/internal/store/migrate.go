package store

import (
	"context"
	"database/sql"
	"fmt"
)

// messageColumns are the columns added after the original upstream schema.
// They are applied with ALTER TABLE so an existing messages.db keeps its data.
var messageColumns = []struct{ name, decl string }{
	{"sender_jid", "TEXT"},
	{"sender_name", "TEXT"},
	{"direct_path", "TEXT"},
	{"mimetype", "TEXT"},
	{"quoted_id", "TEXT"},
	{"quoted_sender", "TEXT"},
	{"quoted_text", "TEXT"},
	{"mentions", "TEXT"},
	{"edited_at", "TIMESTAMP"},
	{"deleted_at", "TIMESTAMP"},
	{"media_width", "INTEGER"},
	{"media_height", "INTEGER"},
	{"media_seconds", "INTEGER"},
	{"media_detail", "TEXT"},
	{"starred", "INTEGER NOT NULL DEFAULT 0"},
}

// chatColumns are the chats columns added after the upstream schema.
var chatColumns = []struct{ name, decl string }{
	// Messages others sent since the chat was last read, here or on another
	// device of the account.
	{"unread_count", "INTEGER NOT NULL DEFAULT 0"},
	// State set on any device of the account (or through /api/chat).
	{"archived", "INTEGER NOT NULL DEFAULT 0"},
	{"pinned", "INTEGER NOT NULL DEFAULT 0"},
	{"muted_until", "INTEGER NOT NULL DEFAULT 0"}, // unix ms; -1 forever, 0 not muted
	// Deleted on a device: hidden from chat lists until a new message
	// arrives. The messages are kept.
	{"deleted_at", "TIMESTAMP"},
}

// ensureColumn adds a column unless the table already has it.
func (s *Store) ensureColumn(ctx context.Context, table, column, decl string) error {
	// table, column and decl come from the tables above, never user input.
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("failed to inspect %s: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	// Release the connection before altering the table.
	rows.Close()
	if _, err := s.db.ExecContext(ctx, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl)); err != nil {
		return fmt.Errorf("failed to add %s.%s: %w", table, column, err)
	}
	return nil
}
