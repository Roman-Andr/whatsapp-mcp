package main

// REST contract: every route of the API, recorded as status, contract
// headers and exact body, against testdata/contract/api/*.golden.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types/events"

	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
)

// Route guard kinds as in PLAN.md Appendix A: R reaches others, S signal
// only, O own account, "-" unguarded.
type contractRoute struct {
	path, kind string
	post       bool // POST enforced (405 otherwise)
}

var contractRoutes = []contractRoute{
	{"/api/send", "R", true},
	{"/api/react", "R", true},
	{"/api/poll", "R", true},
	{"/api/location", "R", true},
	{"/api/edit", "R", true},
	{"/api/delete", "R", true},
	{"/api/read", "S", true},
	{"/api/typing", "S", true},
	{"/api/group", "-", true},
	{"/api/history", "-", true},
	{"/api/download", "-", true},
	{"/api/status", "-", false},
	{"/api/events", "-", false},
	{"/api/qr", "-", false},
	{"/api/logout", "O", true},
	{"/api/connection/reconnect", "-", true},
	{"/api/group/create", "R", true},
	{"/api/group/leave", "R", true},
	{"/api/group/participants", "R", true},
	{"/api/group/settings", "R", true},
	{"/api/group/join", "R", true},
	{"/api/chat", "O", true},
	{"/api/label/chat", "O", true},
	{"/api/label/edit", "O", true},
	{"/api/contact/info", "-", true},
	{"/api/blocklist", "-", false},
	{"/api/block", "O", true},
	{"/api/presence", "-", true},
	{"/api/channels", "-", false},
	{"/api/channel/messages", "-", true},
	{"/api/channel/follow", "O", true},
	{"/api/group/requests", "-", true},
	{"/api/group/requests/update", "R", true},
	{"/api/group/invite/reset", "R", true},
	{"/api/chat/disappearing", "R", true},
	{"/api/community/link", "R", true},
	{"/api/community/groups", "-", true},
	{"/api/poll/vote", "R", true},
	{"/api/contact", "R", true},
	{"/api/sticker", "R", true},
	{"/api/forward", "R", true},
	{"/api/star", "O", true},
}

func TestContractRouteTable(t *testing.T) {
	if len(contractRoutes) != 42 {
		t.Fatalf("%d routes, want 42", len(contractRoutes))
	}
	counts := map[string]int{}
	for _, r := range contractRoutes {
		counts[r.kind]++
	}
	if counts["R"] != 19 || counts["S"] != 2 || counts["O"] != 7 || counts["-"] != 14 {
		t.Fatalf("guard kinds %v", counts)
	}
	// Every route is registered: a valid token never gets the mux's 404.
	e := newContractEnv(t, contractOpts{linked: true})
	for _, r := range contractRoutes {
		if r.path == "/api/events" {
			continue
		}
		if got := e.do(http.MethodGet, r.path, nil, ""); got.status == http.StatusNotFound && strings.Contains(got.body, "404 page not found") {
			t.Errorf("%s is not registered", r.path)
		}
	}
}

// Fixture chats and messages.
const (
	aliceChat = "79990000002@s.whatsapp.net"
	bobChat   = "79990000003@s.whatsapp.net"
	groupChat = "120363000000000001@g.us"
)

// sqliteTime is how go-sqlite3 stores a time.Time.
func sqliteTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.999999999-07:00")
}

