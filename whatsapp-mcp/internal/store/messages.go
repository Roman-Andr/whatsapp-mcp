package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
)

// Reaction is one emoji reaction to a message.
type Reaction struct {
	Emoji      string
	SenderJID  string
	SenderName *string
}

// Votes is an ordered option -> names map (poll votes, event responses).
type Votes struct {
	Keys []string
	Who  map[string][]string
}

// NewVotes is an empty map.
func NewVotes() *Votes { return &Votes{Who: map[string][]string{}} }

func (v *Votes) add(key string, name *string) {
	if _, ok := v.Who[key]; !ok {
		v.Keys = append(v.Keys, key)
		v.Who[key] = []string{}
	}
	if name != nil {
		v.Who[key] = append(v.Who[key], *name)
	}
}

// Message is a stored message with its details.
type Message struct {
	Timestamp    *pyfmt.Time
	Sender       string
	Content      string
	IsFromMe     bool
	ChatJID      string
	ID           string
	ChatName     *string
	MediaType    string
	SenderJID    *string
	SenderName   *string
	Filename     *string
	QuotedID     *string
	QuotedSender *string
	QuotedText   *string
	Mentions     []any
	EditedAt     *pyfmt.Time
	DeletedAt    *pyfmt.Time
	Reactions    []Reaction
	MediaWidth   *int64
	MediaHeight  *int64
	MediaSeconds *int64
	MediaDetail  *string
	Starred      bool
	Transcript   string
	// For polls: every option with the names of those who chose it.
	PollVotes *Votes
	// For WhatsApp events: "going" / "not going" / "maybe" with who answered.
	EventResponses *Votes
}

// Deref is *s, "" for nil.
func Deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Optional columns: rows written by the original upstream bridge lack them.
var optionalMessageColumns = []string{
	"sender_jid", "sender_name", "filename", "quoted_id", "quoted_sender",
	"quoted_text", "mentions", "edited_at", "deleted_at",
	"media_width", "media_height", "media_seconds", "media_detail", "starred",
}

var timeColumns = map[string]bool{"edited_at": true, "deleted_at": true}

// messageSelect selects messages with every optional column. Time columns
// are read as text: the driver would otherwise turn them into time.Time and
// the context queries compare the stored text.
func messageSelect(ctx context.Context, db *sql.DB) (string, error) {
	present, err := columns(ctx, db, "messages")
	if err != nil {
		return "", err
	}
	parts := make([]string, len(optionalMessageColumns))
	for i, c := range optionalMessageColumns {
		switch {
		case !present[c]:
			parts[i] = "NULL AS " + c
		case timeColumns[c]:
			parts[i] = fmt.Sprintf("CAST(messages.%s AS TEXT) AS %s", c, c)
		default:
			parts[i] = fmt.Sprintf("messages.%s AS %s", c, c)
		}
	}
	return fmt.Sprintf(`
        SELECT CAST(messages.timestamp AS TEXT) AS timestamp, messages.sender, chats.name AS chat_name, messages.content,
               messages.is_from_me, messages.chat_jid, messages.id, messages.media_type, %s
        FROM messages
        JOIN chats ON messages.chat_jid = chats.jid
    `, strings.Join(parts, ", ")), nil
}

func nullInt(r row, key string) *int64 {
	if r[key] == nil {
		return nil
	}
	v := r.int(key)
	return &v
}

func rowToMessage(r row) *Message {
	var mentions []any
	if s := r.str("mentions"); s != "" {
		if json.Unmarshal([]byte(s), &mentions) != nil {
			mentions = nil
		}
	}
	return &Message{
		Timestamp:    pyfmt.ParseStoredTime(r["timestamp"]),
		Sender:       r.str("sender"),
		ChatName:     r.nullStr("chat_name"),
		Content:      r.str("content"),
		IsFromMe:     r.bool("is_from_me"),
		ChatJID:      r.str("chat_jid"),
		ID:           r.str("id"),
		MediaType:    r.str("media_type"),
		SenderJID:    r.nullStr("sender_jid"),
		SenderName:   r.nullStr("sender_name"),
		Filename:     r.nullStr("filename"),
		QuotedID:     r.nullStr("quoted_id"),
		QuotedSender: r.nullStr("quoted_sender"),
		QuotedText:   r.nullStr("quoted_text"),
		Mentions:     mentions,
		EditedAt:     pyfmt.ParseStoredTime(r["edited_at"]),
		DeletedAt:    pyfmt.ParseStoredTime(r["deleted_at"]),
		MediaWidth:   nullInt(r, "media_width"),
		MediaHeight:  nullInt(r, "media_height"),
		MediaSeconds: nullInt(r, "media_seconds"),
		MediaDetail:  r.nullStr("media_detail"),
		Starred:      r.bool("starred"),
	}
}

