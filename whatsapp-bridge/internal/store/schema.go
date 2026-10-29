package store

import (
	"context"
	"fmt"
)

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);

		CREATE TABLE IF NOT EXISTS reactions (
			message_id TEXT,
			chat_jid TEXT,
			sender_jid TEXT,
			sender_name TEXT,
			emoji TEXT,
			timestamp TIMESTAMP,
			PRIMARY KEY (message_id, chat_jid, sender_jid)
		);

		CREATE TABLE IF NOT EXISTS polls (
			message_id TEXT,
			chat_jid TEXT,
			question TEXT,
			options TEXT, -- JSON array of option names, in order
			selectable_count INTEGER,
			PRIMARY KEY (message_id, chat_jid)
		);

		CREATE TABLE IF NOT EXISTS poll_votes (
			message_id TEXT,
			chat_jid TEXT,
			voter_jid TEXT,
			voter_name TEXT,
			options TEXT, -- JSON array of the option names chosen
			timestamp TIMESTAMP,
			PRIMARY KEY (message_id, chat_jid, voter_jid)
		);

		-- Answers to WhatsApp events (meetings with a date): each person's current one.
		CREATE TABLE IF NOT EXISTS event_responses (
			message_id TEXT,
			chat_jid TEXT,
			responder_jid TEXT,
			responder_name TEXT,
			response TEXT, -- going, not going, maybe
			extra_guests INTEGER,
			timestamp TIMESTAMP,
			PRIMARY KEY (message_id, chat_jid, responder_jid)
		);

		-- WhatsApp Business labels and which chats carry them.
		CREATE TABLE IF NOT EXISTS labels (
			id TEXT PRIMARY KEY,
			name TEXT,
			color INTEGER,
			deleted INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS chat_labels (
			chat_jid TEXT,
			label_id TEXT,
			PRIMARY KEY (chat_jid, label_id)
		);

		CREATE TABLE IF NOT EXISTS bridge_meta (
			key TEXT PRIMARY KEY,
			value TEXT
		);
	`)
	if err != nil {
		return fmt.Errorf("failed to create tables: %w", err)
	}

	for _, col := range messageColumns {
		if err := s.ensureColumn(ctx, "messages", col.name, col.decl); err != nil {
			return err
		}
	}
	for _, col := range chatColumns {
		if err := s.ensureColumn(ctx, "chats", col.name, col.decl); err != nil {
			return err
		}
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS idx_messages_chat_time ON messages(chat_jid, timestamp);
		CREATE INDEX IF NOT EXISTS idx_messages_time ON messages(timestamp);
		CREATE INDEX IF NOT EXISTS idx_messages_sender ON messages(sender);
		CREATE INDEX IF NOT EXISTS idx_messages_id ON messages(id);
		CREATE INDEX IF NOT EXISTS idx_reactions_message ON reactions(chat_jid, message_id);
	`)
	if err != nil {
		return fmt.Errorf("failed to create indexes: %w", err)
	}

	return s.setupFullTextSearch(ctx)
}
