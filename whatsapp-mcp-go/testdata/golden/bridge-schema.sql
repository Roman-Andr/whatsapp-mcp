CREATE TABLE chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		, unread_count INTEGER NOT NULL DEFAULT 0, archived INTEGER NOT NULL DEFAULT 0, pinned INTEGER NOT NULL DEFAULT 0, muted_until INTEGER NOT NULL DEFAULT 0, deleted_at TIMESTAMP);
CREATE TABLE messages (
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
			file_length INTEGER, sender_jid TEXT, sender_name TEXT, direct_path TEXT, mimetype TEXT, quoted_id TEXT, quoted_sender TEXT, quoted_text TEXT, mentions TEXT, edited_at TIMESTAMP, deleted_at TIMESTAMP, media_width INTEGER, media_height INTEGER, media_seconds INTEGER, media_detail TEXT, starred INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);
CREATE TABLE reactions (
			message_id TEXT,
			chat_jid TEXT,
			sender_jid TEXT,
			sender_name TEXT,
			emoji TEXT,
			timestamp TIMESTAMP,
			PRIMARY KEY (message_id, chat_jid, sender_jid)
		);
CREATE TABLE polls (
			message_id TEXT,
			chat_jid TEXT,
			question TEXT,
			options TEXT, -- JSON array of option names, in order
			selectable_count INTEGER,
			PRIMARY KEY (message_id, chat_jid)
		);
CREATE TABLE poll_votes (
			message_id TEXT,
			chat_jid TEXT,
			voter_jid TEXT,
			voter_name TEXT,
			options TEXT, -- JSON array of the option names chosen
			timestamp TIMESTAMP,
			PRIMARY KEY (message_id, chat_jid, voter_jid)
		);
CREATE TABLE event_responses (
			message_id TEXT,
			chat_jid TEXT,
			responder_jid TEXT,
			responder_name TEXT,
			response TEXT, -- going, not going, maybe
			extra_guests INTEGER,
			timestamp TIMESTAMP,
			PRIMARY KEY (message_id, chat_jid, responder_jid)
		);
CREATE TABLE labels (
			id TEXT PRIMARY KEY,
			name TEXT,
			color INTEGER,
			deleted INTEGER NOT NULL DEFAULT 0
		);
CREATE TABLE chat_labels (
			chat_jid TEXT,
			label_id TEXT,
			PRIMARY KEY (chat_jid, label_id)
		);
CREATE TABLE bridge_meta (
			key TEXT PRIMARY KEY,
			value TEXT
		);
CREATE INDEX idx_messages_chat_time ON messages(chat_jid, timestamp);
CREATE INDEX idx_messages_time ON messages(timestamp);
CREATE INDEX idx_messages_sender ON messages(sender);
CREATE INDEX idx_messages_id ON messages(id);
CREATE INDEX idx_reactions_message ON reactions(chat_jid, message_id);
CREATE VIRTUAL TABLE messages_fts USING fts5(
			content, content='messages', content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		);
CREATE TRIGGER messages_fts_insert AFTER INSERT ON messages BEGIN
			INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
		END;
CREATE TRIGGER messages_fts_delete AFTER DELETE ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
		END;
CREATE TRIGGER messages_fts_update AFTER UPDATE OF content ON messages BEGIN
			INSERT INTO messages_fts(messages_fts, rowid, content) VALUES ('delete', old.rowid, old.content);
			INSERT INTO messages_fts(rowid, content) VALUES (new.rowid, new.content);
		END;
