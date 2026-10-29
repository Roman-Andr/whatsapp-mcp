package store

import (
	"testing"
	"time"
)

func TestSaveChatOnlyMovesForward(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	newer := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	s.SaveChat(ctx, "fresh@g.us", "Group", newer)
	s.SaveChat(ctx, "fresh@g.us", "", newer.Add(-24*time.Hour))

	var name string
	var last time.Time
	s.db.QueryRowContext(ctx, "SELECT name, last_message_time FROM chats WHERE jid = 'fresh@g.us'").Scan(&name, &last)
	if name != "Group" {
		t.Errorf("name replaced by empty one: %q", name)
	}
	if !last.Equal(newer) {
		t.Errorf("last_message_time moved back to %v", last)
	}
}

func TestMergeChatNamesTheMergedChat(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		from     string
		fromName string
		to       string
		want     string
	}{
		// A LID chat without a contact name was named after its bare number.
		{"bare number dropped", "555@lid", "555", "777@s.whatsapp.net", ""},
		{"contact name kept", "556@lid", "Anna", "778@s.whatsapp.net", "Anna"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s := openTestStore(t)
			ctx := t.Context()
			if err := s.SaveChat(ctx, c.from, c.fromName, time.Now()); err != nil {
				t.Fatal(err)
			}
			if err := s.MergeChat(ctx, c.from, c.to, ""); err != nil {
				t.Fatal(err)
			}
			if got := s.ChatName(ctx, c.to); got != c.want {
				t.Errorf("name %q, want %q", got, c.want)
			}
		})
	}
}

func TestMergeChatMovesRowsAndKeepsExistingCopies(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	const lid, pn = "248@lid", "799@s.whatsapp.net"
	now := time.Now()
	for _, jid := range []string{lid, pn} {
		if err := s.SaveChat(ctx, jid, "", now); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range []Message{
		{ID: "A", ChatJID: lid, Content: "from phone", Timestamp: now},
		{ID: "B", ChatJID: pn, Content: "from bridge", Timestamp: now},
		{ID: "B", ChatJID: lid, Content: "duplicate", Timestamp: now},
	} {
		mustSave(t, s, m)
	}
	if err := s.SaveReaction(ctx, Reaction{MessageID: "A", ChatJID: lid, SenderJID: "x@lid", Emoji: "👍", Timestamp: now}); err != nil {
		t.Fatal(err)
	}

	if err := s.MergeChat(ctx, lid, pn, ""); err != nil {
		t.Fatal(err)
	}

	t.Run("message moved", func(t *testing.T) {
		if ref, err := s.Ref(ctx, "A", pn); err != nil || ref == nil {
			t.Errorf("message A not moved: %v %v", ref, err)
		}
	})
	t.Run("existing copy kept", func(t *testing.T) {
		if ref, _ := s.Ref(ctx, "B", pn); ref == nil || ref.Content != "from bridge" {
			t.Errorf("existing copy of B replaced: %+v", ref)
		}
	})
	t.Run("nothing left under the LID", func(t *testing.T) {
		var left int
		s.db.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM messages WHERE chat_jid = ?) + (SELECT COUNT(*) FROM chats WHERE jid = ?) + (SELECT COUNT(*) FROM reactions WHERE chat_jid = ?)", lid, lid, lid).Scan(&left)
		if left != 0 {
			t.Errorf("%d rows left under %s", left, lid)
		}
		if lids, _ := s.ChatsOnServer(ctx, "lid"); len(lids) != 0 {
			t.Errorf("LID chats left: %v", lids)
		}
	})
	t.Run("reaction moved", func(t *testing.T) {
		var reactions int
		s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM reactions WHERE chat_jid = ?", pn).Scan(&reactions)
		if reactions != 1 {
			t.Errorf("reaction not moved: %d", reactions)
		}
	})
}
