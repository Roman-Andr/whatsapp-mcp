package store

import (
	"context"
	"database/sql"
	"strings"
)

// Names resolves display names for JIDs within one tool call.
//
// Order: the name the bridge stored with the message, the chat name, then
// the bridge's contact store (whatsapp.db), following the LID -> phone
// number map because saved contact names live on the phone-number JID.
type Names struct {
	db    *sql.DB
	wa    *sql.DB
	cache map[string]string
}

// newResolver resolves with messages.db and, when it can be opened, the
// contact store; without it names come from the chats only.
func (d *DB) newResolver(ctx context.Context, db *sql.DB) *Names {
	r := &Names{db: db, cache: map[string]string{}}
	if wa, err := d.pool.Contacts(ctx, d.dir); err == nil {
		r.wa = wa
	}
	return r
}

// UserPart is the part of a JID before the "@".
func UserPart(jid string) string {
	user, _, _ := strings.Cut(jid, "@")
	return user
}

// contactName is the contact store's name for jid; a database error there
// counts as no name.
func (r *Names) contactName(ctx context.Context, jid string) string {
	if r.wa == nil {
		return ""
	}
	candidates := []string{jid}
	if strings.HasSuffix(jid, "@lid") {
		pn, err := alias(ctx, r.wa, jid)
		if err != nil {
			return ""
		}
		if pn != "" {
			candidates = append(candidates, pn)
		}
	}
	for _, c := range candidates {
		row, err := queryRow(ctx, r.wa, `SELECT full_name, first_name, push_name, business_name
			FROM whatsmeow_contacts WHERE their_jid = ?`, c)
		if err != nil {
			return ""
		}
		if row != nil {
			for _, col := range []string{"full_name", "first_name", "push_name", "business_name"} {
				if v := row.str(col); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

// Name is the display name for a sender: the stored name if any, else
// what the chats and the contact store know, else the bare number.
func (r *Names) Name(ctx context.Context, sender, senderJID, storedName string) (string, error) {
	if storedName != "" {
		return storedName, nil
	}
	jid := senderJID
	if jid == "" {
		jid = sender
	}
	if jid == "" {
		return "Unknown", nil
	}
	if cached, ok := r.cache[jid]; ok {
		return cached, nil
	}
	result := ""
	full := []string{jid}
	if !strings.Contains(jid, "@") {
		full = []string{jid + "@lid", jid + "@s.whatsapp.net"}
	}
	for _, candidate := range full {
		row, err := queryRow(ctx, r.db, "SELECT name FROM chats WHERE jid = ?", candidate)
		if err != nil {
			return "", err
		}
		if row != nil && row.str("name") != "" && row.str("name") != UserPart(candidate) {
			result = row.str("name")
			break
		}
		if result = r.contactName(ctx, candidate); result != "" {
			break
		}
	}
	if result == "" {
		result = UserPart(jid)
	}
	r.cache[jid] = result
	return result, nil
}

// Must is Name for callers that cannot fail halfway through rendering; a
// database error there leaves the bare number.
func (r *Names) Must(ctx context.Context, sender, senderJID, storedName string) string {
	n, err := r.Name(ctx, sender, senderJID, storedName)
	if err != nil {
		if senderJID != "" {
			return UserPart(senderJID)
		}
		return UserPart(sender)
	}
	return n
}
