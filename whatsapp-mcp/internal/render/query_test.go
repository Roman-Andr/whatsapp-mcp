package render

// Message lists against the fixture rows of internal/storetest.

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/storetest"
)

func TestListMessagesShowsIDsNamesRepliesReactions(t *testing.T) {
	t.Parallel()
	out := mustList(t, newStore(t), opts(func(o *store.ListOpts) { o.ChatJID = group; noContext(o) }))
	// Names come from the contact store through the LID map, not the bare
	// numbers.
	want := "[2026-10-27 12:05:00 +03:00] Chat: Snow operations system From: Antonina: [image | id: A3 | chat: " + group + " | deleted by sender] see screenshot\n" +
		"[2026-10-27 12:01:00 +03:00] Chat: Snow operations system From: Me: [id: A2 | chat: " + group + " | edited] " +
		"(reply to A1 David Bender: \"The plow route on Main street is wrong\") Fixed it [reactions: 👍 David Bender]\n" +
		"[2026-10-27 12:00:00 +03:00] Chat: Snow operations system From: David Bender: [id: A1 | chat: " + group + "] The plow route on Main street is wrong\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestLastInteraction(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	want := "[2026-10-27 12:05:00 +03:00] Chat: Snow operations system From: Antonina: [image | id: A3 | chat: " + group + " | deleted by sender] see screenshot\n"
	if got := LastInteraction(t.Context(), s.DB(), "28952508764308@lid"); got == nil || *got != want {
		t.Errorf("got %v", got)
	}
	if got := LastInteraction(t.Context(), s.DB(), "999@s.whatsapp.net"); got != nil {
		t.Errorf("unknown contact: %q", *got)
	}
	s.Config.ReadAllowlist = "x"
	if got := LastInteraction(t.Context(), s.DB(), "28952508764308@lid"); got == nil ||
		*got != "invalid read allowlist entry 'x': neither a JID nor a phone number" {
		t.Errorf("bad allowlist: %v", got)
	}
}

func TestMissingStoreIsShownAsText(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Config.StoreDir = filepath.Join(t.TempDir(), "missing")
	const prefix = "Database error: "
	texts := map[string]string{
		"list":    mustList(t, s, store.DefaultListOpts()),
		"context": MessageContext(t.Context(), s.DB(), "A1", 1, 1, ""),
		"last":    *LastInteraction(t.Context(), s.DB(), "x@lid"),
	}
	for name, text := range texts {
		if !strings.HasPrefix(text, prefix) {
			t.Errorf("%s: %q", name, text)
		}
	}
}

func TestBrokenStoreIsShownAsText(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec("DROP TABLE reactions; CREATE TABLE reactions (x TEXT)")
	const want = "Database error: no such column: message_id"
	if got := mustList(t, s, opts(func(o *store.ListOpts) { o.ChatJID = group; noContext(o) })); got != want {
		t.Errorf("list: %q", got)
	}
	if got := MessageContext(t.Context(), s.DB(), "A2", 1, 1, ""); got != want {
		t.Errorf("context: %q", got)
	}
	if got := LastInteraction(t.Context(), s.DB(), "32547396399312@lid"); got != nil {
		t.Errorf("last: %q", *got)
	}
}

func TestFullTextAndSubstringSearch(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	search := func(q string) string {
		return mustList(t, s, opts(func(o *store.ListOpts) { o.Query = q; noContext(o) }))
	}
	storetest.AssertContains(t, search("street plow"), "id: A1")
	// Substring inside a word: not an FTS match, still found through LIKE.
	storetest.AssertContains(t, search("treet"), "id: A1")
	if got := search("nothing-like-this"); got != "No messages to display." {
		t.Errorf("got %q", got)
	}
	// FTS syntax characters in user input must not fail.
	storetest.AssertNotContains(t, search(`"unbalanced (AND`), "Database error")
}

func TestContextIsChronologicalAndDeduplicated(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	out := mustList(t, s, opts(func(o *store.ListOpts) { o.ChatJID = group; o.Limit = 2 }))
	ids := storetest.IDsIn(out)
	if len(ids) != len(slices.Compact(slices.Sorted(slices.Values(ids)))) {
		t.Errorf("duplicates in %v", ids)
	}
	ctx := MessageContext(t.Context(), s.DB(), "A2", 5, 5, "")
	if got := storetest.IDsIn(ctx); !reflect.DeepEqual(got, []string{"A1", "A2", "A3"}) {
		t.Errorf("context order %v", got)
	}
	if got := MessageContext(t.Context(), s.DB(), "missing", 5, 5, ""); got != "Message with ID missing not found" {
		t.Errorf("missing: %q", got)
	}
}

func TestLimitsAreClamped(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	o := chatOpts()
	o.Limit, o.Page = -5, -1
	if n := len(s.DB().ListChats(t.Context(), o)); n != 1 {
		t.Errorf("got %d chats", n)
	}
	out := mustList(t, s, opts(func(o *store.ListOpts) { o.Limit = 0; noContext(o) }))
	if n := strings.Count(out, "id:"); n != 1 {
		t.Errorf("got %d messages", n)
	}
}

