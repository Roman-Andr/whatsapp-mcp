package main

// The store the golden scenarios run on: the parity fixture
// (parity/fixture.py, removed in 57465cd) on the schema today's bridge
// creates (testdata/golden/bridge-schema.sql), plus the per-scenario setup
// of parity/parity.sh.

import (
	"bytes"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

const goldenGroup = "120363431597928274@g.us"

// goldenStart is when the run started. The status row S1 sits two hours
// before it, so list_statuses (the last 24 h) shows it on any day; the
// normaliser maps its time back to <NOW-2h>.
var goldenStart = time.Now()

func goldenStatusTime() time.Time {
	return goldenStart.Add(-2 * time.Hour).Truncate(time.Second).UTC()
}

// goldenSchema is the executable DDL of a fresh bridge store.
func goldenSchema(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "golden", "bridge-schema.sql"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func goldenExec(t *testing.T, path string, statements ...string) {
	t.Helper()
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, s := range statements {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, s)
		}
	}
}

// goldenMessageRows are fixture.py's rows, with the columns named because
// the real schema's column order is not the Python test schema's.
const goldenMessageRows = `
INSERT INTO bridge_meta (key, value) VALUES ('fts_enabled', '1');
INSERT INTO chats (jid, name, last_message_time) VALUES ('` + goldenGroup + `', 'Snow operations system', '2026-10-27 17:01:00+03:00');
INSERT INTO chats (jid, name, last_message_time) VALUES ('32547396399312@lid', '32547396399312', '2026-10-27 11:00:00+03:00');
INSERT INTO chats (jid, name, last_message_time) VALUES ('7@s.whatsapp.net', 'Давид Бендер', '2026-10-27 10:00:00+03:00');
INSERT INTO chats (jid, name, last_message_time, deleted_at)
    VALUES ('gone@s.whatsapp.net', 'Gone', '2026-10-27 13:00:00+03:00', '2026-10-27 13:01:00+03:00');
INSERT INTO chats (jid, name, last_message_time) VALUES ('status@broadcast', 'Status updates', '2026-10-26 09:00:00+00:00');
UPDATE chats SET archived = 1, muted_until = -1, unread_count = 3 WHERE jid = '` + goldenGroup + `';
UPDATE chats SET pinned = 1, muted_until = 4102444800000 WHERE jid = '32547396399312@lid';
INSERT INTO labels (id, name, color, deleted) VALUES ('1', 'Paid', 2, 0);
INSERT INTO labels (id, name, color, deleted) VALUES ('2', 'Old', 3, 1);
INSERT INTO labels (id, name, color, deleted) VALUES ('10', 'Новые', 5, 0);
INSERT INTO chat_labels (chat_jid, label_id) VALUES ('` + goldenGroup + `', '1');
INSERT INTO chat_labels (chat_jid, label_id) VALUES ('` + goldenGroup + `', '10');
INSERT INTO chat_labels (chat_jid, label_id) VALUES ('7@s.whatsapp.net', '10');
INSERT INTO messages (id, chat_jid, sender, sender_jid, sender_name, content, timestamp, is_from_me)
    VALUES ('A1', '` + goldenGroup + `', '32547396399312', '32547396399312@lid', NULL,
            'The plow route on Main street is wrong', '2026-10-27 12:00:00.123456789+03:00', 0);
INSERT INTO messages (id, chat_jid, sender, sender_jid, sender_name, content, timestamp, is_from_me,
                      quoted_id, quoted_sender, quoted_text, edited_at, starred)
    VALUES ('A2', '` + goldenGroup + `', '79966764464', '79966764464@s.whatsapp.net', 'Roman',
            'Fixed it', '2026-10-27 12:01:00+03:00', 1,
            'A1', '32547396399312@lid', 'The plow route on Main street is wrong
second line of a long quoted text that goes on and on and on and on',
            '2026-10-27 12:02:00+03:00', 1);
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, deleted_at)
    VALUES ('A3', '` + goldenGroup + `', '28952508764308', 'see screenshot', '2026-10-27 12:05:00+03:00', 0,
            'image', 'image_1.jpg', '2026-10-27 12:06:00+03:00');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('D1', '32547396399312@lid', '32547396399312', 'direct hello', '2026-10-27 11:00:00+03:00', 0);
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('R1', '7@s.whatsapp.net', '7', 'Маршрут по Главной улице
вторая строка', '2026-10-27 10:00:00+03:00', 0);
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('M1', '` + goldenGroup + `', '79966764464', '@32547396399312 zoom? @123', '2026-10-27 14:00:00+03:00', 1);
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('P1', '` + goldenGroup + `', '79966764464', '[poll] Depot?: North / South / East', '2026-10-27 15:00:00+03:00', 1);
INSERT INTO polls (message_id, chat_jid, question, options, selectable_count)
    VALUES ('P1', '` + goldenGroup + `', 'Depot?', '["North", "South", "East"]', 1);
INSERT INTO poll_votes (message_id, chat_jid, voter_jid, voter_name, options, timestamp)
    VALUES ('P1', '` + goldenGroup + `', '32547396399312@lid', 'David Bender', '["North"]', '2026-10-27 15:01:00+03:00');
INSERT INTO poll_votes (message_id, chat_jid, voter_jid, voter_name, options, timestamp)
    VALUES ('P1', '` + goldenGroup + `', '79966764464@s.whatsapp.net', 'Roman', '["North"]', '2026-10-27 15:02:00+03:00');
INSERT INTO poll_votes (message_id, chat_jid, voter_jid, voter_name, options, timestamp)
    VALUES ('P1', '` + goldenGroup + `', '5@lid', NULL, '["South"]', '2026-10-27 15:03:00+03:00');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('E1', '` + goldenGroup + `', '79966764464', '[event] Depot meeting, 2026-11-01 18:00 +03:00', '2026-10-27 16:00:00+03:00', 1);
INSERT INTO event_responses (message_id, chat_jid, responder_jid, responder_name, response, extra_guests, timestamp)
    VALUES ('E1', '` + goldenGroup + `', '32547396399312@lid', 'David Bender', 'going', 1, '2026-10-27 16:01:00+03:00');
INSERT INTO event_responses (message_id, chat_jid, responder_jid, responder_name, response, extra_guests, timestamp)
    VALUES ('E1', '` + goldenGroup + `', '5@lid', NULL, 'maybe', 0, '2026-10-27 16:02:00+03:00');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, media_seconds, media_detail)
    VALUES ('V1', '` + goldenGroup + `', '5', '', '2026-10-27 17:00:00+03:00', 0, 'audio', 75, 'voice');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, media_width, media_height)
    VALUES ('I1', '` + goldenGroup + `', '5', '', '2026-10-27 17:01:00+03:00', 0, 'image', 1280, 720);
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, mimetype)
    VALUES ('F1', '7@s.whatsapp.net', '7', 'Счёт', '2026-10-27 09:30:00+00:00', 0, 'document', 'invoice.pdf', 'application/pdf');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('S1', 'status@broadcast', '5', 'New depot open', ?, 0);
INSERT INTO reactions (message_id, chat_jid, sender_jid, sender_name, emoji, timestamp)
    VALUES ('A2', '` + goldenGroup + `', '32547396399312@lid', 'David Bender', '👍', '2026-10-27 12:03:00+03:00');
INSERT INTO reactions (message_id, chat_jid, sender_jid, sender_name, emoji, timestamp)
    VALUES ('A2', '` + goldenGroup + `', '28952508764308@lid', NULL, '❤️', '2026-10-27 12:04:00+03:00');
`

