package store

import (
	"testing"
	"time"
)

func TestReactionsReplaceAndRemove(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	t0 := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	r := Reaction{MessageID: "M1", ChatJID: "g@g.us", SenderJID: "111@lid", SenderName: "Alice", Emoji: "👍", Timestamp: t0}
	s.SaveReaction(ctx, r)

	r.Emoji, r.Timestamp = "❤️", t0.Add(time.Minute)
	s.SaveReaction(ctx, r)

	stale := r
	stale.Emoji, stale.Timestamp = "😮", t0.Add(-time.Hour)
	s.SaveReaction(ctx, stale) // an older reaction from history sync must not win

	t.Run("newest replaces, older loses", func(t *testing.T) {
		var emoji string
		var n int
		s.db.QueryRowContext(ctx, "SELECT emoji, COUNT(*) FROM reactions WHERE message_id = 'M1'").Scan(&emoji, &n)
		if n != 1 || emoji != "❤️" {
			t.Errorf("got %d reactions, emoji %q; want one ❤️", n, emoji)
		}
	})
	t.Run("empty emoji removes", func(t *testing.T) {
		r.Emoji = ""
		s.SaveReaction(ctx, r)
		var n int
		s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reactions").Scan(&n)
		if n != 0 {
			t.Errorf("reaction not removed, %d left", n)
		}
	})
}
