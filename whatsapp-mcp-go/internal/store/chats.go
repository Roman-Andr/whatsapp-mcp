package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
)

// Chat is a chat with its last message, state and labels.
type Chat struct {
	JID             string
	Name            *string
	LastMessageTime *pyfmt.Time
	LastMessage     *string
	LastSender      *string
	LastIsFromMe    *bool
	// Messages others sent since the chat was last read (on any device).
	UnreadCount *int64
	Archived    *bool
	Pinned      *bool
	// "forever", an ISO-8601 time, or nil when not muted.
	MutedUntil *string
	Labels     []string
}

// Contact is a person found in the chats or the contact store.
type Contact struct {
	PhoneNumber string
	Name        *string
	JID         string
}

// The last message of a chat, joined by rowid. Joining on
// last_message_time = timestamp (the original query) returned nothing when
// the two drifted apart and several rows when two messages shared a second.
const lastMessageJoin = `
    LEFT JOIN messages m ON m.rowid = (
        SELECT rowid FROM messages WHERE chat_jid = c.jid ORDER BY timestamp DESC LIMIT 1
    )
`

// Chat columns the bridge added after the upstream schema.
var optionalChatColumns = []string{"unread_count", "archived", "pinned", "muted_until", "deleted_at"}

// visibleChats hides chats deleted on a device ("" on older stores).
func visibleChats(ctx context.Context, db *sql.DB) (string, error) {
	cols, err := columns(ctx, db, "chats")
	if err != nil || !cols["deleted_at"] {
		return "", err
	}
	return "c.deleted_at IS NULL", nil
}

func mutedUntil(r row) *string {
	if r["muted_until"] == nil {
		return nil
	}
	v := r.int("muted_until")
	if v == 0 {
		return nil
	}
	s := "forever"
	if v < 0 {
		return &s
	}
	until := time.UnixMilli(v).UTC()
	if !until.After(time.Now()) {
		return nil
	}
	s = pyfmt.NewTime(until, true).ISOFormat("T")
	return &s
}

func attachLabels(ctx context.Context, db *sql.DB, chats []*Chat) error {
	if len(chats) == 0 {
		return nil
	}
	if ok, err := hasTable(ctx, db, "chat_labels"); err != nil || !ok {
		return err
	}
	byJID := map[string]*Chat{}
	var jids []any
	for _, c := range chats {
		if byJID[c.JID] == nil {
			jids = append(jids, c.JID)
		}
		byJID[c.JID] = c
	}
	rows, err := queryRows(ctx, db, fmt.Sprintf(`SELECT cl.chat_jid, COALESCE(l.name, cl.label_id) AS name FROM chat_labels cl
            LEFT JOIN labels l ON l.id = cl.label_id
            WHERE cl.chat_jid IN (%s) AND NOT COALESCE(l.deleted, 0) ORDER BY name`, placeholders(len(jids))), jids...)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if c := byJID[r.str("chat_jid")]; c != nil {
			c.Labels = append(c.Labels, r.str("name"))
		}
	}
	return nil
}

func chatSelect(ctx context.Context, db *sql.DB, includeLastMessage bool) (string, error) {
	present, err := columns(ctx, db, "chats")
	if err != nil {
		return "", err
	}
	parts := make([]string, len(optionalChatColumns))
	for i, col := range optionalChatColumns {
		switch {
		case !present[col]:
			parts[i] = "NULL AS " + col
		case col == "deleted_at":
			parts[i] = "CAST(c.deleted_at AS TEXT) AS deleted_at"
		default:
			parts[i] = fmt.Sprintf("c.%s AS %s", col, col)
		}
	}
	optional := strings.Join(parts, ", ")
	if includeLastMessage {
		return fmt.Sprintf(`
            SELECT c.jid, c.name, CAST(c.last_message_time AS TEXT) AS last_message_time, %s,
                   m.content AS last_message, m.sender AS last_sender, m.is_from_me AS last_is_from_me
            FROM chats c %s
        `, optional, lastMessageJoin), nil
	}
	// Without the join the message columns do not exist; select NULLs
	// instead of referencing them.
	return fmt.Sprintf(`
        SELECT c.jid, c.name, CAST(c.last_message_time AS TEXT) AS last_message_time, %s,
               NULL AS last_message, NULL AS last_sender, NULL AS last_is_from_me
        FROM chats c
    `, optional), nil
}

func nullBool(r row, key string) *bool {
	if r[key] == nil {
		return nil
	}
	b := r.bool(key)
	return &b
}

