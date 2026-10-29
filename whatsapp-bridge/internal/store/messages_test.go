package store

import (
	"database/sql"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
)

func TestSaveMessageUpsertKeepsLocalState(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	ts := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	msg := Message{
		ID: "M1", ChatJID: "g@g.us", Sender: "111", SenderJID: "111@lid", SenderName: "Alice",
		Content: "first", Timestamp: ts,
		Media:    content.MediaInfo{Type: "image", Filename: "image_a.jpg", DirectPath: "/v/old"},
		QuotedID: "Q1", Mentions: []string{"222@lid"},
	}
	mustSave(t, s, msg)
	if found, err := s.EditMessage(ctx, "M1", "g@g.us", "edited", ts.Add(time.Minute), nil); err != nil || !found {
		t.Fatalf("edit: found=%v err=%v", found, err)
	}
	s.UpdateDirectPath(ctx, "M1", "g@g.us", "/v/refreshed")

	// History sync delivers the original again, without sender details.
	again := msg
	again.Content, again.SenderJID, again.SenderName, again.QuotedID = "first", "", "", ""
	again.Media.Filename, again.Media.DirectPath = "image_b.jpg", "/v/other"
	mustSave(t, s, again)

	var got struct{ content, senderJID, senderName, filename, directPath, quotedID, mentions string }
	err := s.db.QueryRowContext(ctx, `SELECT content, sender_jid, sender_name, filename, direct_path, quoted_id, mentions
		FROM messages WHERE id = 'M1'`).Scan(&got.content, &got.senderJID, &got.senderName, &got.filename, &got.directPath, &got.quotedID, &got.mentions)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, got, want string
	}{
		{"edited text not overwritten by history sync", got.content, "edited"},
		{"sender JID kept", got.senderJID, "111@lid"},
		{"sender name kept", got.senderName, "Alice"},
		{"quoted id kept", got.quotedID, "Q1"},
		{"file name kept so downloads are not orphaned", got.filename, "image_a.jpg"},
		{"refreshed direct path kept", got.directPath, "/v/refreshed"},
		{"mentions stored as JSON", got.mentions, `["222@lid"]`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		})
	}
}

func TestSaveMessageFixesSenderOnResync(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ts := time.Now()
	// Rows written by the old history sync carry the group as sender.
	mustSave(t, s, Message{ID: "M1", ChatJID: "120@g.us", Sender: "120", Content: "hi", Timestamp: ts})
	mustSave(t, s, Message{ID: "M1", ChatJID: "120@g.us", Sender: "555", SenderJID: "555@lid", Content: "hi", Timestamp: ts})
	ref, err := s.Ref(t.Context(), "M1", "")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Sender != "555" || ref.SenderJID != "555@lid" {
		t.Errorf("sender not corrected: %+v", ref)
	}
}

func TestEmptyMessagesAreNotStored(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	s.SaveMessage(t.Context(), Message{ID: "E", ChatJID: "c@s.whatsapp.net", Timestamp: time.Now()})
	if _, err := s.Ref(t.Context(), "E", ""); err == nil {
		t.Error("message without text or media was stored")
	}
}

func TestMarkDeletedKeepsText(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	ctx := t.Context()
	mustSave(t, s, Message{ID: "D", ChatJID: "c@s.whatsapp.net", Content: "oops", Timestamp: time.Now()})
	for _, c := range []struct {
		name string
		id   string
		want bool
	}{
		{"stored message", "D", true},
		{"unknown message", "missing", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			if found, _ := s.MarkDeleted(ctx, c.id, "c@s.whatsapp.net", time.Now(), nil); found != c.want {
				t.Errorf("found = %v, want %v", found, c.want)
			}
		})
	}
	var text string
	var deleted sql.NullTime
	s.db.QueryRowContext(ctx, "SELECT content, deleted_at FROM messages WHERE id = 'D'").Scan(&text, &deleted)
	if text != "oops" || !deleted.Valid {
		t.Errorf("content=%q deleted=%v", text, deleted)
	}
}

func TestOldestRef(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mustSave(t, s, Message{ID: "B", ChatJID: "g@g.us", Content: "b", Timestamp: t0.Add(time.Hour)})
	mustSave(t, s, Message{ID: "A", ChatJID: "g@g.us", Content: "a", Timestamp: t0})
	t.Run("oldest message", func(t *testing.T) {
		ref, err := s.OldestRef(t.Context(), "g@g.us")
		if err != nil || ref.ID != "A" {
			t.Errorf("oldest = %+v, %v", ref, err)
		}
	})
	t.Run("chat without messages", func(t *testing.T) {
		if _, err := s.OldestRef(t.Context(), "none@g.us"); err == nil {
			t.Error("expected an error")
		}
	})
}

func TestRecentIncomingIsNewestFirst(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mustSave(t, s, Message{ID: "A", ChatJID: "g@g.us", Content: "a", Timestamp: t0})
	mustSave(t, s, Message{ID: "B", ChatJID: "g@g.us", Content: "b", Timestamp: t0.Add(time.Hour)})
	mustSave(t, s, Message{ID: "C", ChatJID: "g@g.us", Content: "mine", Timestamp: t0.Add(2 * time.Hour), IsFromMe: true})
	refs, err := s.RecentIncoming(t.Context(), "g@g.us", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 2 || refs[0].ID != "B" || refs[1].ID != "A" {
		t.Errorf("refs %+v", refs)
	}
}

func TestSaveMessageStoresMediaMetadata(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	voice := content.MediaInfo{Type: "audio", Seconds: 42, Detail: "voice"}
	mustSave(t, s, Message{ID: "V1", ChatJID: "g@g.us", Sender: "1", Timestamp: time.Now(), Media: voice})
	var seconds int
	var detail string
	s.db.QueryRowContext(t.Context(), "SELECT media_seconds, media_detail FROM messages WHERE id = 'V1'").Scan(&seconds, &detail)
	if seconds != 42 || detail != "voice" {
		t.Errorf("stored %d %q", seconds, detail)
	}
}
