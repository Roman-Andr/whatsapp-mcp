package store

import (
	"context"
	"errors"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
)

// ExportMessages are a chat's messages in a time range, oldest first,
// with their details and every sender's name.
func (d *DB) ExportMessages(ctx context.Context, chatJID, after, before string) ([]*Message, error) {
	db, err := d.Open(ctx)
	if err != nil {
		return nil, err
	}
	s, err := d.readScope(ctx, db)
	if err != nil {
		return nil, err
	}
	if !Readable(chatJID, s) {
		return nil, errors.New(HiddenMessage(chatJID))
	}
	where, params := []string{"messages.chat_jid = ?"}, []any{chatJID}
	if after != "" {
		t, err := pyfmt.FilterTime(after, "after")
		if err != nil {
			return nil, err
		}
		where = append(where, "julianday(messages.timestamp) > julianday(?)")
		params = append(params, t)
	}
	if before != "" {
		t, err := pyfmt.FilterTime(before, "before")
		if err != nil {
			return nil, err
		}
		where = append(where, "julianday(messages.timestamp) < julianday(?)")
		params = append(params, t)
	}
	sel, err := messageSelect(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := queryRows(ctx, db, sel+" WHERE "+strings.Join(where, " AND ")+" ORDER BY messages.timestamp ASC", params...)
	if err != nil {
		return nil, err
	}
	messages := rowsToMessages(rows)
	if err := d.AttachDetails(ctx, db, messages); err != nil {
		return nil, err
	}
	resolver := d.newResolver(ctx, db)
	for _, m := range messages {
		name := "Me"
		if !m.IsFromMe {
			if name, err = resolver.Name(ctx, m.Sender, Deref(m.SenderJID), Deref(m.SenderName)); err != nil {
				return nil, err
			}
		}
		m.SenderName = &name
	}
	return messages, nil
}