// seedREST stores the chats, messages, poll and label the REST cases act on.
// Messages that must look recent (edit window) are relative to now; the
// responses never show their timestamps.
func seedREST(e *contractEnv) {
	old := "2025-05-01 10:00:00+00:00"
	recent := sqliteTime(time.Now().Add(-time.Minute))
	hourAgo := sqliteTime(time.Now().Add(-time.Hour))
	e.exec(`INSERT INTO chats (jid, name, last_message_time) VALUES
		('79990000002@s.whatsapp.net', 'Alice Full', '2025-05-01 10:00:00+00:00'),
		('79990000003@s.whatsapp.net', 'Bob', '2025-05-01 10:00:00+00:00'),
		('120363000000000001@g.us', 'Depot group', '2025-05-01 10:00:00+00:00')`)
	insert := `INSERT INTO messages (id, chat_jid, sender, sender_jid, sender_name, content, timestamp, is_from_me,
		media_type, filename, mimetype, deleted_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	rows := [][]any{
		{"IN1", aliceChat, "79990000002", aliceChat, "Alice Full", "hello", old, false, "", "", "", nil},
		{"OUT1", aliceChat, "79990000001", "79990000001@s.whatsapp.net", "Me", "recent", recent, true, "", "", "", nil},
		{"OUTOLD", aliceChat, "79990000001", "79990000001@s.whatsapp.net", "Me", "old one", hourAgo, true, "", "", "", nil},
		{"OUTIMG", aliceChat, "79990000001", "79990000001@s.whatsapp.net", "Me", "", recent, true, "image", "a.jpg", "image/jpeg", nil},
		{"OUTPOLL", aliceChat, "79990000001", "79990000001@s.whatsapp.net", "Me", "[poll] Lunch? (choose one)\n- Soup\n- Salad", recent, true, "", "", "", nil},
		{"OUTDEL", aliceChat, "79990000001", "79990000001@s.whatsapp.net", "Me", "gone", recent, true, "", "", "", old},
		{"B1", bobChat, "79990000003", bobChat, "Bob", "hey", old, false, "", "", "", nil},
		{"NOSENDER", "79990000004@s.whatsapp.net", "", nil, "", "legacy row", old, false, "", "", "", nil},
		{"G1", groupChat, "99990000002", "99990000002@lid", "Alice Full", "group hi", old, false, "", "", "", nil},
		{"POLL1", groupChat, "99990000002", "99990000002@lid", "Alice Full", "[poll] Depot? (choose one)\n- North\n- South", old, false, "", "", "", nil},
	}
	e.exec(`INSERT INTO chats (jid, name, last_message_time) VALUES ('79990000004@s.whatsapp.net', 'Legacy', '2025-05-01 10:00:00+00:00'),
		('120363000000000003@g.us', 'Legacy group', '2025-05-01 10:00:00+00:00')`)
	rows = append(rows, []any{"GNOSENDER", "120363000000000003@g.us", "", nil, "", "who wrote this", old, false, "", "", "", nil})
	for _, r := range rows {
		e.exec(insert, r...)
	}
	e.exec(`INSERT INTO messages (id, chat_jid, sender, sender_jid, sender_name, content, timestamp, is_from_me,
		media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length, direct_path, mimetype)
		VALUES ('IMG1', '79990000002@s.whatsapp.net', '79990000002', '79990000002@s.whatsapp.net', 'Alice Full', 'photo',
		'2025-05-01 10:01:00+00:00', 0, 'image', 'IMG1.jpg', 'https://mmg.whatsapp.net/v/t62.7118-24/abc?ccb=11',
		X'0101010101010101010101010101010101010101010101010101010101010101',
		X'0202020202020202020202020202020202020202020202020202020202020202',
		X'0303030303030303030303030303030303030303030303030303030303030303',
		1234, '/v/t62.7118-24/abc?ccb=11', 'image/jpeg')`)
	e.exec(`INSERT INTO polls (message_id, chat_jid, question, options, selectable_count)
		VALUES ('POLL1', '120363000000000001@g.us', 'Depot?', '["North","South"]', 1)`)
	e.exec(`INSERT INTO labels (id, name, color, deleted) VALUES ('1', 'Customers', 2, 0)`)

	for _, f := range []string{"store/files/photo.jpg", "store/files/sticker.webp", "store/files/.hidden.jpg", "outside.jpg"} {
		if err := os.MkdirAll(filepath.Dir(f), 0700); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(f, []byte("not really an image"), 0600); err != nil {
			e.t.Fatal(err)
		}
	}
}

// expand puts the bridge directory into a request body.
func (e *contractEnv) expand(body string) string {
	return strings.ReplaceAll(body, "<DIR>", e.dir)
}

// transcript collects recorded exchanges for one golden.
type transcript struct {
	e  *contractEnv
	sb strings.Builder
}

func (tr *transcript) note(format string, args ...any) {
	fmt.Fprintf(&tr.sb, "#### "+format+"\n\n", args...)
}

func (tr *transcript) do(method, path string, headers map[string]string, body string) {
	tr.e.t.Helper()
	tr.sb.WriteString(tr.e.do(method, path, headers, tr.e.expand(body)).String())
}

func (tr *transcript) post(path, body string) {
	tr.e.t.Helper()
	tr.do(http.MethodPost, path, nil, body)
}

func (tr *transcript) check(name string) {
	tr.e.t.Helper()
	checkGolden(tr.e.t, "api/"+name+".golden", tr.e.normalise(tr.sb.String()))
}

// checkAudit compares the audit lines written since the last check.
func (e *contractEnv) checkAudit(name string) {
	e.t.Helper()
	lines := e.auditLines()
	text := strings.Join(lines, "\n")
	if text != "" {
		text += "\n"
	}
	checkGolden(e.t, "api/"+name+".audit.golden", text)
}

func TestContractAuth(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true})
	tr := &transcript{e: e}
	variants := []map[string]string{
		{},
		{"Authorization": "Bearer wrong-token"},
		{"Authorization": "Bearer " + contractToken},
		{"X-API-Token": contractToken},
		{"Authorization": contractToken},
		{"X-API-Token": "wrong-token", "Authorization": "Bearer " + contractToken},
		{"Authorization": "bearer " + contractToken},
	}
	for _, path := range []string{"/api/send", "/api/status", "/api/qr", "/api/nope"} {
		method, body := http.MethodGet, ""
		if path == "/api/send" {
			method, body = http.MethodPost, `{}`
		}
		for _, h := range variants {
			tr.do(method, path, h, body)
		}
	}
	tr.do(http.MethodGet, "/", nil, "")
	tr.do(http.MethodGet, "/api/send/extra", nil, "")
	tr.check("auth")
	e.checkAudit("auth")
}

func TestContractMethodAndDecode(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true})
	tr := &transcript{e: e}
	huge := `{"message":"` + strings.Repeat("a", 1<<20) + `"}`
	for _, r := range contractRoutes {
		if !r.post {
			continue
		}
		tr.do(http.MethodGet, r.path, nil, "")
		tr.do(http.MethodPut, r.path, nil, `{}`)
		if r.path == "/api/connection/reconnect" {
			continue // it reads no body
		}
		tr.post(r.path, `{`)
		tr.post(r.path, `[]`)
		tr.post(r.path, huge)
	}
	tr.check("decode")
	e.checkAudit("decode")
}

// validationCases reach every validation message that is decided before the
// bridge talks to WhatsApp.
var validationCases = []struct{ path, body string }{
	{"/api/send", `{}`},
	{"/api/send", `{"recipient":"","message":"hi"}`},
	{"/api/send", `{"recipient":"79990000002"}`},
	{"/api/send", `{"recipient":"not a number","message":"hi"}`},
	{"/api/send", `{"recipient":"+","message":"hi"}`},
	{"/api/send", `{"recipient":"bad@@jid","message":"hi"}`},
	{"/api/send", `{"recipient":"79990000002","media_path":"relative.jpg"}`},
	{"/api/send", `{"recipient":"79990000002","media_path":"<DIR>/store/files/missing.jpg"}`},
	{"/api/send", `{"recipient":"79990000002","media_path":"<DIR>/outside.jpg"}`},
	{"/api/send", `{"recipient":"79990000002","media_path":"<DIR>/store/files/.hidden.jpg"}`},
	{"/api/send", `{"recipient":"79990000002","message":"hi","reply_to_message_id":"NOPE"}`},
	{"/api/react", `{}`},
	{"/api/react", `{"chat_jid":"79990000002@s.whatsapp.net"}`},
	{"/api/react", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"NOPE","emoji":"👍"}`},
	{"/api/poll", `{}`},
	{"/api/poll", `{"recipient":"79990000002"}`},
	{"/api/poll", `{"recipient":"79990000002","question":"Q","options":["A"]}`},
	{"/api/poll", `{"recipient":"79990000002","question":"Q","options":["A"," "]}`},
	{"/api/poll", `{"recipient":"79990000002","question":"Q","options":["A","A"]}`},
	{"/api/poll", `{"recipient":"79990000002","question":"Q","options":["A","B"],"selectable_count":3}`},
	{"/api/poll", `{"recipient":"bad!","question":"Q","options":["A","B"]}`},
	{"/api/location", `{}`},
	{"/api/location", `{"recipient":"79990000002","latitude":91,"longitude":0}`},
	{"/api/location", `{"recipient":"79990000002","latitude":0,"longitude":-181}`},
	{"/api/location", `{"recipient":"bad!","latitude":1,"longitude":2}`},
	{"/api/edit", `{}`},
	{"/api/edit", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUT1"}`},
	{"/api/edit", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"NOPE","message":"x"}`},
	{"/api/edit", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1","message":"x"}`},
	{"/api/edit", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUTDEL","message":"x"}`},
	{"/api/edit", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUTIMG","message":"x"}`},
	{"/api/edit", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUTPOLL","message":"x"}`},
	{"/api/edit", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUTOLD","message":"x"}`},
	{"/api/delete", `{}`},
	{"/api/delete", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"NOPE"}`},
	{"/api/delete", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUTDEL"}`},
	{"/api/delete", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1"}`},
	{"/api/delete", `{"chat_jid":"120363000000000001@g.us","message_id":"G1"}`},
	{"/api/read", `{}`},
	{"/api/read", `{"chat_jid":"79990000009@s.whatsapp.net"}`},
	{"/api/read", `{"chat_jid":"79990000002@s.whatsapp.net","message_ids":["NOPE"]}`},
	{"/api/read", `{"chat_jid":"79990000002@s.whatsapp.net","message_ids":["OUT1"]}`},
	{"/api/read", `{"chat_jid":"79990000004@s.whatsapp.net"}`},
	{"/api/read", `{"chat_jid":"120363000000000003@g.us"}`},
	{"/api/typing", `{}`},
	{"/api/typing", `{"chat_jid":"79990000002","state":"dancing"}`},
	{"/api/typing", `{"chat_jid":"bad!"}`},
	{"/api/group", `{}`},
	{"/api/group", `{"chat_jid":"79990000002@s.whatsapp.net"}`},
	{"/api/history", `{}`},
	{"/api/download", `{}`},
	{"/api/download", `{"message_id":"IMG1"}`},
	{"/api/download", `{"chat_jid":"79990000002@s.whatsapp.net"}`},
	{"/api/download", `{"message_id":"NOPE","chat_jid":"79990000002@s.whatsapp.net"}`},
	{"/api/download", `{"message_id":"IN1","chat_jid":"79990000002@s.whatsapp.net"}`},
	{"/api/download", `{"message_id":"OUTIMG","chat_jid":"79990000002@s.whatsapp.net"}`},
	{"/api/logout", `{}`},
	{"/api/logout", `{"confirm":false}`},
	{"/api/group/create", `{}`},
	{"/api/group/create", `{"name":"` + strings.Repeat("x", 26) + `","participants":["79990000002"]}`},
	{"/api/group/create", `{"name":"Depot"}`},
	{"/api/group/create", `{"name":"Depot","participants":["bad!"]}`},
	{"/api/group/create", `{"name":"Depot","participants":["120363000000000001@g.us"]}`},
	{"/api/group/leave", `{}`},
	{"/api/group/participants", `{}`},
	{"/api/group/participants", `{"chat_jid":"120363000000000001@g.us","action":"kick","participants":["79990000003"]}`},
	{"/api/group/participants", `{"chat_jid":"120363000000000001@g.us","action":"add"}`},
	{"/api/group/settings", `{}`},
	{"/api/group/settings", `{"chat_jid":"120363000000000001@g.us"}`},
	{"/api/group/settings", `{"chat_jid":"120363000000000001@g.us","name":""}`},
	{"/api/group/join", `{}`},
	{"/api/group/join", `{"invite":"https://example.com/AbCdEf"}`},
	{"/api/chat", `{}`},
	{"/api/chat", `{"chat_jid":"bad!","action":"archive"}`},
	{"/api/chat", `{"chat_jid":"79990000002","action":"explode"}`},
	{"/api/chat", `{"chat_jid":"79990000002","action":"mute","mute_hours":-1}`},
	{"/api/label/chat", `{}`},
	{"/api/label/chat", `{"chat_jid":"bad!","label":"Customers"}`},
	{"/api/label/chat", `{"chat_jid":"79990000002"}`},
	{"/api/label/chat", `{"chat_jid":"79990000002","label":"Nope"}`},
	{"/api/label/edit", `{}`},
	{"/api/label/edit", `{"name":"VIP","color":25}`},
	{"/api/label/edit", `{"deleted":true}`},
	{"/api/contact/info", `{}`},
	{"/api/block", `{}`},
	{"/api/block", `{"jid":"bad!"}`},
	{"/api/block", `{"jid":"120363000000000001@g.us","blocked":true}`},
	{"/api/presence", `{"jid":"79990000002"}`},
	{"/api/channel/messages", `{}`},
	{"/api/channel/follow", `{}`},
	{"/api/group/requests", `{}`},
	{"/api/group/requests/update", `{}`},
	{"/api/group/requests/update", `{"chat_jid":"120363000000000001@g.us","action":"maybe"}`},
	{"/api/group/requests/update", `{"chat_jid":"120363000000000001@g.us","action":"approve"}`},
	{"/api/group/invite/reset", `{}`},
	{"/api/chat/disappearing", `{}`},
	{"/api/chat/disappearing", `{"chat_jid":"79990000002","timer":"1y"}`},
	{"/api/chat/disappearing", `{"chat_jid":"bad!","timer":"24h"}`},
	{"/api/community/link", `{}`},
	{"/api/community/link", `{"community_jid":"120363000000000002@g.us"}`},
	{"/api/community/groups", `{}`},
	{"/api/poll/vote", `{}`},
	{"/api/poll/vote", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1","options":["North"]}`},
	{"/api/poll/vote", `{"chat_jid":"120363000000000001@g.us","message_id":"POLL1","options":["East"]}`},
	{"/api/contact", `{}`},
	{"/api/contact", `{"recipient":"79990000002"}`},
	{"/api/contact", `{"recipient":"79990000002","phone":"+7 999"}`},
	{"/api/contact", `{"recipient":"79990000002","vcard":"hello"}`},
	{"/api/contact", `{"recipient":"bad!","name":"Bob","phone":"79990000003"}`},
	{"/api/sticker", `{}`},
	{"/api/sticker", `{"recipient":"79990000002"}`},
	{"/api/sticker", `{"recipient":"79990000002","media_path":"<DIR>/outside.jpg"}`},
	{"/api/forward", `{}`},
	{"/api/forward", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"NOPE","recipient":"79990000003"}`},
	{"/api/forward", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUTDEL","recipient":"79990000003"}`},
	{"/api/forward", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1","recipient":"bad!"}`},
	{"/api/star", `{}`},
	{"/api/star", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"NOPE"}`},
}

func TestContractValidation(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true})
	seedREST(e)
	tr := &transcript{e: e}
	for _, c := range validationCases {
		tr.post(c.path, c.body)
	}
	tr.check("validation")
	e.checkAudit("validation")
}

func TestContractReadOnly(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true, readOnly: true, rate: 10, burst: 3})
	seedREST(e)
	tr := &transcript{e: e}
	for _, r := range contractRoutes {
		if r.kind == "-" {
			continue
		}
		tr.post(r.path, `{}`)
	}
	tr.note("unguarded routes still answer in read-only mode")
	tr.post("/api/connection/reconnect", ``)
	tr.post("/api/history", `{"chat_jid":"79990000002@s.whatsapp.net"}`)
	tr.do(http.MethodGet, "/api/status", nil, "")
	tr.check("readonly")
	e.checkAudit("readonly")
}

func TestContractRateLimit(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true, rate: 10, burst: 3})
	seedREST(e)
	tr := &transcript{e: e}
	send := `{"recipient":"79990000002","message":"hi"}`
	tr.note("burst of 3, then refused")
	for range 4 {
		tr.post("/api/send", send)
	}
	tr.note("validation failures use tokens too")
	tr.post("/api/react", `{}`)
	tr.note("signal-only and own-account routes are not limited")
	tr.post("/api/read", `{"chat_jid":"79990000002@s.whatsapp.net"}`)
	tr.post("/api/typing", `{"chat_jid":"79990000002"}`)
	tr.post("/api/chat", `{"chat_jid":"79990000002","action":"pin"}`)
	tr.post("/api/star", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1"}`)
	tr.note("6 s later: passes although Retry-After said 7")
	e.clock.advance(6 * time.Second)
	tr.post("/api/send", send)
	tr.note("7 s later: one more token, then refused again")
	e.clock.advance(7 * time.Second)
	tr.post("/api/send", send)
	tr.post("/api/group/create", `{"name":"Depot","participants":["79990000002"]}`)
	tr.note("a minute later: tokens for every route that reaches others")
	e.clock.advance(time.Minute)
	for _, r := range contractRoutes {
		if r.kind == "R" && r.path != "/api/send" {
			tr.post(r.path, `{}`)
			e.clock.advance(6 * time.Second)
		}
	}
	tr.check("ratelimit")
	e.checkAudit("ratelimit")
}

// sendAllowCases address chats outside the send allowlist (only Alice is
// on it), plus Alice by her LID.
var sendAllowCases = []struct{ path, body string }{
	{"/api/send", `{"recipient":"79990000003","message":"hi"}`},
	{"/api/send", `{"recipient":"120363000000000001@g.us","message":"hi"}`},
	{"/api/send", `{"recipient":"99990000002@lid","message":"hi via LID"}`},
	{"/api/send", `{"recipient":"79990000002","message":"hi"}`},
	{"/api/send", `{"recipient":"79990000003","media_path":"<DIR>/outside.jpg"}`},
	{"/api/react", `{"chat_jid":"120363000000000001@g.us","message_id":"G1","emoji":"👍"}`},
	{"/api/react", `{"chat_jid":"79990000003@s.whatsapp.net","message_id":"B1","emoji":"👍"}`},
	{"/api/poll", `{"recipient":"79990000003","question":"Q","options":["A","B"]}`},
	{"/api/location", `{"recipient":"79990000003","latitude":1,"longitude":2}`},
	{"/api/edit", `{"chat_jid":"79990000003@s.whatsapp.net","message_id":"B1","message":"x"}`},
	{"/api/delete", `{"chat_jid":"79990000003@s.whatsapp.net","message_id":"B1"}`},
	{"/api/read", `{"chat_jid":"79990000003@s.whatsapp.net"}`},
	{"/api/typing", `{"chat_jid":"79990000003"}`},
	{"/api/typing", `{"chat_jid":"99990000002@lid"}`},
	{"/api/group/create", `{"name":"Depot","participants":["79990000002","79990000003"]}`},
	{"/api/group/leave", `{"chat_jid":"120363000000000001@g.us"}`},
	{"/api/group/participants", `{"chat_jid":"120363000000000001@g.us","action":"add","participants":["79990000002"]}`},
	{"/api/group/settings", `{"chat_jid":"120363000000000001@g.us","name":"New"}`},
	{"/api/group/join", `{"invite":"AbCdEf123"}`},
	{"/api/chat", `{"chat_jid":"79990000003","action":"archive"}`},
	{"/api/label/chat", `{"chat_jid":"79990000003","label":"Customers","labeled":true}`},
	{"/api/label/edit", `{"name":"VIP"}`},
	{"/api/block", `{"jid":"79990000003","blocked":true}`},
	{"/api/channel/follow", `{"channel":"120363000000000099@newsletter","follow":true}`},
	{"/api/group/requests/update", `{"chat_jid":"120363000000000001@g.us","action":"approve","participants":["79990000003"]}`},
	{"/api/group/invite/reset", `{"chat_jid":"120363000000000001@g.us"}`},
	{"/api/chat/disappearing", `{"chat_jid":"79990000003","timer":"7d"}`},
	{"/api/community/link", `{"community_jid":"120363000000000002@g.us","group_jid":"120363000000000001@g.us"}`},
	{"/api/poll/vote", `{"chat_jid":"120363000000000001@g.us","message_id":"POLL1","options":["North"]}`},
	{"/api/contact", `{"recipient":"79990000003","name":"Bob","phone":"79990000003"}`},
	{"/api/sticker", `{"recipient":"79990000003","media_path":"<DIR>/store/files/sticker.webp"}`},
	{"/api/forward", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1","recipient":"79990000003"}`},
	{"/api/star", `{"chat_jid":"79990000003@s.whatsapp.net","message_id":"B1","starred":true}`},
}

func TestContractSendAllowlist(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true, sendAllow: []string{"79990000002"}})
	seedREST(e)
	tr := &transcript{e: e}
	for _, c := range sendAllowCases {
		tr.post(c.path, c.body)
	}
	tr.check("send-allowlist")
	e.checkAudit("send-allowlist")
}

func TestContractReadAllowlist(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true, presence: true, readAllow: []string{"79990000002"}})
	seedREST(e)
	tr := &transcript{e: e}
	tr.note("refused: chats outside the read allowlist")
	tr.post("/api/group", `{"chat_jid":"120363000000000001@g.us"}`)
	tr.post("/api/history", `{"chat_jid":"120363000000000001@g.us"}`)
	tr.post("/api/history", `{"chat_jid":"bad@@jid"}`)
	tr.post("/api/download", `{"message_id":"G1","chat_jid":"120363000000000001@g.us"}`)
	tr.post("/api/contact/info", `{"jid":"79990000003"}`)
	tr.post("/api/presence", `{"jid":"79990000003"}`)
	tr.post("/api/channel/messages", `{"channel_jid":"120363000000000099@newsletter"}`)
	tr.post("/api/group/requests", `{"chat_jid":"120363000000000001@g.us"}`)
	tr.post("/api/community/groups", `{"community_jid":"120363000000000002@g.us"}`)
	tr.post("/api/forward", `{"chat_jid":"79990000003@s.whatsapp.net","message_id":"B1","recipient":"79990000002"}`)
	tr.note("allowed: Alice by phone number and by LID")
	tr.post("/api/history", `{"chat_jid":"79990000002@s.whatsapp.net"}`)
	tr.post("/api/download", `{"message_id":"IN1","chat_jid":"79990000002@s.whatsapp.net"}`)
	tr.post("/api/contact/info", `{"jid":"99990000002@lid"}`)
	tr.post("/api/presence", `{"jid":"99990000002@lid"}`)
	tr.post("/api/presence", `{}`)
	tr.post("/api/presence", `{"jid":"120363000000000001@g.us"}`)
	tr.do(http.MethodGet, "/api/blocklist", nil, "")
	tr.do(http.MethodGet, "/api/channels", nil, "")
	tr.do(http.MethodGet, "/api/status", nil, "")
	tr.check("read-allowlist")
	e.checkAudit("read-allowlist")
}

// notConnectedCases pass validation; the unconnected client ends each.
var notConnectedCases = []struct{ method, path, body string }{
	{"POST", "/api/send", `{"recipient":"79990000002","message":"hi"}`},
	{"POST", "/api/send", `{"recipient":"79990000002","message":"caption","media_path":"<DIR>/store/files/photo.jpg"}`},
	{"POST", "/api/react", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1","emoji":"👍"}`},
	{"POST", "/api/poll", `{"recipient":"79990000002","question":"Depot?","options":["North","South"]}`},
	{"POST", "/api/location", `{"recipient":"79990000002","latitude":55.75,"longitude":37.62,"name":"Depot","address":"Main st"}`},
	{"POST", "/api/edit", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUT1","message":"fixed"}`},
	{"POST", "/api/delete", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"OUT1"}`},
	{"POST", "/api/read", `{"chat_jid":"79990000002@s.whatsapp.net"}`},
	{"POST", "/api/typing", `{"chat_jid":"79990000002","state":"recording"}`},
	{"POST", "/api/group", `{"chat_jid":"120363000000000001@g.us"}`},
	{"POST", "/api/history", `{"chat_jid":"79990000002@s.whatsapp.net","count":10}`},
	{"POST", "/api/download", `{"message_id":"IMG1","chat_jid":"79990000002@s.whatsapp.net"}`},
	{"GET", "/api/status", ``},
	{"GET", "/api/qr", ``},
	{"POST", "/api/logout", `{"confirm":true}`},
	{"POST", "/api/connection/reconnect", ``},
	{"POST", "/api/group/create", `{"name":"Depot","participants":["79990000002"]}`},
	{"POST", "/api/group/create", `{"name":"Depot community","community":true}`},
	{"POST", "/api/group/leave", `{"chat_jid":"120363000000000001@g.us"}`},
	{"POST", "/api/group/participants", `{"chat_jid":"120363000000000001@g.us","action":"promote","participants":["79990000003"]}`},
	{"POST", "/api/group/settings", `{"chat_jid":"120363000000000001@g.us","name":"New","announce_only":true}`},
	{"POST", "/api/group/join", `{"invite":"https://chat.whatsapp.com/AbCdEf123"}`},
	{"POST", "/api/chat", `{"chat_jid":"79990000002","action":"archive"}`},
	{"POST", "/api/label/chat", `{"chat_jid":"79990000002","label":"customers","labeled":true}`},
	{"POST", "/api/label/edit", `{"name":"VIP","color":3}`},
	{"POST", "/api/contact/info", `{"jid":"79990000002","picture":true}`},
	{"GET", "/api/blocklist", ``},
	{"POST", "/api/block", `{"jid":"79990000003","blocked":true}`},
	{"POST", "/api/presence", `{"jid":"79990000002"}`},
	{"GET", "/api/channels", ``},
	{"POST", "/api/channel/messages", `{"channel_jid":"120363000000000099@newsletter","count":5}`},
	{"POST", "/api/channel/follow", `{"channel":"120363000000000099@newsletter","follow":true}`},
	{"POST", "/api/group/requests", `{"chat_jid":"120363000000000001@g.us"}`},
	{"POST", "/api/group/requests/update", `{"chat_jid":"120363000000000001@g.us","action":"reject","participants":["79990000003"]}`},
	{"POST", "/api/group/invite/reset", `{"chat_jid":"120363000000000001@g.us"}`},
	{"POST", "/api/chat/disappearing", `{"chat_jid":"79990000002","timer":"24h"}`},
	{"POST", "/api/community/link", `{"community_jid":"120363000000000002@g.us","group_jid":"120363000000000001@g.us"}`},
	{"POST", "/api/community/groups", `{"community_jid":"120363000000000002@g.us"}`},
	{"POST", "/api/poll/vote", `{"chat_jid":"120363000000000001@g.us","message_id":"POLL1","options":["North"]}`},
	{"POST", "/api/contact", `{"recipient":"79990000002","name":"Bob","phone":"+7 999 000-00-03"}`},
	{"POST", "/api/sticker", `{"recipient":"79990000002","media_path":"<DIR>/store/files/sticker.webp"}`},
	{"POST", "/api/forward", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1","recipient":"79990000003"}`},
	{"POST", "/api/star", `{"chat_jid":"79990000002@s.whatsapp.net","message_id":"IN1","starred":true}`},
}

func TestContractNotConnected(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true, presence: true})
	seedREST(e)
	before := e.dumpDB()
	tr := &transcript{e: e}
	for _, c := range notConnectedCases {
		tr.do(c.method, c.path, nil, c.body)
	}
	tr.check("not-connected")
	e.checkAudit("not-connected")
	// Nothing reached WhatsApp, so nothing was stored.
	if after := e.dumpDB(); after != before {
		t.Errorf("the store changed:\n%s", lineDiff(before, after))
	}
}

