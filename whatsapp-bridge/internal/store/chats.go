package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SaveChat records a chat. The name is only replaced by a non-empty one and
// the last message time only moves forward, so a history sync delivering an
// old batch cannot push an active chat down the list.
func (s *Store) SaveChat(ctx context.Context, jid, name string, lastMessageTime time.Time) error {
	var existing sql.NullTime
	err := s.db.QueryRowContext(ctx, "SELECT last_message_time FROM chats WHERE jid = ?", jid).Scan(&existing)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if errors.Is(err, sql.ErrNoRows) {
		_, err = s.db.ExecContext(ctx,
			"INSERT INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)",
			jid, name, lastMessageTime,
		)
		return err
	}
	newTime := lastMessageTime
	if existing.Valid && existing.Time.After(lastMessageTime) {
		newTime = existing.Time
	}
	_, err = s.db.ExecContext(ctx,
		"UPDATE chats SET name = COALESCE(NULLIF(?, ''), name), last_message_time = ? WHERE jid = ?",
		name, newTime, jid,
	)
	return err
}

// ChatsOnServer lists the stored chats whose JID is on server, e.g. "lid".
func (s *Store) ChatsOnServer(ctx context.Context, server string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT jid FROM chats WHERE jid LIKE ?", "%@"+server)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jids []string
	for rows.Next() {
		var jid string
		if err := rows.Scan(&jid); err != nil {
			return nil, err
		}
		jids = append(jids, jid)
	}
	return jids, rows.Err()
}

// MergeChat moves everything stored under chat from into chat to and drops
// from. Rows that exist under both keep the copy already in to. name, when
// set, is the merged chat's name; otherwise from's name is kept unless it is
// just from's own number, which means nothing under to.
func (s *Store) MergeChat(ctx context.Context, from, to, name string) error {
	// MergeChat keeps the known defect B5 (refactor plan): event_responses
	// and chat_labels rows stay under from.
	return s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO chats (jid, name, last_message_time)
			SELECT ?, COALESCE(NULLIF(?, ''), NULLIF(name, ?)), last_message_time FROM chats WHERE jid = ?
		ON CONFLICT(jid) DO UPDATE SET
			name = COALESCE(NULLIF(chats.name, ''), excluded.name),
			last_message_time = MAX(COALESCE(chats.last_message_time, excluded.last_message_time), COALESCE(excluded.last_message_time, chats.last_message_time))`,
			to, name, strings.SplitN(from, "@", 2)[0], from)
		if err != nil {
			return err
		}
		// The table names are the constants below, never user input.
		for _, table := range []string{"messages", "reactions", "polls", "poll_votes"} {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE OR IGNORE %s SET chat_jid = ? WHERE chat_jid = ?", table), to, from); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE chat_jid = ?", table), from); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM chats WHERE jid = ?", from)
		return err
	})
}

// AddUnread counts n more unread messages in a chat.
func (s *Store) AddUnread(ctx context.Context, chatJID string, n int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE chats SET unread_count = unread_count + ? WHERE jid = ?", n, chatJID)
	return err
}

// SetUnread sets a chat's unread count, e.g. to 0 once it was read.
func (s *Store) SetUnread(ctx context.Context, chatJID string, n int) error {
	_, err := s.db.ExecContext(ctx, "UPDATE chats SET unread_count = ? WHERE jid = ?", max(n, 0), chatJID)
	return err
}

// MarkUnread flags a chat that someone marked as unread on another device:
// it shows as unread even with nothing new in it.
func (s *Store) MarkUnread(ctx context.Context, chatJID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE chats SET unread_count = MAX(unread_count, 1) WHERE jid = ?", chatJID)
	return err
}

// SetChatFlag sets the archived or pinned flag of a chat.
func (s *Store) SetChatFlag(ctx context.Context, chatJID, flag string, on bool) error {
	if flag != "archived" && flag != "pinned" {
		return fmt.Errorf("unknown chat flag %q", flag)
	}
	// flag is one of the two column names checked above, never user input.
	_, err := s.db.ExecContext(ctx, fmt.Sprintf("UPDATE chats SET %s = ? WHERE jid = ?", flag), on, chatJID)
	return err
}

// SetMutedUntil records until when a chat is muted (unix ms, -1 forever, 0 not).
func (s *Store) SetMutedUntil(ctx context.Context, chatJID string, untilMS int64) error {
	_, err := s.db.ExecContext(ctx, "UPDATE chats SET muted_until = ? WHERE jid = ?", untilMS, chatJID)
	return err
}

// MarkChatDeleted hides a chat deleted on a device. Its messages are kept:
// the store is also the archive the agent reads.
func (s *Store) MarkChatDeleted(ctx context.Context, chatJID string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, "UPDATE chats SET deleted_at = ?, unread_count = 0, pinned = 0 WHERE jid = ?", at, chatJID)
	return err
}

// ClearChatDeleted shows a deleted chat again once a new message arrives,
// as WhatsApp does.
func (s *Store) ClearChatDeleted(ctx context.Context, chatJID string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE chats SET deleted_at = NULL WHERE jid = ? AND deleted_at IS NOT NULL", chatJID)
	return err
}

// ChatName returns the stored name of a chat, or "" if unknown.
func (s *Store) ChatName(ctx context.Context, jid string) string {
	var name sql.NullString
	// No row (or a failed query) means no stored name; the caller looks one up.
	_ = s.db.QueryRowContext(ctx, "SELECT name FROM chats WHERE jid = ?", jid).Scan(&name)
	return name.String
}
