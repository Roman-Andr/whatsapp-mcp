package store_test

// Chat and contact queries against the fixture rows of internal/storetest.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/storetest"
)

const (
	group  = storetest.Group
	direct = "32547396399312@lid"
)

func newStore(t *testing.T) *storetest.Fixture {
	t.Helper()
	return storetest.New(t, storetest.Options{})
}

func chatOpts() store.ChatOpts {
	return store.ChatOpts{Limit: 20, IncludeLastMessage: true, SortBy: "last_active"}
}

func jids(chats []*store.Chat) []string {
	out := []string{}
	for _, c := range chats {
		out = append(out, c.JID)
	}
	return out
}

func TestListChats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(o *store.ChatOpts)
		want []string
	}{
		{"most recently active first", nil, []string{group, direct}},
		{"by name", func(o *store.ChatOpts) { o.SortBy = "name" }, []string{direct, group}},
		{"by name or JID", func(o *store.ChatOpts) { o.Query = "SNOW" }, []string{group}},
		{"one page", func(o *store.ChatOpts) { o.Limit, o.Page = 1, 1 }, []string{direct}},
		{"an unknown label", func(o *store.ChatOpts) { o.Label = "nope" }, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			o := chatOpts()
			if tc.edit != nil {
				tc.edit(&o)
			}
			if got := jids(newStore(t).DB().ListChats(t.Context(), o)); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestListChatsWithoutLastMessage(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	o := chatOpts()
	o.IncludeLastMessage = false
	chats := s.DB().ListChats(t.Context(), o)
	if chats[0].LastMessage != nil {
		t.Error("last message without the join")
	}
	if got := store.Deref(s.DB().ListChats(t.Context(), chatOpts())[0].LastMessage); got != "see screenshot" {
		t.Errorf("last message %q", got)
	}
}

func TestChatNamesComeFromTheContactStore(t *testing.T) {
	t.Parallel()
	c := newStore(t).DB().GetChat(t.Context(), direct, false)
	if got := store.Deref(c.Name); got != "David Bender" {
		t.Errorf("name %q", got)
	}
}

func TestGetContactChatsHasNoDuplicates(t *testing.T) {
	t.Parallel()
	got := jids(newStore(t).DB().GetContactChats(t.Context(), direct, 20, 0))
	slices.Sort(got)
	want := []string{group, direct}
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}

func TestGetDirectChatByContact(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, phone, want string
	}{
		{"through the LID map", "+18046727669", direct},
		{"by part of the JID", "3254739639", direct},
		{"unknown", "+999", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			chat := newStore(t).DB().GetDirectChatByContact(t.Context(), tc.phone)
			got := ""
			if chat != nil {
				got = chat.JID
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSearchContacts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, query string
		want        []store.Contact
	}{
		{"names in the contact store", "david", []store.Contact{
			{PhoneNumber: "18046727669", Name: new("David Bender"), JID: "18046727669@s.whatsapp.net"},
		}},
		{"push names", "antonina", []store.Contact{
			{PhoneNumber: "28952508764308", Name: new("Antonina"), JID: "28952508764308@lid"},
		}},
		{"a chat named by its number", "32547396399312", []store.Contact{
			{PhoneNumber: "32547396399312", Name: new("32547396399312"), JID: direct},
		}},
		{"nothing", "zzz", []store.Contact{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := []store.Contact{}
			for _, c := range newStore(t).DB().SearchContacts(t.Context(), tc.query, 50) {
				got = append(got, *c)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSearchContactsHonoursLimit(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	for i := range 80 {
		storetest.Exec(t, s.WhatsappDB(), "INSERT INTO whatsmeow_contacts VALUES ('me', ?, NULL, ?, NULL, NULL, NULL)",
			fmt.Sprintf("1000%03d@s.whatsapp.net", i), fmt.Sprintf("Driver %d", i))
	}
	for _, limit := range []int{50, 70, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			t.Parallel()
			if got := len(s.DB().SearchContacts(t.Context(), "Driver", limit)); got != limit {
				t.Errorf("got %d", got)
			}
		})
	}
}

func TestUnreadChatsAreListedWithCounts(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec("UPDATE chats SET unread_count = 3 WHERE jid = ?", group)
	chats := s.DB().ListUnreadChats(t.Context(), 20)
	if len(chats) != 1 || chats[0].JID != group || *chats[0].UnreadCount != 3 || store.Deref(chats[0].LastMessage) != "see screenshot" {
		t.Fatalf("got %v", jids(chats))
	}
	if c := s.DB().GetChat(t.Context(), direct, true); *c.UnreadCount != 0 {
		t.Errorf("unread %d", *c.UnreadCount)
	}
}

func TestChatStateAndLabelsAreShown(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec(`
		UPDATE chats SET archived = 1, muted_until = -1 WHERE jid = '` + group + `';
		UPDATE chats SET pinned = 1, muted_until = 4102444800000 WHERE jid = '` + direct + `';
		INSERT INTO chats (jid, name, last_message_time, deleted_at)
			VALUES ('gone@s.whatsapp.net', 'Gone', '2026-10-27 13:00:00+03:00', '2026-10-27 13:01:00+03:00');
		INSERT INTO chats (jid, name, last_message_time, muted_until)
			VALUES ('old@s.whatsapp.net', 'Old', '2026-10-27 10:00:00+03:00', 1000);
		INSERT INTO labels VALUES ('1', 'Paid', 2, 0);
		INSERT INTO labels VALUES ('2', 'Old', 3, 1);
		INSERT INTO chat_labels VALUES ('` + group + `', '1');`)
	chats := map[string]*store.Chat{}
	for _, c := range s.DB().ListChats(t.Context(), chatOpts()) {
		chats[c.JID] = c
	}
	if chats["gone@s.whatsapp.net"] != nil {
		t.Error("deleted chat listed")
	}
	g := chats[group]
	if !*g.Archived || *g.Pinned || store.Deref(g.MutedUntil) != "forever" || !reflect.DeepEqual(g.Labels, []string{"Paid"}) {
		t.Errorf("group %+v", g)
	}
	if d := chats[direct]; !*d.Pinned || store.Deref(d.MutedUntil) != "2100-01-01T00:00:00+00:00" {
		t.Errorf("direct %+v %s", d, store.Deref(d.MutedUntil))
	}
	if o := chats["old@s.whatsapp.net"]; o.MutedUntil != nil {
		t.Errorf("an expired mute shown: %s", *o.MutedUntil)
	}
	o := chatOpts()
	o.Label = "paid"
	if got := jids(s.DB().ListChats(t.Context(), o)); !reflect.DeepEqual(got, []string{group}) {
		t.Errorf("label filter %v", got)
	}
	if c := s.DB().GetChat(t.Context(), "gone@s.whatsapp.net", true); store.Deref(c.Name) != "Gone" {
		t.Errorf("deleted chat %+v", c)
	}
	if got := storetest.JSONOf(t, s.DB().ListLabels(t.Context())); got != storetest.JSONOf(t, `[{"id": "1", "name": "Paid", "color": 2, "chats": 1}]`) {
		t.Errorf("labels %s", got)
	}
}

func TestUpstreamStoreLacksTheNewColumns(t *testing.T) {
	t.Parallel()
	f := storetest.New(t, storetest.Options{Schema: storetest.UpstreamSchema, Empty: true})
	f.Exec(`INSERT INTO chats (jid, name, last_message_time) VALUES ('` + group + `', 'G', '2026-10-27 12:00:00+03:00');
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES ('U1', '` + group + `', '5', 'hi', '2026-10-27 12:00:00+03:00', 0)`)
	d := f.DB()
	if got := jids(d.ListChats(t.Context(), chatOpts())); !reflect.DeepEqual(got, []string{group}) {
		t.Errorf("chats %v", got)
	}
	if got := d.ListUnreadChats(t.Context(), 5); len(got) != 0 {
		t.Errorf("unread %v", jids(got))
	}
	if got := d.ListLabels(t.Context()); len(got) != 0 {
		t.Errorf("labels %v", got)
	}
	o := chatOpts()
	o.Label = "x"
	if got := d.ListChats(t.Context(), o); len(got) != 0 {
		t.Errorf("label filter %v", jids(got))
	}
	if got := jids(d.GetContactChats(t.Context(), "5@s.whatsapp.net", 5, 0)); !reflect.DeepEqual(got, []string{group}) {
		t.Errorf("contact chats %v", got)
	}
}

func TestConnectionsAreReusedAndSeeNewRows(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	first, err := s.Pool.Open(t.Context(), s.MessagesDB())
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Pool.Open(t.Context(), s.MessagesDB()); again != first {
		t.Error("connection not reused")
	}
	s.Exec("INSERT INTO chats (jid, name, last_message_time) VALUES ('new@g.us', 'New', '2026-10-28 10:00:00+03:00')")
	if !slices.Contains(jids(s.DB().ListChats(t.Context(), chatOpts())), "new@g.us") {
		t.Error("new row not seen")
	}
	// A replaced file (a fresh store) gets a fresh connection.
	if err := os.Rename(s.MessagesDB(), filepath.Join(s.Dir, "old.db")); err != nil {
		t.Fatal(err)
	}
	s.Exec(storetest.BridgeSchema)
	if again, _ := s.Pool.Open(t.Context(), s.MessagesDB()); again == first {
		t.Error("stale connection kept")
	}
	if got := s.DB().ListChats(t.Context(), chatOpts()); len(got) != 0 {
		t.Errorf("got %v", jids(got))
	}
}

func TestContactsIsNilWithoutAContactStore(t *testing.T) {
	t.Parallel()
	pool := store.NewPool(logging.Discard())
	t.Cleanup(pool.Close)
	if wa, err := pool.Contacts(t.Context(), t.TempDir()); wa != nil || err != nil {
		t.Errorf("got %v %v", wa, err)
	}
}

func TestChatQueryErrorsAreLoggedAndGiveNoChats(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(t *testing.T, f *storetest.Fixture)
		log   string
	}{
		{"no store", func(t *testing.T, f *storetest.Fixture) {
			t.Helper()
			f.Config.StoreDir = filepath.Join(t.TempDir(), "missing")
		}, "ERROR database error err="},
		{"a broken table", func(t *testing.T, f *storetest.Fixture) {
			t.Helper()
			f.Exec("DROP TABLE chats; CREATE TABLE chats (x TEXT)")
		}, "ERROR database error err="},
		{"a bad allowlist", func(t *testing.T, f *storetest.Fixture) {
			t.Helper()
			f.Config.ReadAllowlist = "not-a-number"
		}, `ERROR read allowlist error err="invalid read allowlist entry 'not-a-number': neither a JID nor a phone number"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStore(t)
			tc.setup(t, f)
			var logged bytes.Buffer
			pool := store.NewPool(logging.New(&logged))
			t.Cleanup(pool.Close)
			d := store.NewDB(pool, f.Config.StoreDir, f.Scope())
			if got := d.ListChats(t.Context(), chatOpts()); got == nil || len(got) != 0 {
				t.Errorf("chats %v", got)
			}
			if got := d.GetChat(t.Context(), group, true); got != nil {
				t.Errorf("chat %+v", got)
			}
			if line, _, _ := bytes.Cut(logged.Bytes(), []byte("\n")); !bytes.HasPrefix(line, []byte(tc.log)) {
				t.Errorf("logged %q, want %q...", line, tc.log)
			}
		})
	}
}

func TestReadAllowlist(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		list    string
		file    string
		bridge  []string
		want    []string
		hidden  string
		message string
	}{
		{"none", "", "", nil, []string{group, direct}, "", ""},
		{"by JID", direct, "", nil, []string{direct}, group,
			"Chat " + group + " is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)"},
		{"by phone number through the LID map", "+18046727669", "", nil, []string{direct}, group,
			"Chat " + group + " is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)"},
		{"from a file, with comments", "", group + " # the crew\n# nothing else\n", nil, []string{group}, direct,
			"Chat " + direct + " is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)"},
		{"the bridge's", "", "", []string{group}, []string{group}, direct,
			"Chat " + direct + " is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)"},
		{"both must allow a chat", group, "", []string{direct}, []string{}, group,
			"Chat " + group + " is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)"},
		{"a device suffix names the same chat", "18046727669:4@s.whatsapp.net", "", nil, []string{direct}, "", ""},
		{"a bad entry", "abc", "", nil, []string{}, group, "invalid read allowlist entry 'abc': neither a JID nor a phone number"},
		{"an unreadable bridge list", "", "", []string{"bad entry"}, []string{}, group,
			"the bridge's read_allowlist in messages.db is unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newStore(t)
			f.Config.ReadAllowlist = tc.list
			if tc.file != "" {
				f.Config.ReadAllowlistFile = filepath.Join(t.TempDir(), "allow")
				if err := os.WriteFile(f.Config.ReadAllowlistFile, []byte(tc.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.bridge != nil {
				f.PublishReadAllowlist(tc.bridge...)
			}
			d := f.DB()
			if got := jids(d.ListChats(t.Context(), chatOpts())); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("chats %v, want %v", got, tc.want)
			}
			if tc.hidden != "" {
				if got := d.CheckReadable(t.Context(), tc.hidden); got != tc.message {
					t.Errorf("CheckReadable %q, want %q", got, tc.message)
				}
			}
		})
	}
}

func TestReadAllowlistFileIsReadForEveryQuery(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	f := filepath.Join(t.TempDir(), "allow")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(group + "\n")
	s.Config.ReadAllowlistFile = f
	db := s.DB()
	if got := jids(db.ListChats(t.Context(), chatOpts())); !reflect.DeepEqual(got, []string{group}) {
		t.Errorf("got %v", got)
	}
	// The same DB sees the edited file: no restart needed.
	write(direct + "\n")
	if got := jids(db.ListChats(t.Context(), chatOpts())); !reflect.DeepEqual(got, []string{direct}) {
		t.Errorf("after the edit: %v", got)
	}
	if err := os.Remove(f); err != nil {
		t.Fatal(err)
	}
	const want = "cannot read WHATSAPP_READ_ALLOWLIST_FILE: open "
	if got := db.CheckReadable(t.Context(), group); len(got) < len(want) || got[:len(want)] != want {
		t.Errorf("missing file: %q", got)
	}
}

func TestEventFilter(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	if allow, err := f.DB().EventFilter(t.Context()); allow != nil || err != nil {
		t.Errorf("unrestricted: %v", err)
	}
	f.Config.ReadAllowlist = "18046727669"
	allow, err := f.DB().EventFilter(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for jid, want := range map[string]bool{direct: true, "18046727669@s.whatsapp.net": true, group: false, "": false} {
		if allow(jid) != want {
			t.Errorf("%q: %v", jid, !want)
		}
	}
	f.Config.ReadAllowlist = "x"
	if _, err := f.DB().EventFilter(t.Context()); err == nil {
		t.Error("bad allowlist accepted")
	}
}

func TestMeta(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	if got := f.DB().Meta(t.Context(), "fts_enabled"); got != "1" {
		t.Errorf("fts_enabled %q", got)
	}
	if got := f.DB().Meta(t.Context(), "missing"); got != "" {
		t.Errorf("missing %q", got)
	}
	upstream := storetest.New(t, storetest.Options{Schema: storetest.UpstreamSchema, Empty: true})
	if got := upstream.DB().Meta(t.Context(), "fts_enabled"); got != "" {
		t.Errorf("without bridge_meta %q", got)
	}
}