func TestContractStatusAndPairing(t *testing.T) {
	var sb strings.Builder
	section := func(e *contractEnv, title string, steps func(tr *transcript)) {
		tr := &transcript{e: e}
		tr.note("%s", title)
		steps(tr)
		sb.WriteString(e.normalise(tr.sb.String()))
	}

	unlinked := newContractEnv(t, contractOpts{})
	section(unlinked, "unlinked, starting", func(tr *transcript) {
		tr.do(http.MethodGet, "/api/status", nil, "")
		tr.do(http.MethodGet, "/api/qr", nil, "")
		tr.do(http.MethodGet, "/api/qr?format=png", nil, "")
		tr.post("/api/logout", `{"confirm":true}`)
		tr.post("/api/connection/reconnect", ``)
	})
	section(unlinked, "unlinked, QR code shown", func(tr *transcript) {
		unlinked.setSession(SessionQRRequired, reasonQRRequired, true)
		unlinked.setQR("2@contract-qr-code,AbCdEf==,GhIjKl==,MnOpQr==")
		tr.do(http.MethodGet, "/api/status", nil, "")
		tr.do(http.MethodGet, "/api/qr", nil, "")
		tr.do(http.MethodGet, "/api/qr?format=png", nil, "")
		tr.do(http.MethodPost, "/api/qr?format=png", nil, "")
	})
	section(unlinked, "unlinked, pairing code", func(tr *transcript) {
		unlinked.setPairingCode("ABCD-EFGH", nil)
		tr.do(http.MethodGet, "/api/qr", nil, "")
		unlinked.setPairingCode("", errors.New("phone number rejected"))
		tr.do(http.MethodGet, "/api/qr", nil, "")
	})
	section(unlinked, "unlinked, QR image missing", func(tr *transcript) {
		if err := os.Remove("store/qr.png"); err != nil {
			t.Fatal(err)
		}
		tr.do(http.MethodGet, "/api/qr?format=png", nil, "")
	})
	unlinked.checkAudit("status-unlinked")

	linked := newContractEnv(t, contractOpts{linked: true})
	linked.setQR("2@stale")
	section(linked, "linked", func(tr *transcript) {
		tr.do(http.MethodGet, "/api/status", nil, "")
		tr.do(http.MethodGet, "/api/qr", nil, "")
		tr.do(http.MethodGet, "/api/qr?format=png", nil, "")
	})
	section(linked, "session states, driven by whatsmeow events", func(tr *transcript) {
		for _, evt := range []any{
			&events.Connected{},
			&events.Disconnected{},
			&events.LoggedOut{Reason: events.ConnectFailureLoggedOut, OnConnect: true},
			&events.LoggedOut{},
			&events.StreamReplaced{},
			&events.QR{},
			&events.TemporaryBan{Code: events.TempBanSentToTooManyPeople, Expire: 2 * time.Hour},
			&events.ClientOutdated{},
			&events.ConnectFailure{Reason: events.ConnectFailureServiceUnavailable, Message: "try later"},
		} {
			linked.ingest(evt)
			tr.note("after %T", evt)
			tr.do(http.MethodGet, "/api/status", nil, "")
		}
	})
	section(linked, "reconnect", func(tr *transcript) {
		tr.post("/api/connection/reconnect", ``)
		tr.do(http.MethodGet, "/api/status", nil, "")
		linked.clock.advance(10 * time.Second)
		tr.post("/api/connection/reconnect", ``)
		linked.clock.advance(20 * time.Second)
		tr.post("/api/connection/reconnect", ``)
	})
	section(linked, "logout", func(tr *transcript) {
		tr.post("/api/logout", `{"confirm":true}`)
	})
	linked.checkAudit("status-linked")

	restricted := newContractEnv(t, contractOpts{linked: true, readOnly: true,
		sendAllow: []string{"79990000002", "120363000000000001@g.us"}, readAllow: []string{"99990000002@lid"}})
	section(restricted, "read-only with allowlists", func(tr *transcript) {
		tr.do(http.MethodGet, "/api/status", nil, "")
	})
	checkGolden(t, "api/status.golden", sb.String())
	checkGolden(t, "api/status.db.golden", restricted.dumpDB())
}