func rowToChat(ctx context.Context, r row, resolver *Names) (*Chat, error) {
	name := r.nullStr("name")
	jid := r.str("jid")
	// Chats addressed by LID were stored under their bare number; show the
	// contact's name when the contact store knows it.
	if resolver != nil && (Deref(name) == "" || *name == UserPart(jid)) && !strings.HasSuffix(jid, "@g.us") {
		n, err := resolver.Name(ctx, jid, "", "")
		if err != nil {
			return nil, err
		}
		name = &n
	}
	return &Chat{
		JID:             jid,
		Name:            name,
		LastMessageTime: pyfmt.ParseStoredTime(r["last_message_time"]),
		LastMessage:     r.nullStr("last_message"),
		LastSender:      r.nullStr("last_sender"),
		LastIsFromMe:    nullBool(r, "last_is_from_me"),
		UnreadCount:     nullInt(r, "unread_count"),
		Archived:        nullBool(r, "archived"),
		Pinned:          nullBool(r, "pinned"),
		MutedUntil:      mutedUntil(r),
		Labels:          []string{},
	}, nil
}

func rowsToChats(ctx context.Context, rows []row, resolver *Names) ([]*Chat, error) {
	out := []*Chat{}
	for _, r := range rows {
		c, err := rowToChat(ctx, r, resolver)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// chatQuery runs a chat-returning query.
//
// Allowlist and database errors are logged and give no chats (an empty
// list, or nil for one chat): the tools built on these return chats, not
// text, so the error cannot be shown. This is one of the three ways a
// database error surfaces, all kept as clients know them; render shows it
// as text for the message lists, and the media lookups treat it as no media.
func chatQuery[T any](ctx context.Context, d *DB, empty T, fn func(db *sql.DB, resolver *Names) (T, error)) T {
	db, err := d.Open(ctx)
	if err != nil {
		d.Log().Error("database error", "err", err)
		return empty
	}
	out, err := fn(db, d.newResolver(ctx, db))
	if err != nil {
		d.logQueryError(err)
		return empty
	}
	return out
}

// logQueryError logs a failed query: a misconfigured allowlist or a
// database error.
func (d *DB) logQueryError(err error) {
	if errors.Is(err, ErrReadScope) {
		d.Log().Error("read allowlist error", "err", err)
		return
	}
	d.Log().Error("database error", "err", err)
}

// ChatOpts are the criteria of ListChats.
type ChatOpts struct {
	Query              string
	Limit, Page        int
	IncludeLastMessage bool
	SortBy             string
	Label              string
}

// ListChats returns chats matching the criteria. Chats deleted on a device
// are left out.
func (d *DB) ListChats(ctx context.Context, o ChatOpts) []*Chat {
	return chatQuery(ctx, d, []*Chat{}, func(db *sql.DB, res *Names) ([]*Chat, error) {
		query, err := chatSelect(ctx, db, o.IncludeLastMessage)
		if err != nil {
			return nil, err
		}
		s, err := d.readScope(ctx, db)
		if err != nil {
			return nil, err
		}
		var where []string
		var params []any
		if clause, cp := scopeClause("c.jid", s); clause != "" {
			where = append(where, clause)
			params = append(params, cp...)
		}
		if o.Query != "" {
			where = append(where, "(ULOWER(c.name) LIKE ULOWER(?) OR c.jid LIKE ?)")
			params = append(params, "%"+o.Query+"%", "%"+o.Query+"%")
		}
		visible, err := visibleChats(ctx, db)
		if err != nil {
			return nil, err
		}
		if visible != "" {
			where = append(where, visible)
		}
		hasLabels, err := hasTable(ctx, db, "chat_labels")
		if err != nil {
			return nil, err
		}
		if o.Label != "" && hasLabels {
			where = append(where, `c.jid IN (SELECT cl.chat_jid FROM chat_labels cl LEFT JOIN labels l ON l.id = cl.label_id
                            WHERE cl.label_id = ? OR ULOWER(l.name) = ULOWER(?))`)
			params = append(params, o.Label, o.Label)
		} else if o.Label != "" {
			return []*Chat{}, nil
		}
		if len(where) > 0 {
			query += " WHERE " + strings.Join(where, " AND ")
		}
		if o.SortBy == "last_active" {
			query += " ORDER BY c.last_message_time DESC"
		} else {
			query += " ORDER BY c.name"
		}
		limit, offset := clamp(o.Limit, o.Page)
		query += " LIMIT ? OFFSET ?"
		params = append(params, limit, offset)
		rows, err := queryRows(ctx, db, query, params...)
		if err != nil {
			return nil, err
		}
		chats, err := rowsToChats(ctx, rows, res)
		if err != nil {
			return nil, err
		}
		return chats, attachLabels(ctx, db, chats)
	})
}

// ListUnreadChats returns chats with unread messages, most recently active first.
func (d *DB) ListUnreadChats(ctx context.Context, limit int) []*Chat {
	return chatQuery(ctx, d, []*Chat{}, func(db *sql.DB, res *Names) ([]*Chat, error) {
		cols, err := columns(ctx, db, "chats")
		if err != nil {
			return nil, err
		}
		if !cols["unread_count"] {
			return []*Chat{}, nil
		}
		s, err := d.readScope(ctx, db)
		if err != nil {
			return nil, err
		}
		clause, params := scopeClause("c.jid", s)
		limit, _ = clamp(limit, 0)
		visible, err := visibleChats(ctx, db)
		if err != nil {
			return nil, err
		}
		query, err := chatSelect(ctx, db, true)
		if err != nil {
			return nil, err
		}
		query += " WHERE c.unread_count > 0"
		if clause != "" {
			query += " AND " + clause
		}
		if visible != "" {
			query += " AND " + visible
		}
		query += " ORDER BY c.last_message_time DESC LIMIT ?"
		rows, err := queryRows(ctx, db, query, append(params, limit)...)
		if err != nil {
			return nil, err
		}
		chats, err := rowsToChats(ctx, rows, res)
		if err != nil {
			return nil, err
		}
		return chats, attachLabels(ctx, db, chats)
	})
}

// ListLabels returns the WhatsApp Business labels with the number of chats
// carrying each.
func (d *DB) ListLabels(ctx context.Context) []*pyfmt.OMap {
	return chatQuery(ctx, d, []*pyfmt.OMap{}, func(db *sql.DB, _ *Names) ([]*pyfmt.OMap, error) {
		if ok, err := hasTable(ctx, db, "labels"); err != nil || !ok {
			return []*pyfmt.OMap{}, err
		}
		s, err := d.readScope(ctx, db)
		if err != nil {
			return nil, err
		}
		clause, params := scopeClause("cl.chat_jid", s)
		if clause != "" {
			clause = "AND " + clause
		}
		rows, err := queryRows(ctx, db, fmt.Sprintf(`SELECT l.id, l.name, l.color,
                       (SELECT COUNT(*) FROM chat_labels cl WHERE cl.label_id = l.id %s) AS chats
                FROM labels l WHERE NOT l.deleted ORDER BY CAST(l.id AS INTEGER)`, clause), params...)
		if err != nil {
			return nil, err
		}
		out := []*pyfmt.OMap{}
		for _, r := range rows {
			out = append(out, pyfmt.NewOMap("id", r["id"], "name", r["name"], "color", r["color"], "chats", r["chats"]))
		}
		return out, nil
	})
}

// SearchContacts searches contacts by name or phone number, including
// saved contacts the bridge has never exchanged a message with.
//
// A database error is logged and the contacts found before it are kept; a
// misconfigured allowlist gives none.
func (d *DB) SearchContacts(ctx context.Context, query string, limit int) []*Contact {
	limit, _ = clamp(limit, 0)
	var order []string
	found := map[string]*Contact{}
	put := func(c *Contact) {
		if found[c.JID] == nil {
			order = append(order, c.JID)
		}
		found[c.JID] = c
	}
	pattern := "%" + query + "%"
	var s Chats
	err := func() error {
		db, err := d.Open(ctx)
		if err != nil {
			return err
		}
		if s, err = d.readScope(ctx, db); err != nil {
			return err
		}
		rows, err := queryRows(ctx, db, `SELECT DISTINCT jid, name FROM chats
                   WHERE (ULOWER(name) LIKE ULOWER(?) OR ULOWER(jid) LIKE ULOWER(?)) AND jid NOT LIKE '%@g.us'
                   ORDER BY name, jid LIMIT ?`, pattern, pattern, limit)
		if err != nil {
			return err
		}
		for _, r := range rows {
			put(&Contact{UserPart(r.str("jid")), r.nullStr("name"), r.str("jid")})
		}
		wa, err := d.pool.Contacts(ctx, d.dir)
		if err != nil || wa == nil {
			return err
		}
		rows, err = queryRows(ctx, wa, `SELECT their_jid, COALESCE(NULLIF(full_name, ''), NULLIF(first_name, ''),
                              NULLIF(push_name, ''), business_name) AS name
                       FROM whatsmeow_contacts
                       WHERE ULOWER(full_name) LIKE ULOWER(?) OR ULOWER(push_name) LIKE ULOWER(?)
                          OR ULOWER(first_name) LIKE ULOWER(?) OR their_jid LIKE ?
                       LIMIT ?`, pattern, pattern, pattern, pattern, limit)
		if err != nil {
			return err
		}
		for _, r := range rows {
			jid := r.str("their_jid")
			existing := found[jid]
			if existing == nil || Deref(existing.Name) == "" || *existing.Name == existing.PhoneNumber {
				put(&Contact{UserPart(jid), r.nullStr("name"), jid})
			}
		}
		return nil
	}()
	if err != nil {
		d.logQueryError(err)
		if errors.Is(err, ErrReadScope) {
			return []*Contact{}
		}
	}
	out := []*Contact{}
	for _, jid := range order {
		if Readable(jid, s) && len(out) < limit {
			out = append(out, found[jid])
		}
	}
	return out
}

// GetContactChats returns every chat involving the contact, each once.
func (d *DB) GetContactChats(ctx context.Context, jid string, limit, page int) []*Chat {
	return chatQuery(ctx, d, []*Chat{}, func(db *sql.DB, res *Names) ([]*Chat, error) {
		user := UserPart(jid)
		cols, err := columns(ctx, db, "messages")
		if err != nil {
			return nil, err
		}
		senderFilter, senderParams := "sender = ?", []any{user}
		if cols["sender_jid"] {
			senderFilter, senderParams = "sender = ? OR sender_jid = ?", []any{user, jid}
		}
		limit, offset := clamp(limit, page)
		s, err := d.readScope(ctx, db)
		if err != nil {
			return nil, err
		}
		clause, cp := scopeClause("c.jid", s)
		if clause != "" {
			clause = "AND " + clause
		}
		sel, err := chatSelect(ctx, db, true)
		if err != nil {
			return nil, err
		}
		query := sel + fmt.Sprintf(`
            WHERE (c.jid = ? OR c.jid IN (SELECT DISTINCT chat_jid FROM messages WHERE %s))
            %s
            ORDER BY c.last_message_time DESC LIMIT ? OFFSET ?
        `, senderFilter, clause)
		params := append([]any{jid}, senderParams...)
		params = append(append(params, cp...), limit, offset)
		rows, err := queryRows(ctx, db, query, params...)
		if err != nil {
			return nil, err
		}
		return rowsToChats(ctx, rows, res)
	})
}

// GetChat returns a chat's metadata, nil when missing or hidden.
func (d *DB) GetChat(ctx context.Context, chatJID string, includeLastMessage bool) *Chat {
	return chatQuery(ctx, d, (*Chat)(nil), func(db *sql.DB, res *Names) (*Chat, error) {
		s, err := d.readScope(ctx, db)
		if err != nil {
			return nil, err
		}
		if !Readable(chatJID, s) {
			return nil, nil
		}
		sel, err := chatSelect(ctx, db, includeLastMessage)
		if err != nil {
			return nil, err
		}
		r, err := queryRow(ctx, db, sel+" WHERE c.jid = ?", chatJID)
		if err != nil || r == nil {
			return nil, err
		}
		c, err := rowToChat(ctx, r, res)
		if err != nil {
			return nil, err
		}
		return c, attachLabels(ctx, db, []*Chat{c})
	})
}

// GetDirectChatByContact finds the direct chat with a phone number; chats
// addressed by LID are found through the bridge's LID map.
func (d *DB) GetDirectChatByContact(ctx context.Context, phone string) *Chat {
	number := strings.TrimLeft(UserPart(phone), "+")
	candidates := []string{number + "@s.whatsapp.net"}
	// The LID map is optional: without it only the phone-number chat counts.
	if wa, err := d.pool.Contacts(ctx, d.dir); err == nil && wa != nil {
		if lid, err := alias(ctx, wa, candidates[0]); err == nil && lid != "" {
			candidates = append(candidates, lid)
		}
	}
	return chatQuery(ctx, d, (*Chat)(nil), func(db *sql.DB, res *Names) (*Chat, error) {
		s, err := d.readScope(ctx, db)
		if err != nil {
			return nil, err
		}
		sel, err := chatSelect(ctx, db, true)
		if err != nil {
			return nil, err
		}
		for _, jid := range candidates {
			if !Readable(jid, s) {
				continue
			}
			r, err := queryRow(ctx, db, sel+" WHERE c.jid = ?", jid)
			if err != nil {
				return nil, err
			}
			if r != nil {
				return rowToChat(ctx, r, res)
			}
		}
		clause, cp := scopeClause("c.jid", s)
		query := sel + " WHERE c.jid LIKE ? AND c.jid NOT LIKE '%@g.us'"
		if clause != "" {
			query += " AND " + clause
		}
		r, err := queryRow(ctx, db, query+" LIMIT 1", append([]any{"%" + number + "%"}, cp...)...)
		if err != nil || r == nil {
			return nil, err
		}
		return rowToChat(ctx, r, res)
	})
}