func TestWorksOnUpstreamSchema(t *testing.T) {
	t.Parallel()
	s := storetest.New(t, storetest.Options{Schema: storetest.UpstreamSchema, Empty: true})
	s.Exec(`
		INSERT INTO chats (jid, name, last_message_time) VALUES ('c@s.whatsapp.net', 'Bob', '2026-10-01 10:00:00+00:00');
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
			VALUES ('U1', 'c@s.whatsapp.net', 'c', 'old style row', '2026-10-01 10:00:00+00:00', 0);`)
	out := mustList(t, s, opts(func(o *store.ListOpts) { o.Query = "style" }))
	storetest.AssertContains(t, out, "id: U1", "From: Bob:")
	if got := jids(s.DB().GetContactChats(t.Context(), "c@s.whatsapp.net", 20, 0)); len(got) == 0 || got[0] != "c@s.whatsapp.net" {
		t.Errorf("got %v", got)
	}
	if got := s.DB().ListUnreadChats(t.Context(), 20); len(got) != 0 {
		t.Errorf("unread on upstream schema: %v", jids(got))
	}
	if c := s.DB().ListChats(t.Context(), chatOpts())[0]; c.UnreadCount != nil {
		t.Error("unread count on upstream schema")
	}
}

func TestMentionsAndLIDChatNamesUseContactNames(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
		VALUES ('M1', '` + group + `', '79966764464', '@32547396399312 zoom? @123', '2026-10-27 14:00:00+03:00', 1)`)
	out := mustList(t, s, opts(func(o *store.ListOpts) { o.Query = "zoom"; noContext(o) }))
	storetest.AssertContains(t, out, "@David Bender zoom? @123")
	for _, c := range s.DB().ListChats(t.Context(), chatOpts()) {
		if c.JID == "32547396399312@lid" && store.Deref(c.Name) != "David Bender" {
			t.Errorf("name %q", store.Deref(c.Name))
		}
	}
}

func TestPollVotesAreShownNextToThePoll(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec(`
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
			VALUES ('P1', '` + group + `', '79966764464', '[poll] Depot?: North / South / East', '2026-10-27 15:00:00+03:00', 1);
		INSERT INTO polls VALUES ('P1', '` + group + `', 'Depot?', '["North", "South", "East"]', 1);
		INSERT INTO poll_votes VALUES ('P1', '` + group + `', '32547396399312@lid', 'David Bender', '["North"]', '2026-10-27 15:01:00+03:00');
		INSERT INTO poll_votes VALUES ('P1', '` + group + `', '79966764464@s.whatsapp.net', 'Roman', '["North"]', '2026-10-27 15:02:00+03:00');
		INSERT INTO poll_votes VALUES ('P1', '` + group + `', '5@lid', NULL, '["South"]', '2026-10-27 15:03:00+03:00');`)
	out := mustList(t, s, opts(func(o *store.ListOpts) { o.ChatJID = group; o.Query = "Depot"; noContext(o) }))
	storetest.AssertContains(t, out, "[votes: North (2): David Bender, Roman; South (1): 5; East (0)]")
}

func TestDateFiltersCompareInstantsAcrossOffsets(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	// A1 was stored at 12:00:00.123+03:00, i.e. 09:00:00.123 UTC.
	out := mustList(t, s, opts(func(o *store.ListOpts) {
		o.After, o.Before, o.ChatJID = "2026-10-27T08:59:00Z", "2026-10-27T09:01:00Z", group
		noContext(o)
	}))
	storetest.AssertContains(t, out, "id: A1")
	out = mustList(t, s, opts(func(o *store.ListOpts) { o.After, o.ChatJID = "2026-10-27T09:01:00+00:00", group; noContext(o) }))
	storetest.AssertNotContains(t, out, "id: A1")
	_, err := ListMessages(t.Context(), s.DB(), opts(func(o *store.ListOpts) { o.After = "yesterday" }))
	if !errors.Is(err, store.ErrInvalidArgument) {
		t.Errorf("invalid date accepted: %v", err)
	}
}

func TestReadAllowlistFromEnvIntersectsWithTheBridge(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Config.ReadAllowlist = group + ", +18046727669"
	if got := jids(s.DB().ListChats(t.Context(), chatOpts())); !reflect.DeepEqual(got, []string{group, "32547396399312@lid"}) {
		t.Errorf("got %v", got)
	}
	s.PublishReadAllowlist(group)
	if got := jids(s.DB().ListChats(t.Context(), chatOpts())); !reflect.DeepEqual(got, []string{group}) {
		t.Errorf("got %v", got)
	}
	s.Config.ReadAllowlist = "John"
	// A typo hides everything instead of lifting the restriction.
	if got := s.DB().ListChats(t.Context(), chatOpts()); len(got) != 0 {
		t.Errorf("got %v", jids(got))
	}
	storetest.AssertContains(t, mustList(t, s, store.DefaultListOpts()), "invalid read allowlist entry")
}

func TestReadAllowlistFile(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	f := filepath.Join(t.TempDir(), "allow")
	if err := os.WriteFile(f, []byte("# the crew\n"+group+"  # group\n\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.Config.ReadAllowlistFile = f
	if got := jids(s.DB().ListChats(t.Context(), chatOpts())); !reflect.DeepEqual(got, []string{group}) {
		t.Errorf("got %v", got)
	}
	s.Config.ReadAllowlistFile = filepath.Join(t.TempDir(), "missing")
	storetest.AssertContains(t, mustList(t, s, store.DefaultListOpts()), "cannot read WHATSAPP_READ_ALLOWLIST_FILE")
}

func TestSearchIgnoresCaseInEveryScript(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec(`
		INSERT INTO chats (jid, name, last_message_time) VALUES ('7@s.whatsapp.net', 'Давид Бендер', '2026-10-27 10:00:00+03:00');
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
			VALUES ('R1', '7@s.whatsapp.net', '7', 'Маршрут по Главной улице', '2026-10-27 10:00:00+03:00', 0);`)
	o := chatOpts()
	o.Query = "давид"
	if got := jids(s.DB().ListChats(t.Context(), o)); !reflect.DeepEqual(got, []string{"7@s.whatsapp.net"}) {
		t.Errorf("chats %v", got)
	}
	var contacts []string
	for _, c := range s.DB().SearchContacts(t.Context(), "БЕНДЕР", 50) {
		contacts = append(contacts, c.JID)
	}
	if !reflect.DeepEqual(contacts, []string{"7@s.whatsapp.net"}) {
		t.Errorf("contacts %v", contacts)
	}
	// Substring inside a word: only the LIKE fallback can find it.
	storetest.AssertContains(t, mustList(t, s, opts(func(o *store.ListOpts) { o.Query = "ЛАВН"; noContext(o) })), "id: R1")
	// Full case folding, as Python's casefold: ß matches SS.
	storetest.Exec(t, s.WhatsappDB(), "INSERT INTO whatsmeow_contacts VALUES ('me', '49@s.whatsapp.net', 'Straße', NULL, NULL, NULL, NULL)")
	if got := s.DB().SearchContacts(t.Context(), "STRASSE", 50); len(got) != 1 || got[0].JID != "49@s.whatsapp.net" {
		t.Errorf("casefold: %v", got)
	}
}

func TestEventResponsesAndTimeOffsetsAreShown(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec(`
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me)
			VALUES ('E1', '` + group + `', '79966764464', '[event] Depot meeting, 2026-11-01 18:00 +03:00', '2026-10-27 16:00:00+03:00', 1);
		INSERT INTO event_responses VALUES ('E1', '` + group + `', '32547396399312@lid', 'David Bender', 'going', 1, '2026-10-27 16:01:00+03:00');
		INSERT INTO event_responses VALUES ('E1', '` + group + `', '5@lid', NULL, 'maybe', 0, '2026-10-27 16:02:00+03:00');`)
	out := mustList(t, s, opts(func(o *store.ListOpts) { o.ChatJID = group; o.Query = "Depot meeting"; noContext(o) }))
	storetest.AssertContains(t, out, "[responses: going (1): David Bender +1; maybe (1): 5]")
	if !strings.HasPrefix(out, "[2026-10-27 16:00:00 +03:00]") {
		t.Errorf("got %q", out)
	}
}

func TestMediaDetailsAreShown(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec(`
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, media_seconds, media_detail)
			VALUES ('V1', '` + group + `', '5', '', '2026-10-27 17:00:00+03:00', 0, 'audio', 75, 'voice');
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, media_width, media_height)
			VALUES ('I1', '` + group + `', '5', '', '2026-10-27 17:01:00+03:00', 0, 'image', 1280, 720);`)
	out := mustList(t, s, opts(func(o *store.ListOpts) { o.ChatJID, o.After = group, "2026-10-27T16:59:00+03:00"; noContext(o) }))
	want := "[2026-10-27 17:01:00 +03:00] Chat: Snow operations system From: 5: [image 1280x720 | id: I1 | chat: " + group + "] \n" +
		"[2026-10-27 17:00:00 +03:00] Chat: Snow operations system From: 5: [audio (voice) 1:15 | id: V1 | chat: " + group + "] \n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

func TestStarredMessagesAreMarkedAndFilterable(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Exec("UPDATE messages SET starred = 1 WHERE id = 'A2'")
	out := mustList(t, s, opts(func(o *store.ListOpts) { o.StarredOnly = true; noContext(o) }))
	storetest.AssertContains(t, out, "id: A2", "starred")
	storetest.AssertNotContains(t, out, "id: A1")
}