func rowsToMessages(rows []row) []*Message {
	out := make([]*Message, len(rows))
	for i, r := range rows {
		out[i] = rowToMessage(r)
	}
	return out
}

type msgKey struct{ id, chat string }

func indexMessages(messages []*Message) (map[msgKey]*Message, []any) {
	keys := map[msgKey]*Message{}
	seen := map[string]bool{}
	var ids []any
	for _, m := range messages {
		keys[msgKey{m.ID, m.ChatJID}] = m
		if !seen[m.ID] {
			seen[m.ID] = true
			ids = append(ids, m.ID)
		}
	}
	return keys, ids
}

// AttachDetails adds reactions, poll Votes, event responses and cached
// voice transcripts, one query per list.
func (d *DB) AttachDetails(ctx context.Context, db *sql.DB, messages []*Message) error {
	if len(messages) == 0 {
		return nil
	}
	keys, ids := indexMessages(messages)
	if ok, err := hasTable(ctx, db, "reactions"); err != nil {
		return err
	} else if ok {
		rows, err := queryRows(ctx, db, fmt.Sprintf(`SELECT message_id, chat_jid, sender_jid, sender_name, emoji FROM reactions
                WHERE message_id IN (%s) ORDER BY timestamp`, placeholders(len(ids))), ids...)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if t := keys[msgKey{r.str("message_id"), r.str("chat_jid")}]; t != nil {
				t.Reactions = append(t.Reactions, Reaction{r.str("emoji"), r.str("sender_jid"), r.nullStr("sender_name")})
			}
		}
	}
	if ok, err := hasTable(ctx, db, "polls"); err != nil {
		return err
	} else if ok {
		if err := attachPollVotes(ctx, db, messages, keys, ids); err != nil {
			return err
		}
	}
	if ok, err := hasTable(ctx, db, "event_responses"); err != nil {
		return err
	} else if ok {
		if err := attachEventResponses(ctx, db, messages); err != nil {
			return err
		}
	}
	for _, m := range messages {
		if m.MediaType == "audio" {
			m.Transcript = d.CachedTranscript(m.ID, m.ChatJID)
		}
	}
	return nil
}

func attachEventResponses(ctx context.Context, db *sql.DB, messages []*Message) error {
	var events []*Message
	for _, m := range messages {
		if strings.HasPrefix(m.Content, "[event] ") {
			events = append(events, m)
		}
	}
	if len(events) == 0 {
		return nil
	}
	keys, ids := indexMessages(events)
	rows, err := queryRows(ctx, db, fmt.Sprintf(`SELECT message_id, chat_jid, responder_jid, responder_name, response, extra_guests FROM event_responses
            WHERE message_id IN (%s) ORDER BY timestamp`, placeholders(len(ids))), ids...)
	if err != nil {
		return err
	}
	for _, r := range rows {
		t := keys[msgKey{r.str("message_id"), r.str("chat_jid")}]
		if t == nil {
			continue
		}
		who := r.str("responder_name")
		if who == "" {
			who = UserPart(r.str("responder_jid"))
		}
		if g := r.int("extra_guests"); g != 0 {
			who += fmt.Sprintf(" +%d", g)
		}
		if t.EventResponses == nil {
			t.EventResponses = NewVotes()
		}
		t.EventResponses.add(r.str("response"), &who)
	}
	return nil
}

