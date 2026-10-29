package store_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/storetest"
)

func ids(messages []*store.Message) []string {
	out := []string{}
	for _, m := range messages {
		out = append(out, m.ID)
	}
	return out
}

func TestListMessages(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		edit func(o *store.ListOpts)
		want []string
	}{
		{"newest first", func(o *store.ListOpts) { o.IncludeContext = false }, []string{"A3", "A2", "A1", "D1"}},
		{"after a time", func(o *store.ListOpts) { o.IncludeContext, o.After = false, "2026-10-27T09:00:30Z" }, []string{"A3", "A2"}},
		{"before a time", func(o *store.ListOpts) { o.IncludeContext, o.Before = false, "2026-10-27T12:00:30+03:00" }, []string{"A1", "D1"}},
		{"by sender JID", func(o *store.ListOpts) { o.IncludeContext, o.SenderPhoneNumber = false, direct }, []string{"A1", "D1"}},
		{"by chat", func(o *store.ListOpts) { o.IncludeContext, o.ChatJID = false, direct }, []string{"D1"}},
		{"by word", func(o *store.ListOpts) { o.IncludeContext, o.Query = false, "PLOW" }, []string{"A1"}},
		{"by fragment", func(o *store.ListOpts) { o.IncludeContext, o.Query = false, "creensh" }, []string{"A3"}},
		{"starred", func(o *store.ListOpts) { o.IncludeContext, o.StarredOnly = false, true }, []string{}},
		{"with context, oldest first per match", func(o *store.ListOpts) { o.Query = "fixed" }, []string{"A1", "A2", "A3"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := newStore(t).DB()
			db, err := d.Open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			o := store.DefaultListOpts()
			tc.edit(&o)
			got, err := d.ListMessages(t.Context(), db, o)
			if err != nil || !reflect.DeepEqual(ids(got), tc.want) {
				t.Errorf("got %v %v, want %v", ids(got), err, tc.want)
			}
		})
	}
}

func TestListMessagesRejectsBadDates(t *testing.T) {
	t.Parallel()
	d := newStore(t).DB()
	db, err := d.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	o := store.DefaultListOpts()
	o.After = "yesterday"
	_, err = d.ListMessages(t.Context(), db, o)
	const want = "Invalid date format for 'after': yesterday. Please use ISO-8601 format."
	if !errors.Is(err, store.ErrInvalidArgument) || err.Error() != want {
		t.Errorf("got %v", err)
	}
}

func TestMessageContext(t *testing.T) {
	t.Parallel()
	d := newStore(t).DB()
	db, err := d.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got, err := d.MessageContext(t.Context(), db, "A2", group, 5, 5, nil)
	if err != nil || !reflect.DeepEqual(ids(got), []string{"A1", "A2", "A3"}) {
		t.Errorf("got %v %v", ids(got), err)
	}
	for _, s := range []store.Chats{nil, {direct: true}} {
		_, err = d.MessageContext(t.Context(), db, "A9", "", 1, 1, s)
		if !errors.Is(err, store.ErrNotFound) || err.Error() != "Message with ID A9 not found" {
			t.Errorf("missing: %v", err)
		}
	}
	_, err = d.MessageContext(t.Context(), db, "A2", "", 1, 1, store.Chats{direct: true})
	if !errors.Is(err, store.ErrNotFound) || err.Error() != "Message with ID A2 not found" {
		t.Errorf("hidden: %v", err)
	}
}

func TestDetailsAreAttached(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	f.Exec(`
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES ('P1', '` + group + `', '5', '[poll] Depot?', '2026-10-27 12:10:00+03:00', 0);
		INSERT INTO polls (message_id, chat_jid, question, options, selectable_count) VALUES ('P1', '` + group + `', 'Depot?', '["North","South"]', 1);
		INSERT INTO poll_votes (message_id, chat_jid, voter_jid, voter_name, options, timestamp) VALUES ('P1', '` + group + `', '7@s.whatsapp.net', '', '["South"]', '2026-10-27 12:11:00+03:00');
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me) VALUES ('E1', '` + group + `', '5', '[event] Party', '2026-10-27 12:12:00+03:00', 0);
		INSERT INTO event_responses (message_id, chat_jid, responder_jid, responder_name, response, extra_guests, timestamp) VALUES ('E1', '` + group + `', '8@lid', 'Ann', 'going', 2, '2026-10-27 12:13:00+03:00');
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type) VALUES ('V1', '` + group + `', '5', '', '2026-10-27 12:14:00+03:00', 0, 'audio');`)
	d := f.DB()
	if err := os.MkdirAll(d.TranscriptsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.TranscriptsDir(), storetest.Group+"_V1.txt"), []byte(" hello \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := d.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	o := store.DefaultListOpts()
	o.IncludeContext, o.After = false, "2026-10-27T12:09:00+03:00"
	got, err := d.ListMessages(t.Context(), db, o)
	if err != nil || !reflect.DeepEqual(ids(got), []string{"V1", "E1", "P1"}) {
		t.Fatalf("got %v %v", ids(got), err)
	}
	if v := got[0]; v.Transcript != "hello" {
		t.Errorf("transcript %q", v.Transcript)
	}
	if e := got[1].EventResponses; e == nil || !reflect.DeepEqual(e.Who, map[string][]string{"going": {"Ann +2"}}) {
		t.Errorf("event responses %+v", e)
	}
	if p := got[2].PollVotes; p == nil || !reflect.DeepEqual(p.Keys, []string{"North", "South"}) ||
		!reflect.DeepEqual(p.Who, map[string][]string{"North": {}, "South": {"7"}}) {
		t.Errorf("poll votes %+v", p)
	}
}

func TestLastInteraction(t *testing.T) {
	t.Parallel()
	d := newStore(t).DB()
	db, err := d.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	m, err := d.LastInteraction(t.Context(), db, direct)
	if err != nil || m == nil || m.ID != "A1" {
		t.Errorf("got %+v %v", m, err)
	}
	if m, err := d.LastInteraction(t.Context(), db, "999@s.whatsapp.net"); m != nil || err != nil {
		t.Errorf("unknown: %+v %v", m, err)
	}
}

func TestGetMediaInfo(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	cases := []struct {
		name, id, chat string
		scope          string
		want           *store.MediaInfo
	}{
		{"an image", "A3", group, "", &store.MediaInfo{MediaType: "image", Filename: "image_1.jpg"}},
		{"no media", "A1", group, "", nil},
		{"a hidden chat", "A3", group, direct, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f.Config.ReadAllowlist = tc.scope
			if got := f.DB().GetMediaInfo(t.Context(), tc.id, tc.chat); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestExportMessages(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	got, err := f.DB().ExportMessages(t.Context(), group, "", "2026-10-27T12:04:00+03:00")
	if err != nil || !reflect.DeepEqual(ids(got), []string{"A1", "A2"}) {
		t.Fatalf("got %v %v", ids(got), err)
	}
	names := []string{store.Deref(got[0].SenderName), store.Deref(got[1].SenderName)}
	if !reflect.DeepEqual(names, []string{"David Bender", "Me"}) {
		t.Errorf("names %v", names)
	}
	f.Config.ReadAllowlist = direct
	if _, err := f.DB().ExportMessages(t.Context(), group, "", ""); err == nil ||
		err.Error() != "Chat "+group+" is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)" {
		t.Errorf("hidden: %v", err)
	}
}
