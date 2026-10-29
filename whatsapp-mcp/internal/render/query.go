package render

// The message lists the tools and resources show, with the texts that
// stand in for them when the store cannot be read.
//
// A database error is shown to the client as "Database error: ..." here,
// where the result is text anyway; the chat lists of internal/store log it
// and return no chats, and the media lookups treat it as no media. The
// three behaviours are what clients have always seen and stay as they are.

import (
	"context"
	"errors"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
)

// databaseError is the text shown for a failed query, which is also logged.
func databaseError(d *store.DB, err error) string {
	d.Log().Error("database error", "err", err)
	return "Database error: " + err.Error()
}

// ListMessages returns messages matching the criteria, with context.
// Database and allowlist problems come back as the text; invalid dates are
// an error (store.ErrInvalidArgument).
func ListMessages(ctx context.Context, d *store.DB, o store.ListOpts) (string, error) {
	db, err := d.Open(ctx)
	if err != nil {
		// Not logged: the store is missing, which the text says.
		return "Database error: " + err.Error(), nil
	}
	resolver := d.NewResolver(ctx, db)
	messages, err := d.ListMessages(ctx, db, o)
	switch {
	case errors.Is(err, store.ErrInvalidArgument):
		return "", err
	case errors.Is(err, store.ErrReadScope):
		return err.Error(), nil
	case err != nil:
		return databaseError(d, err), nil
	}
	return FormatMessages(ctx, messages, true, resolver), nil
}

// MessageContext renders the messages around one, oldest first.
func MessageContext(ctx context.Context, d *store.DB, messageID string, before, after int, chatJID string) string {
	db, err := d.Open(ctx)
	if err != nil {
		return "Database error: " + err.Error()
	}
	resolver := d.NewResolver(ctx, db)
	s, err := d.ReadScope(ctx, db)
	var messages []*store.Message
	if err == nil {
		messages, err = d.MessageContext(ctx, db, messageID, chatJID, before, after, s)
	}
	if err == nil {
		err = d.AttachDetails(ctx, db, messages)
	}
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrReadScope):
		return err.Error()
	case err != nil:
		return databaseError(d, err)
	}
	return FormatMessages(ctx, messages, true, resolver)
}

// LastInteraction renders the most recent message involving a contact;
// nil when there is none. A database error is logged and also gives nil.
func LastInteraction(ctx context.Context, d *store.DB, jid string) *string {
	text := func(s string) *string { return &s }
	db, err := d.Open(ctx)
	if err != nil {
		return text("Database error: " + err.Error())
	}
	resolver := d.NewResolver(ctx, db)
	m, err := d.LastInteraction(ctx, db, jid)
	switch {
	case errors.Is(err, store.ErrReadScope):
		return text(err.Error())
	case err != nil:
		databaseError(d, err)
		return nil
	case m == nil:
		return nil
	}
	return text(FormatMessage(ctx, m, true, resolver))
}
