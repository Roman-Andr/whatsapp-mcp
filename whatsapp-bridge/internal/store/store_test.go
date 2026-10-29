package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// openTestStore opens a fresh messages.db in a temporary directory, with the
// chats the tests' messages belong to (messages.chat_jid references chats;
// the bridge always stores the chat first).
func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, jid := range []string{"g@g.us", "120@g.us", "c@s.whatsapp.net"} {
		if err := s.SaveChat(t.Context(), jid, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func mustSave(t *testing.T, s *Store, m Message) {
	t.Helper()
	if err := s.SaveMessage(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func TestOpenMigratesUpstreamSchemaWithoutLosingRows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The schema and a row as the original upstream bridge wrote them.
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(dir, "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.ExecContext(t.Context(), `
		CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);
		CREATE TABLE messages (id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
			is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT, media_key BLOB, file_sha256 BLOB,
			file_enc_sha256 BLOB, file_length INTEGER, PRIMARY KEY (id, chat_jid));
		INSERT INTO chats VALUES ('g@g.us', 'Group', '2026-10-01 10:00:00+00:00');
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
			VALUES ('OLD1', 'g@g.us', '123', 'old text', '2026-10-01 10:00:00+00:00', 0);
	`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	t.Run("old row kept", func(t *testing.T) {
		ref, err := s.Ref(t.Context(), "OLD1", "g@g.us")
		if err != nil {
			t.Fatal(err)
		}
		if ref.Content != "old text" || ref.Sender != "123" {
			t.Errorf("old row changed: %+v", ref)
		}
	})
	t.Run("every added column exists", func(t *testing.T) {
		for _, col := range messageColumns {
			if _, err := s.db.ExecContext(t.Context(), "SELECT "+col.name+" FROM messages"); err != nil {
				t.Errorf("column %s missing: %v", col.name, err)
			}
		}
	})
	t.Run("existing rows indexed", func(t *testing.T) {
		if !s.FullTextSearch() {
			t.Skip("built without sqlite_fts5")
		}
		var n int
		s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'old'").Scan(&n)
		if n != 1 {
			t.Errorf("existing rows not indexed for full-text search: %d matches", n)
		}
	})
	t.Run("reopening is a no-op", func(t *testing.T) {
		reopened, err := Open(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}
		reopened.Close()
	})
}

func TestOpenMakesTheStorePrivate(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "store")
	// A store created by an older bridge was world-readable.
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, c := range []struct {
		name string
		path string
		want os.FileMode
	}{
		{"directory", dir, 0700},
		{"database", filepath.Join(dir, "messages.db"), 0600},
	} {
		t.Run(c.name, func(t *testing.T) {
			st, err := os.Stat(c.path)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != c.want {
				t.Errorf("%s mode %v, want %v", c.path, st.Mode().Perm(), c.want)
			}
		})
	}
	if st, err := os.Stat(filepath.Join(dir, "messages.db-wal")); err == nil && st.Mode().Perm() != 0600 {
		t.Errorf("WAL mode %v", st.Mode().Perm())
	}
}

func TestFullTextIndexFollowsEdits(t *testing.T) {
	t.Parallel()
	s := openTestStore(t)
	if !s.FullTextSearch() {
		t.Skip("built without sqlite_fts5")
	}
	mustSave(t, s, Message{ID: "F", ChatJID: "c@s.whatsapp.net", Content: "snow plow broke", Timestamp: time.Now()})
	s.EditMessage(t.Context(), "F", "c@s.whatsapp.net", "truck repaired", time.Now(), nil)

	for _, c := range []struct {
		query string
		want  int
	}{{"plow", 0}, {"repaired", 1}} {
		t.Run(c.query, func(t *testing.T) {
			var n int
			s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH ?", c.query).Scan(&n)
			if n != c.want {
				t.Errorf("%d matches, want %d", n, c.want)
			}
		})
	}
}