func attachPollVotes(ctx context.Context, db *sql.DB, messages []*Message, keys map[msgKey]*Message, ids []any) error {
	rows, err := queryRows(ctx, db, fmt.Sprintf("SELECT message_id, chat_jid, options FROM polls WHERE message_id IN (%s)", placeholders(len(ids))), ids...)
	if err != nil {
		return err
	}
	for _, r := range rows {
		t := keys[msgKey{r.str("message_id"), r.str("chat_jid")}]
		if t == nil {
			continue
		}
		t.PollVotes = NewVotes()
		raw := r.str("options")
		if raw == "" {
			raw = "[]"
		}
		var options []string
		if json.Unmarshal([]byte(raw), &options) == nil {
			for _, o := range options {
				t.PollVotes.add(o, nil)
			}
		}
	}
	anyPoll := false
	for _, m := range messages {
		anyPoll = anyPoll || m.PollVotes != nil
	}
	if !anyPoll {
		return nil
	}
	if ok, err := hasTable(ctx, db, "poll_votes"); err != nil || !ok {
		return err
	}
	rows, err = queryRows(ctx, db, fmt.Sprintf(`SELECT message_id, chat_jid, voter_jid, voter_name, options FROM poll_votes
            WHERE message_id IN (%s) ORDER BY timestamp`, placeholders(len(ids))), ids...)
	if err != nil {
		return err
	}
	for _, r := range rows {
		t := keys[msgKey{r.str("message_id"), r.str("chat_jid")}]
		if t == nil || t.PollVotes == nil {
			continue
		}
		raw := r.str("options")
		if raw == "" {
			raw = "[]"
		}
		var chosen []string
		if json.Unmarshal([]byte(raw), &chosen) != nil {
			continue
		}
		who := r.str("voter_name")
		if who == "" {
			who = UserPart(r.str("voter_jid"))
		}
		for _, o := range chosen {
			t.PollVotes.add(o, &who)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Queries

// ListOpts are the criteria of ListMessages.
type ListOpts struct {
	After, Before, SenderPhoneNumber, ChatJID, Query string
	Limit, Page                                      int
	IncludeContext                                   bool
	ContextBefore, ContextAfter                      int
	StarredOnly                                      bool
}

// DefaultListOpts are list_messages' defaults.
func DefaultListOpts() ListOpts {
	return ListOpts{Limit: 20, IncludeContext: true, ContextBefore: 1, ContextAfter: 1}
}

// ListMessages returns messages matching the criteria, with context, and
// their details. Invalid dates are an ErrInvalidArgument.
func (d *DB) ListMessages(ctx context.Context, db *sql.DB, o ListOpts) ([]*Message, error) {
	s, err := d.readScope(ctx, db)
	if err != nil {
		return nil, err
	}
	where, params, err := listFilter(ctx, db, o, s)
	if err != nil {
		return nil, err
	}
	limit, offset := clamp(o.Limit, o.Page)
	query, err := messageSelect(ctx, db)
	if err != nil {
		return nil, err
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY messages.timestamp DESC LIMIT ? OFFSET ?"
	params = append(params, limit, offset)
	rows, err := queryRows(ctx, db, query, params...)
	if err != nil {
		return nil, err
	}
	result := rowsToMessages(rows)
	if o.IncludeContext && len(result) > 0 {
		if result, err = d.withContext(ctx, db, result, o, s); err != nil {
			return nil, err
		}
	}
	if err := d.AttachDetails(ctx, db, result); err != nil {
		return nil, err
	}
	return result, nil
}

// listFilter is the WHERE conditions of ListMessages and their parameters.
func listFilter(ctx context.Context, db *sql.DB, o ListOpts, s Chats) ([]string, []any, error) {
	cols, err := columns(ctx, db, "messages")
	if err != nil {
		return nil, nil, err
	}
	var where []string
	var params []any
	add := func(cond string, p ...any) {
		where = append(where, cond)
		params = append(params, p...)
	}
	if clause, cp := scopeClause("messages.chat_jid", s); clause != "" {
		add(clause, cp...)
	}
	// Timestamps are stored with the offset they were received in, so they
	// are compared as instants, not as strings.
	if o.After != "" {
		t, err := pyfmt.FilterTime(o.After, "after")
		if err != nil {
			return nil, nil, &kindError{ErrInvalidArgument, err.Error()}
		}
		add("julianday(messages.timestamp) > julianday(?)", t)
	}
	if o.Before != "" {
		t, err := pyfmt.FilterTime(o.Before, "before")
		if err != nil {
			return nil, nil, &kindError{ErrInvalidArgument, err.Error()}
		}
		add("julianday(messages.timestamp) < julianday(?)", t)
	}
	if o.SenderPhoneNumber != "" {
		sender := strings.TrimLeft(UserPart(o.SenderPhoneNumber), "+")
		if cols["sender_jid"] {
			add("(messages.sender = ? OR messages.sender_jid = ?)", sender, o.SenderPhoneNumber)
		} else {
			add("messages.sender = ?", sender)
		}
	}
	if o.ChatJID != "" {
		add("messages.chat_jid = ?", o.ChatJID)
	}
	if o.StarredOnly {
		if cols["starred"] {
			add("messages.starred = 1")
		} else {
			add("0")
		}
	}
	if o.Query != "" {
		fts := ""
		if ftsEnabled(ctx, db) {
			fts = ftsQuery(o.Query)
		}
		if fts != "" {
			// Word search through the index, plus substring search for
			// fragments inside words the index cannot match.
			add("(messages.rowid IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)"+
				" OR ULOWER(messages.content) LIKE ULOWER(?))", fts, "%"+o.Query+"%")
		} else {
			add("ULOWER(messages.content) LIKE ULOWER(?)", "%"+o.Query+"%")
		}
	}
	return where, params, nil
}

// withContext puts the messages around each match next to it, each
// message once.
func (d *DB) withContext(ctx context.Context, db *sql.DB, matches []*Message, o ListOpts, s Chats) ([]*Message, error) {
	seen := map[msgKey]bool{}
	var out []*Message
	for _, msg := range matches {
		around, err := d.MessageContext(ctx, db, msg.ID, msg.ChatJID, o.ContextBefore, o.ContextAfter, s)
		if err != nil {
			return nil, err
		}
		for _, m := range around {
			k := msgKey{m.ID, m.ChatJID}
			if !seen[k] {
				seen[k] = true
				out = append(out, m)
			}
		}
	}
	return out, nil
}

// MessageContext returns before + the message + after, oldest first.
// A missing message, or one in a hidden chat, is an ErrNotFound.
func (d *DB) MessageContext(ctx context.Context, db *sql.DB, messageID, chatJID string, before, after int, s Chats) ([]*Message, error) {
	sel, err := messageSelect(ctx, db)
	if err != nil {
		return nil, err
	}
	clause, cp := scopeClause("messages.chat_jid", s)
	extra := ""
	if clause != "" {
		extra = " AND " + clause
	}
	var target row
	if chatJID != "" {
		target, err = queryRow(ctx, db, sel+" WHERE messages.id = ? AND messages.chat_jid = ?"+extra, append([]any{messageID, chatJID}, cp...)...)
	} else {
		target, err = queryRow(ctx, db, sel+" WHERE messages.id = ?"+extra, append([]any{messageID}, cp...)...)
	}
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, &kindError{ErrNotFound, fmt.Sprintf("Message with ID %s not found", messageID)}
	}
	before = max(0, min(before, MaxLimit))
	after = max(0, min(after, MaxLimit))
	beforeRows, err := queryRows(ctx, db, sel+" WHERE messages.chat_jid = ? AND messages.timestamp < ? ORDER BY messages.timestamp DESC LIMIT ?",
		target["chat_jid"], target["timestamp"], before)
	if err != nil {
		return nil, err
	}
	afterRows, err := queryRows(ctx, db, sel+" WHERE messages.chat_jid = ? AND messages.timestamp > ? ORDER BY messages.timestamp ASC LIMIT ?",
		target["chat_jid"], target["timestamp"], after)
	if err != nil {
		return nil, err
	}
	var out []*Message
	for i := len(beforeRows) - 1; i >= 0; i-- {
		out = append(out, rowToMessage(beforeRows[i]))
	}
	out = append(out, rowToMessage(target))
	return append(out, rowsToMessages(afterRows)...), nil
}

// MediaInfo is a message's media type, MIME type and file name.
type MediaInfo struct {
	MediaType, Filename, Mimetype string
}

// GetMediaInfo returns nil for messages without media or in hidden chats.
// A database error is treated as no media.
func (d *DB) GetMediaInfo(ctx context.Context, messageID, chatJID string) *MediaInfo {
	db, err := d.Open(ctx)
	if err != nil {
		return nil
	}
	s, err := d.readScope(ctx, db)
	if err != nil || !Readable(chatJID, s) {
		return nil
	}
	cols, err := columns(ctx, db, "messages")
	if err != nil {
		return nil
	}
	mime := "NULL"
	if cols["mimetype"] {
		mime = "mimetype"
	}
	r, err := queryRow(ctx, db, fmt.Sprintf("SELECT media_type, filename, %s AS mimetype FROM messages WHERE id = ? AND chat_jid = ?", mime), messageID, chatJID)
	if err != nil || r == nil || r.str("media_type") == "" {
		return nil
	}
	return &MediaInfo{r.str("media_type"), r.str("filename"), r.str("mimetype")}
}

// LastInteraction is the most recent message involving a contact, with
// its details; nil when there is none.
func (d *DB) LastInteraction(ctx context.Context, db *sql.DB, jid string) (*Message, error) {
	cols, err := columns(ctx, db, "messages")
	if err != nil {
		return nil, err
	}
	user := UserPart(jid)
	cond := "(messages.sender = ? OR messages.chat_jid = ?"
	params := []any{user, jid}
	if cols["sender_jid"] {
		cond += " OR messages.sender_jid = ?)"
		params = append(params, jid)
	} else {
		cond += ")"
	}
	s, err := d.readScope(ctx, db)
	if err != nil {
		return nil, err
	}
	if clause, cp := scopeClause("messages.chat_jid", s); clause != "" {
		cond += " AND " + clause
		params = append(params, cp...)
	}
	sel, err := messageSelect(ctx, db)
	if err != nil {
		return nil, err
	}
	r, err := queryRow(ctx, db, sel+fmt.Sprintf(" WHERE %s ORDER BY messages.timestamp DESC LIMIT 1", cond), params...)
	if err != nil || r == nil {
		return nil, err
	}
	m := []*Message{rowToMessage(r)}
	if err := d.AttachDetails(ctx, db, m); err != nil {
		return nil, err
	}
	return m[0], nil
}
