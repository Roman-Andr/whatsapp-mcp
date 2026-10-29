package store

import (
	"context"
	"time"
)

// EventResponse is one row of event_responses.
type EventResponse struct {
	MessageID     string // the event
	ChatJID       string
	ResponderJID  string
	ResponderName string
	Response      string
	ExtraGuests   int
	Timestamp     time.Time
}

// SaveEventResponse records an answer; a newer one replaces it.
func (s *Store) SaveEventResponse(ctx context.Context, r EventResponse) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO event_responses (message_id, chat_jid, responder_jid, responder_name, response, extra_guests, timestamp)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id, chat_jid, responder_jid) DO UPDATE SET
			response = excluded.response, extra_guests = excluded.extra_guests, timestamp = excluded.timestamp,
			responder_name = COALESCE(NULLIF(excluded.responder_name, ''), event_responses.responder_name)
		WHERE excluded.timestamp >= event_responses.timestamp`,
		r.MessageID, r.ChatJID, r.ResponderJID, r.ResponderName, r.Response, r.ExtraGuests, r.Timestamp)
	return err
}
