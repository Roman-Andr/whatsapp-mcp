package events

import "time"

// Event is what SSE subscribers and the webhook receive. Its JSON form is
// part of the API.
type Event struct {
	Type       string    `json:"type"` // message, edit, delete, reaction, poll_vote, history, session, unavailable, group, call
	ChatJID    string    `json:"chat_jid,omitempty"`
	ChatName   string    `json:"chat_name,omitempty"`
	MessageID  string    `json:"message_id,omitempty"`
	SenderJID  string    `json:"sender_jid,omitempty"`
	SenderName string    `json:"sender_name,omitempty"`
	IsFromMe   bool      `json:"is_from_me,omitempty"`
	Content    string    `json:"content,omitempty"`
	MediaType  string    `json:"media_type,omitempty"`
	QuotedID   string    `json:"quoted_id,omitempty"`
	Emoji      string    `json:"emoji,omitempty"`
	State      string    `json:"state,omitempty"` // session: the new session state; call: ringing, answered, missed, ...
	Timestamp  time.Time `json:"timestamp"`
}
