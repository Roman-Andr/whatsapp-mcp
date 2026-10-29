package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
)

// Message is one row of the messages table.
type Message struct {
	ID         string
	ChatJID    string
	Sender     string // user part of the sender JID (kept for backwards compatibility)
	SenderJID  string // full sender JID, needed to react to or quote the message
	SenderName string // display name resolved when the message was received
	Content    string
	Timestamp  time.Time
	IsFromMe   bool

	Media content.MediaInfo

	QuotedID     string // id of the message this one replies to
	QuotedSender string // JID of the author of the quoted message
	QuotedText   string // text of the quoted message as embedded in the reply
	Mentions     []string
}

// SaveMessage inserts a message or refreshes an existing one.
//
// It is an upsert rather than INSERT OR REPLACE: a replace deletes the row
// first, which loses local state (edits, deletions, the file name a download
// was saved under) and does not fire the full-text delete trigger.
func (s *Store) SaveMessage(ctx context.Context, m Message) error {
	if m.Content == "" && m.Media.Type == "" {
		return nil
	}
	var mentions any
	if len(m.Mentions) > 0 {
		encoded, err := json.Marshal(m.Mentions)
		if err != nil {
			return err
		}
		mentions = string(encoded)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (
			id, chat_jid, sender, sender_jid, sender_name, content, timestamp, is_from_me,
			media_type, filename, url, direct_path, mimetype, media_key, file_sha256, file_enc_sha256, file_length,
			quoted_id, quoted_sender, quoted_text, mentions,
			media_width, media_height, media_seconds, media_detail
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, chat_jid) DO UPDATE SET
			sender = excluded.sender,
			sender_jid = COALESCE(NULLIF(excluded.sender_jid, ''), messages.sender_jid),
			sender_name = COALESCE(NULLIF(excluded.sender_name, ''), messages.sender_name),
			content = CASE WHEN messages.edited_at IS NOT NULL THEN messages.content ELSE excluded.content END,
			timestamp = excluded.timestamp,
			is_from_me = excluded.is_from_me,
			media_type = excluded.media_type,
			filename = COALESCE(NULLIF(messages.filename, ''), excluded.filename),
			url = COALESCE(NULLIF(excluded.url, ''), messages.url),
			direct_path = COALESCE(NULLIF(messages.direct_path, ''), excluded.direct_path),
			mimetype = COALESCE(NULLIF(excluded.mimetype, ''), messages.mimetype),
			media_key = COALESCE(excluded.media_key, messages.media_key),
			file_sha256 = COALESCE(excluded.file_sha256, messages.file_sha256),
			file_enc_sha256 = COALESCE(excluded.file_enc_sha256, messages.file_enc_sha256),
			file_length = COALESCE(NULLIF(excluded.file_length, 0), messages.file_length),
			quoted_id = COALESCE(NULLIF(excluded.quoted_id, ''), messages.quoted_id),
			quoted_sender = COALESCE(NULLIF(excluded.quoted_sender, ''), messages.quoted_sender),
			quoted_text = COALESCE(NULLIF(excluded.quoted_text, ''), messages.quoted_text),
			mentions = COALESCE(excluded.mentions, messages.mentions),
			media_width = COALESCE(excluded.media_width, messages.media_width),
			media_height = COALESCE(excluded.media_height, messages.media_height),
			media_seconds = COALESCE(excluded.media_seconds, messages.media_seconds),
			media_detail = COALESCE(NULLIF(excluded.media_detail, ''), messages.media_detail)`,
		m.ID, m.ChatJID, m.Sender, m.SenderJID, m.SenderName, m.Content, m.Timestamp, m.IsFromMe,
		m.Media.Type, m.Media.Filename, m.Media.URL, m.Media.DirectPath, m.Media.Mimetype,
		m.Media.MediaKey, m.Media.FileSHA256, m.Media.FileEncSHA256, m.Media.FileLength,
		m.QuotedID, m.QuotedSender, m.QuotedText, mentions,
		nullIfZero(m.Media.Width), nullIfZero(m.Media.Height), nullIfZero(m.Media.Seconds), m.Media.Detail,
	)
	return err
}

// SavePlaceholder stores m unless a message with its id is already there,
// reporting whether it did. It never overwrites a real message.
func (s *Store) SavePlaceholder(ctx context.Context, m Message) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, chat_jid, sender, sender_jid, sender_name, content, timestamp, is_from_me)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id, chat_jid) DO NOTHING`,
		m.ID, m.ChatJID, m.Sender, m.SenderJID, m.SenderName, m.Content, m.Timestamp, m.IsFromMe,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func nullIfZero(n uint32) any {
	if n == 0 {
		return nil
	}
	return n
}

// Author says who sent an edit or revoke. Only the author of a message may
// change it: anyone in a group can send a protocol message naming any message
// id, and neither WhatsApp nor whatsmeow checks that it is theirs.
type Author struct {
	FromMe bool
	JIDs   []string // the sender's JIDs, every form it is known by (LID, phone number)
}

// clause restricts an UPDATE to messages by the author. A nil author (an
// admin deleting someone's message, checked by the caller) matches any.
func (a *Author) clause() (string, []any) {
	if a == nil {
		return "", nil
	}
	if a.FromMe {
		return " AND is_from_me = 1", nil
	}
	if len(a.JIDs) == 0 {
		return " AND 0", nil
	}
	var args []any
	marks := make([]string, 0, len(a.JIDs))
	users := make([]string, 0, len(a.JIDs))
	for _, jid := range a.JIDs {
		marks = append(marks, "?")
		args = append(args, jid)
	}
	for _, jid := range a.JIDs {
		users = append(users, "?")
		user, _, _ := strings.Cut(jid, "@")
		args = append(args, user)
	}
	// Rows from before sender_jid existed only have the user part in sender.
	return fmt.Sprintf(" AND NOT is_from_me AND (sender_jid IN (%s) OR (COALESCE(sender_jid, '') = '' AND sender IN (%s)))",
		strings.Join(marks, ","), strings.Join(users, ",")), args
}

// EditMessage replaces the text of an edited message. It reports whether the
// original message was known (and by that author).
func (s *Store) EditMessage(ctx context.Context, id, chatJID, content string, editedAt time.Time, author *Author) (bool, error) {
	cond, extra := author.clause()
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET content = ?, edited_at = ? WHERE id = ? AND chat_jid = ?"+cond, //nolint:gosec // cond is fixed SQL with placeholders (Author.clause)
		append([]any{content, editedAt, id, chatJID}, extra...)...,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkDeleted flags a message revoked by its sender. The original text is
// kept: knowing what was retracted is often the point of reading a chat log.
func (s *Store) MarkDeleted(ctx context.Context, id, chatJID string, deletedAt time.Time, author *Author) (bool, error) {
	cond, extra := author.clause()
	res, err := s.db.ExecContext(ctx,
		"UPDATE messages SET deleted_at = ? WHERE id = ? AND chat_jid = ?"+cond, //nolint:gosec // cond is fixed SQL with placeholders (Author.clause)
		append([]any{deletedAt, id, chatJID}, extra...)...,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetStarred records whether a message is starred.
func (s *Store) SetStarred(ctx context.Context, id, chatJID string, starred bool) error {
	_, err := s.db.ExecContext(ctx, "UPDATE messages SET starred = ? WHERE id = ? AND chat_jid = ?", starred, id, chatJID)
	return err
}

// MessageRef is what the bridge needs to address an already stored message
// when reacting to it, quoting it, retrying its media or paging history.
type MessageRef struct {
	ID        string
	ChatJID   string
	Sender    string
	SenderJID string
	IsFromMe  bool
	Content   string
	Timestamp time.Time
	MediaType string
	Deleted   bool
}

// ResolveChat returns the first of chats in which message id is stored, or
// chats[0] if none has it. A direct chat can be stored under the contact's
// phone number and also under their LID (a message sent to a number, its
// reactions and votes arriving from a LID-addressed device); the caller
// passes both forms of the same chat.
func (s *Store) ResolveChat(ctx context.Context, id string, chats ...string) string {
	for _, chat := range chats {
		var found int
		if s.db.QueryRowContext(ctx, "SELECT 1 FROM messages WHERE id = ? AND chat_jid = ?", id, chat).Scan(&found) == nil {
			return chat
		}
	}
	if len(chats) == 0 {
		return ""
	}
	return chats[0]
}

// MessageExists reports whether a message is stored.
func (s *Store) MessageExists(ctx context.Context, id, chatJID string) bool {
	var found int
	return s.db.QueryRowContext(ctx, "SELECT 1 FROM messages WHERE id = ? AND chat_jid = ?", id, chatJID).Scan(&found) == nil
}

// Ref looks a message up by id. chatJID may be empty, in which case
// the id alone is used (ids are unique in practice).
func (s *Store) Ref(ctx context.Context, id, chatJID string) (*MessageRef, error) {
	query := `SELECT id, chat_jid, COALESCE(sender, ''), COALESCE(sender_jid, ''), is_from_me, COALESCE(content, ''), timestamp,
			COALESCE(media_type, ''), deleted_at IS NOT NULL
		FROM messages WHERE id = ?`
	args := []any{id}
	if chatJID != "" {
		query += " AND chat_jid = ?"
		args = append(args, chatJID)
	}
	query += " LIMIT 1"
	var ref MessageRef
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&ref.ID, &ref.ChatJID, &ref.Sender, &ref.SenderJID, &ref.IsFromMe, &ref.Content, &ref.Timestamp,
		&ref.MediaType, &ref.Deleted,
	)
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

// OldestRef returns the oldest stored message of a chat, the anchor
// for an on-demand history request.
func (s *Store) OldestRef(ctx context.Context, chatJID string) (*MessageRef, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM messages WHERE chat_jid = ? ORDER BY timestamp ASC LIMIT 1", chatJID,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.Ref(ctx, id, chatJID)
}

// NewestRef returns the newest stored message of a chat.
func (s *Store) NewestRef(ctx context.Context, chatJID string) (*MessageRef, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		"SELECT id FROM messages WHERE chat_jid = ? ORDER BY timestamp DESC LIMIT 1", chatJID,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.Ref(ctx, id, chatJID)
}

// RecentIncoming returns the newest messages of a chat that others sent,
// newest first: what "mark this chat as read" acknowledges.
func (s *Store) RecentIncoming(ctx context.Context, chatJID string, limit int) ([]*MessageRef, error) {
	ids, err := s.recentIncomingIDs(ctx, chatJID, limit)
	if err != nil {
		return nil, err
	}
	refs := make([]*MessageRef, 0, len(ids))
	for _, id := range ids {
		ref, err := s.Ref(ctx, id, chatJID)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// recentIncomingIDs is the query part of RecentIncoming; its rows are closed
// before the refs are looked up.
func (s *Store) recentIncomingIDs(ctx context.Context, chatJID string, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT id FROM messages WHERE chat_jid = ? AND NOT is_from_me ORDER BY timestamp DESC LIMIT ?", chatJID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Media is the media part of a stored message.
type Media struct {
	content.MediaInfo
	IsFromMe  bool
	SenderJID string
	Sender    string
}

// Media returns the media fields of a message.
func (s *Store) Media(ctx context.Context, id, chatJID string) (*Media, error) {
	var m Media
	var fileLength sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT COALESCE(media_type, ''), COALESCE(filename, ''), COALESCE(url, ''), COALESCE(direct_path, ''),
			COALESCE(mimetype, ''), media_key, file_sha256, file_enc_sha256, file_length,
			is_from_me, COALESCE(sender_jid, ''), COALESCE(sender, '')
		FROM messages WHERE id = ? AND chat_jid = ?`, id, chatJID,
	).Scan(&m.Type, &m.Filename, &m.URL, &m.DirectPath, &m.Mimetype, &m.MediaKey, &m.FileSHA256,
		&m.FileEncSHA256, &fileLength, &m.IsFromMe, &m.SenderJID, &m.Sender)
	if err != nil {
		return nil, err
	}
	if fileLength.Valid {
		m.FileLength = uint64(fileLength.Int64) //nolint:gosec // file_length is written from a uint64
	}
	return &m, nil
}

// UpdateDirectPath stores a refreshed CDN path obtained through a media retry.
func (s *Store) UpdateDirectPath(ctx context.Context, id, chatJID, directPath string) error {
	_, err := s.db.ExecContext(ctx,
		"UPDATE messages SET direct_path = ? WHERE id = ? AND chat_jid = ?",
		directPath, id, chatJID,
	)
	return err
}