// goldenContactRows is whatsapp.db as fixture.py wrote it: just the two
// whatsmeow tables the server reads.
const goldenContactRows = `
CREATE TABLE whatsmeow_contacts (our_jid TEXT, their_jid TEXT, first_name TEXT, full_name TEXT,
    push_name TEXT, business_name TEXT, redacted_phone TEXT);
CREATE TABLE whatsmeow_lid_map (lid TEXT PRIMARY KEY, pn TEXT UNIQUE NOT NULL);
INSERT INTO whatsmeow_lid_map VALUES ('32547396399312', '18046727669');
INSERT INTO whatsmeow_lid_map VALUES ('28952508764308', '375293714259');
INSERT INTO whatsmeow_contacts VALUES ('me', '18046727669@s.whatsapp.net', 'David', 'David Bender', NULL, NULL, NULL);
INSERT INTO whatsmeow_contacts VALUES ('me', '28952508764308@lid', NULL, NULL, 'Antonina', NULL, NULL);
INSERT INTO whatsmeow_contacts VALUES ('me', '4915112345678@s.whatsapp.net', 'Straße', NULL, NULL, 'Strasse GmbH', NULL);
`

// buildGoldenStore writes the fixture store into dir.
func buildGoldenStore(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "transcripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	messages := filepath.Join(dir, "messages.db")
	db, err := sql.Open("sqlite3", messages)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(goldenSchema(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(goldenMessageRows, goldenStatusTime().Format("2006-01-02 15:04:05-07:00")); err != nil {
		t.Fatal(err)
	}
	goldenExec(t, filepath.Join(dir, "whatsapp.db"), goldenContactRows)
	writeGoldenFile(t, filepath.Join(dir, "transcripts", goldenGroup+"_V1.txt"), "please move the depot pin\n")
	writeGoldenFile(t, filepath.Join(dir, "api_token"), "tok123\n")
}

// buildGoldenWorkStore is the second account of the accounts scenarios.
func buildGoldenWorkStore(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	goldenExec(t, filepath.Join(dir, "messages.db"), goldenSchema(t), `
INSERT INTO bridge_meta (key, value) VALUES ('fts_enabled', '1');
INSERT INTO chats (jid, name, last_message_time) VALUES ('w@g.us', 'Work crew', '2026-10-27 10:00:00+03:00');
INSERT INTO chats (jid, name, last_message_time) VALUES ('secret@g.us', 'Board', '2026-10-27 09:00:00+03:00');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('W1', 'w@g.us', '1', 'Shift starts at six', '2026-10-27 10:00:00+03:00', 0);
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
    VALUES ('S1', 'secret@g.us', '1', 'Quarterly numbers', '2026-10-27 09:00:00+03:00', 0);
INSERT INTO bridge_meta (key, value) VALUES ('read_allowlist', '["w@g.us"]');
`)
	writeGoldenFile(t, filepath.Join(dir, "api_token"), "worktoken\n")
}

// setupGoldenMediaRoots publishes the outbox as the only media root and
// puts files inside and outside it (parity.sh's setup_mediaroots).
func setupGoldenMediaRoots(t *testing.T, store, picture string) {
	t.Helper()
	pic, err := os.ReadFile(picture)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"outbox/.git", "private"} {
		if err := os.MkdirAll(filepath.Join(store, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range map[string]string{
		"private/photo.png":      string(pic),
		"outbox/logo.png":        string(pic),
		"outbox/report.pdf":      "%PDF-1.4",
		"outbox/voice.ogg":       "OggS",
		"outbox/sticker.webp":    "RIFF",
		"private/voice.mp3":      "x",
		"outbox/.git/config.mp3": "x",
	} {
		writeGoldenFile(t, filepath.Join(store, name), body)
	}
	if err := os.Symlink(filepath.Join(store, "private", "photo.png"), filepath.Join(store, "outbox", "escape.png")); err != nil {
		t.Fatal(err)
	}
	outbox := filepath.Join(store, "outbox")
	goldenExec(t, filepath.Join(store, "messages.db"),
		`INSERT OR REPLACE INTO bridge_meta (key, value) VALUES ('media_roots', '["`+outbox+`"]')`,
		`INSERT OR REPLACE INTO bridge_meta (key, value) VALUES ('media_outbox', '`+outbox+`')`)
}

// writeGoldenPNG writes the 64×32 red picture parity.sh made with PIL.
func writeGoldenPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64, 32))
	for y := range 32 {
		for x := range 64 {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	writeGoldenFile(t, path, b.String())
}

func writeGoldenFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