// TestContractEvents reads the SSE stream over a loopback server: the
// framing, the read-allowlist filter and the session events that always
// pass.
func TestContractEvents(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true, readAllow: []string{"79990000002"}})
	server := httptest.NewServer(e.handler)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Token", contractToken)
	// The test's own client: http.DefaultClient fails every request here.
	resp, err := (&http.Client{Transport: &http.Transport{}}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	fmt.Fprintf(&sb, "< %d\n", resp.StatusCode)
	for _, h := range append(recordedHeaders, "Connection") {
		for _, v := range resp.Header.Values(h) {
			fmt.Fprintf(&sb, "< %s: %s\n", h, v)
		}
	}
	sb.WriteString("\n")
	reader := bufio.NewReader(resp.Body)
	readFrame := func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("reading the stream: %v", err)
			}
			sb.WriteString(line)
			if line == "\n" {
				return
			}
		}
	}
	readFrame() // ": connected"
	at := time.Date(2025, 6, 1, 9, 30, 0, 0, time.UTC)
	e.publish(hub.Event{Type: "message", ChatJID: bobChat, ChatName: "Bob", MessageID: "HIDDEN", Content: "filtered out", Timestamp: at})
	e.publish(hub.Event{Type: "message", ChatJID: aliceChat, ChatName: "Alice Full", MessageID: "M1",
		SenderJID: aliceChat, SenderName: "Alice Full", Content: "line one\nline \"two\"", MediaType: "image", QuotedID: "Q1", Timestamp: at})
	e.publish(hub.Event{Type: "reaction", ChatJID: "99990000002@lid", MessageID: "M1", SenderJID: "99990000002@lid", Emoji: "👍", IsFromMe: true, Timestamp: at})
	e.publish(hub.Event{Type: "session", State: SessionLoggedOut, Content: reasonLoggedOut, Timestamp: at})
	readFrame()
	readFrame()
	readFrame()
	cancel()
	checkGolden(t, "api/events.golden", e.normalise(sb.String()))
}

// TestContractDownloadFailsOffline checks the guarantee the harness gives:
// a stored message with complete media keys ends in an error, not a
// download.
func TestContractDownloadFailsOffline(t *testing.T) {
	e := newContractEnv(t, contractOpts{linked: true})
	seedREST(e)
	r := e.post("/api/download", `{"message_id":"IMG1","chat_jid":"79990000002@s.whatsapp.net"}`)
	if r.status != http.StatusInternalServerError {
		t.Fatalf("download: %s", r)
	}
	if _, err := os.Stat(filepath.Join(mediaDir(aliceChat), "IMG1_IMG1.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file was written: %v", err)
	}
}
