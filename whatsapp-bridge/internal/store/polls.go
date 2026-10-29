package store

import (
	"context"
	"encoding/json"
	"time"
)

// SavePoll records the question and options of a poll, needed to turn the
// option hashes in votes back into names.
func (s *Store) SavePoll(ctx context.Context, messageID, chatJID, question string, options []string, selectable int) error {
	encoded, err := json.Marshal(options)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO polls (message_id, chat_jid, question, options, selectable_count) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(message_id, chat_jid) DO UPDATE SET
			question = excluded.question, options = excluded.options, selectable_count = excluded.selectable_count`,
		messageID, chatJID, question, string(encoded), selectable,
	)
	return err
}

// PollOptions returns the option names of a stored poll.
func (s *Store) PollOptions(ctx context.Context, messageID, chatJID string) ([]string, error) {
	var encoded string
	err := s.db.QueryRowContext(ctx,
		"SELECT options FROM polls WHERE message_id = ? AND chat_jid = ?", messageID, chatJID,
	).Scan(&encoded)
	if err != nil {
		return nil, err
	}
	var options []string
	return options, json.Unmarshal([]byte(encoded), &options)
}

// PollVote is one row of poll_votes: a voter's current choice.
type PollVote struct {
	MessageID string // the poll
	ChatJID   string
	VoterJID  string
	VoterName string
	Options   []string
	Timestamp time.Time
}

// SavePollVote records a vote. A vote replaces the voter's earlier one, and
// an empty choice means the vote was withdrawn. Votes can arrive out of
// order (history sync), so an older vote never overwrites a newer one.
func (s *Store) SavePollVote(ctx context.Context, v PollVote) error {
	if len(v.Options) == 0 {
		_, err := s.db.ExecContext(ctx,
			"DELETE FROM poll_votes WHERE message_id = ? AND chat_jid = ? AND voter_jid = ? AND timestamp <= ?",
			v.MessageID, v.ChatJID, v.VoterJID, v.Timestamp,
		)
		return err
	}
	encoded, err := json.Marshal(v.Options)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO poll_votes (message_id, chat_jid, voter_jid, voter_name, options, timestamp)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(message_id, chat_jid, voter_jid) DO UPDATE SET
			options = excluded.options,
			timestamp = excluded.timestamp,
			voter_name = COALESCE(NULLIF(excluded.voter_name, ''), poll_votes.voter_name)
		WHERE excluded.timestamp >= poll_votes.timestamp`,
		v.MessageID, v.ChatJID, v.VoterJID, v.VoterName, string(encoded), v.Timestamp,
	)
	return err
}
