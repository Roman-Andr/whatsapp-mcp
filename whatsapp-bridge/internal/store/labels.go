package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SaveLabel records a label's name and colour, or that it was deleted.
func (s *Store) SaveLabel(ctx context.Context, id, name string, color int32, deleted bool) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO labels (id, name, color, deleted) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = COALESCE(NULLIF(excluded.name, ''), labels.name),
			color = excluded.color, deleted = excluded.deleted`, id, name, color, deleted)
	if err == nil && deleted {
		_, err = s.db.ExecContext(ctx, "DELETE FROM chat_labels WHERE label_id = ?", id)
	}
	return err
}

// SetChatLabel adds or removes a label from a chat.
func (s *Store) SetChatLabel(ctx context.Context, chatJID, labelID string, labeled bool) error {
	if !labeled {
		_, err := s.db.ExecContext(ctx, "DELETE FROM chat_labels WHERE chat_jid = ? AND label_id = ?", chatJID, labelID)
		return err
	}
	_, err := s.db.ExecContext(ctx, "INSERT OR IGNORE INTO chat_labels (chat_jid, label_id) VALUES (?, ?)", chatJID, labelID)
	return err
}

// FindLabel returns the id of the live label with this id or name. Names
// are compared in Go: SQLite's LOWER() only folds ASCII.
func (s *Store) FindLabel(ctx context.Context, label string) (string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, COALESCE(name, '') FROM labels WHERE NOT deleted ORDER BY CAST(id AS INTEGER)")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	byName := ""
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return "", err
		}
		if id == label {
			return id, nil
		}
		if byName == "" && strings.EqualFold(name, label) {
			byName = id
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if byName == "" {
		return "", sql.ErrNoRows
	}
	return byName, nil
}

// Label returns a label's name.
func (s *Store) Label(ctx context.Context, id string) (string, error) {
	var name sql.NullString
	err := s.db.QueryRowContext(ctx, "SELECT name FROM labels WHERE id = ?", id).Scan(&name)
	return name.String, err
}

// NextLabelID picks an id for a new label: WhatsApp uses small integers.
func (s *Store) NextLabelID(ctx context.Context) (string, error) {
	var highest sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MAX(CAST(id AS INTEGER)) FROM labels").Scan(&highest)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(highest.Int64 + 1), nil
}
