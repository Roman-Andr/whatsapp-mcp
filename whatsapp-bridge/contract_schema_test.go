package main

// Store schema contract: the DDL of a fresh and of an upgraded messages.db,
// the connection settings and the file modes the bridge leaves behind.

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/config"
)

// upstreamSchema is the messages.db of the original upstream bridge, which
// live stores were upgraded from (the same as whatsapp-mcp's fixture).
const upstreamSchema = `
CREATE TABLE chats (jid TEXT PRIMARY KEY, name TEXT, last_message_time TIMESTAMP);
CREATE TABLE messages (id TEXT, chat_jid TEXT, sender TEXT, content TEXT, timestamp TIMESTAMP,
    is_from_me BOOLEAN, media_type TEXT, filename TEXT, url TEXT, media_key BLOB, file_sha256 BLOB,
    file_enc_sha256 BLOB, file_length INTEGER, PRIMARY KEY (id, chat_jid));
INSERT INTO chats (jid, name, last_message_time) VALUES ('120363000000000001@g.us', 'Depot group', '2024-12-01 10:00:00+03:00');
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
    VALUES ('U1', '120363000000000001@g.us', '79990000002', 'old text', '2024-12-01 10:00:00+03:00', 0, '', '', '', NULL, NULL, NULL, 0);
INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
    VALUES ('U2', '120363000000000001@g.us', '79990000001', 'old photo', '2024-12-01 10:01:00+03:00', 1, 'image', 'image_1.jpg', 'https://mmg.whatsapp.net/u2', X'0102', X'0304', X'0506', 2048);
`

// dumpSchema renders sqlite_master, every table's columns in physical
// order, and the rows of the given tables.
func dumpSchema(t *testing.T, db *sql.DB, rowsOf []string) string {
	t.Helper()
	var sb strings.Builder
	sb.WriteString("== sqlite_master\n")
	var tables []string
	eachRow(t, db, `SELECT type, name, tbl_name, COALESCE(sql, '') FROM sqlite_master ORDER BY type, name`, func(rows *sql.Rows) {
		var typ, name, tbl, ddl string
		if err := rows.Scan(&typ, &name, &tbl, &ddl); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sb, "%s %s (%s)\n%s\n\n", typ, name, tbl, ddl)
		if typ == "table" {
			tables = append(tables, name)
		}
	})
	for _, table := range tables {
		fmt.Fprintf(&sb, "== table_info %s\n", table)
		query := fmt.Sprintf("SELECT cid, name, type, \"notnull\", COALESCE(dflt_value, 'NULL'), pk FROM pragma_table_info(%s)", sqlQuote(table))
		eachRow(t, db, query, func(rows *sql.Rows) {
			var cid, notNull, pk int
			var name, typ, dflt string
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&sb, "%d %s %s notnull=%d default=%s pk=%d\n", cid, name, typ, notNull, dflt, pk)
		})
	}
	sb.WriteString(dumpTables(t, db, rowsOf))
	return sb.String()
}

func TestContractSchemaFresh(t *testing.T) {
	dir := t.TempDir()
	db, _ := openContractStore(t, dir)
	checkGolden(t, "schema/fresh.golden", normalise(dumpSchema(t, db, []string{"bridge_meta"}), dir))
}

func TestContractSchemaUpgraded(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "store"), 0700); err != nil {
		t.Fatal(err)
	}
	upstream, err := sql.Open("sqlite3", filepath.Join(dir, "store", "messages.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := upstream.Exec(upstreamSchema); err != nil {
		t.Fatal(err)
	}
	upstream.Close()

	tables := []string{"chats", "messages", "bridge_meta"}
	db, closeStore := openContractStore(t, dir)
	first := normalise(dumpSchema(t, db, tables), dir)
	closeStore()
	checkGolden(t, "schema/upgraded.golden", first)

	// Migrating is idempotent: a second start changes nothing.
	db, _ = openContractStore(t, dir)
	if second := normalise(dumpSchema(t, db, tables), dir); second != first {
		t.Errorf("second open changed the store:\n%s", lineDiff(first, second))
	}
}

// TestContractSchemaSQL writes the executable DDL of a fresh store, the
// file whatsapp-mcp builds its fixtures on (its
// testdata/golden/bridge-schema.sql must be byte-identical), and checks it
// runs on an empty database.
func TestContractSchemaSQL(t *testing.T) {
	dir := t.TempDir()
	db, _ := openContractStore(t, dir)
	var sb strings.Builder
	eachRow(t, db, `SELECT sql FROM sqlite_master
		WHERE sql IS NOT NULL AND NOT (type = 'table' AND name GLOB 'messages_fts_*')
		ORDER BY rowid`, func(rows *sql.Rows) {
		var ddl string
		if err := rows.Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		sb.WriteString(ddl + ";\n")
	})
	checkGolden(t, "schema/fresh.sql", sb.String())

	empty, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer empty.Close()
	if _, err := empty.Exec(sb.String()); err != nil {
		t.Fatalf("fresh.sql does not run on an empty database: %v", err)
	}
}

func TestContractDSN(t *testing.T) {
	cases := []struct {
		dsn                  string
		journal              string
		foreignKeys, timeout int
	}{
		{"file:store/whatsapp.db?_foreign_keys=on", "delete", 1, 5000},
		{"file:store/messages.db?_foreign_keys=on&_busy_timeout=5000&_journal_mode=WAL", "wal", 1, 5000},
	}
	t.Chdir(t.TempDir())
	if err := os.Mkdir("store", 0700); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		db, err := sql.Open("sqlite3", c.dsn)
		if err != nil {
			t.Fatal(err)
		}
		checkPragmas(t, c.dsn, db, c.journal, c.foreignKeys, c.timeout)
		db.Close()
	}
	// The store opens messages.db with these settings.
	db, _ := openContractStore(t, t.TempDir())
	checkPragmas(t, "message store", db, "wal", 1, 5000)
}

func checkPragmas(t *testing.T, name string, db *sql.DB, journal string, foreignKeys, timeout int) {
	t.Helper()
	var mode string
	var fk, busy int
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
		t.Fatal(err)
	}
	if mode != journal || fk != foreignKeys || busy != timeout {
		t.Errorf("%s: journal_mode=%s foreign_keys=%d busy_timeout=%d, want %s %d %d", name, mode, fk, busy, journal, foreignKeys, timeout)
	}
}

func TestContractFileModes(t *testing.T) {
	t.Setenv("WHATSAPP_BRIDGE_TOKEN", "")
	e := newContractEnv(t, contractOpts{linked: true})
	if _, err := config.Secret("", "store/api_token", nil); err != nil {
		t.Fatal(err)
	}
	e.setQR("2@modes")
	e.post("/api/send", `{}`) // writes the audit log
	var sb strings.Builder
	for _, p := range []string{"store", "store/messages.db", "store/messages.db-wal", "store/messages.db-shm", "store/audit.log", "store/api_token", "store/qr.png"} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sb, "%s %04o\n", p, st.Mode().Perm())
	}
	checkGolden(t, "schema/modes.golden", sb.String())
}
