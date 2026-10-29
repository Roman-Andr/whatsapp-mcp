package store

import (
	"context"
	"time"
)

// Reaction is one row of the reactions table.
type Reaction struct {
	MessageID  string
	ChatJID    string
	SenderJID  string
	SenderName string
	Emoji      string
	Timestamp  time.Time
}

// SaveReaction records a reaction; an empty emoji means it was removed.
// One reaction per person per message: a new emoji replaces the old one.
func (s *Store) SaveReaction(ctx context.Context, r Reaction) error {
	if r.Emoji == "" {
		_, err := s.db.ExecContext(ctx,
			"DELETE FROM reactions WHERE message_id = ? AND chat_jid = ? AND sender_jid = ?",
			r.MessageID, r.ChatJID, r.SenderJID,
		)
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO reactions (message_id, chat_jid, sender_jid, sender_name, emoji, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id, chat_jid, sender_jid) DO UPDATE SET
			emoji = excluded.emoji,
			timestamp = excluded.timestamp,
			sender_name = COALESCE(NULLIF(excluded.sender_name, ''), reactions.sender_name)
		WHERE excluded.timestamp >= reactions.timestamp`,
		r.MessageID, r.ChatJID, r.SenderJID, r.SenderName, r.Emoji, r.Timestamp,
	)
	return err
}
