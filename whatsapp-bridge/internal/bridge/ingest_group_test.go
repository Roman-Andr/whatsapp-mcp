package bridge

import (
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// groupNotices lists the stored lines of testGroup in id order.
func groupNotices(t *testing.T, b *testBridge) []string {
	t.Helper()
	rows, err := b.store.DB().QueryContext(t.Context(), "SELECT content FROM messages WHERE chat_jid = ? ORDER BY id", testGroup)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var contents []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		contents = append(contents, c)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return contents
}

func TestMembershipChangesBecomeGroupHistory(t *testing.T) {
	t.Parallel()
	admin := types.NewJID("7", types.HiddenUserServer)
	newcomer := types.NewJID("5", types.HiddenUserServer)
	at := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name string
		evt  *events.GroupInfo
		want []string
	}{
		{"added and promoted", &events.GroupInfo{
			JID: testGroupJID, Sender: &admin, Timestamp: at,
			Join: []types.JID{newcomer}, Promote: []types.JID{types.NewJID("6", types.HiddenUserServer)},
		}, []string{"[group] 5 was added (by 7)", "[group] 6 is now an admin (by 7)"}},
		{"joined by link and left", &events.GroupInfo{
			JID: testGroupJID, Timestamp: at, JoinReason: "invite",
			Join: []types.JID{newcomer}, Leave: []types.JID{types.NewJID("6", types.HiddenUserServer)},
		}, []string{"[group] 5 joined with the invite link", "[group] 6 left"}},
		{"removed and demoted", &events.GroupInfo{
			JID: testGroupJID, Sender: &admin, Timestamp: at,
			Leave: []types.JID{newcomer}, Demote: []types.JID{types.NewJID("6", types.HiddenUserServer)},
		}, []string{"[group] 6 is no longer an admin (by 7)", "[group] 5 was removed (by 7)"}},
		{"settings", &events.GroupInfo{
			JID: testGroupJID, Sender: &admin, Timestamp: at,
			Name:     &types.GroupName{Name: "Plows"},
			Topic:    &types.GroupTopic{Topic: "north depot"},
			Announce: &types.GroupAnnounce{IsAnnounce: true},
		}, []string{"[group] description changed (by 7): north depot", "[group] name changed to \"Plows\" (by 7)", "[group] only admins may send (by 7)"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBridge(t)
			sub := b.events.Subscribe()
			b.HandleEvent(c.evt)
			b.HandleEvent(c.evt) // a repeated notification adds nothing
			if got := groupNotices(t, b); strings.Join(got, "\n") != strings.Join(c.want, "\n") {
				t.Errorf("notices %q", got)
			}
			if e := nextEvent(t, sub); e.Type != "group" || e.ChatJID != testGroup {
				t.Errorf("event %+v", e)
			}
		})
	}
	t.Run("no change", func(t *testing.T) {
		t.Parallel()
		b := newTestBridge(t)
		b.HandleEvent(&events.GroupInfo{JID: testGroupJID})
		if got := groupNotices(t, b); len(got) != 0 {
			t.Errorf("notices %q", got)
		}
	})
}

func TestWelcome(t *testing.T) {
	t.Parallel()
	welcomes := func(text string) func(*Deps) {
		return func(d *Deps) { d.Welcomes = map[string]string{opsGroup: text} }
	}
	readOnly := func(d *Deps) { d.ReadOnly = true }
	cases := []struct {
		name     string
		opts     []func(*Deps)
		user     types.JID
		sent     string // text sent; "" for none
		mentions string
		audited  string // audited result; "" for no audit line
	}{
		{"by name", []func(*Deps){welcomes("Hi {name}, routes are pinned")}, aliceLID, "Hi Alice Full, routes are pinned", "", "Message sent to 120@g.us"},
		{"with a mention", []func(*Deps){welcomes("Welcome {mention}")}, aliceLID, "Welcome @8000", "8000@lid", "Message sent to 120@g.us"},
		{"read-only", []func(*Deps){welcomes("Hi"), readOnly}, aliceLID, "", "", "skipped: read-only mode"},
		{"ourselves", []func(*Deps){welcomes("Hi")}, ownPN, "", "", ""},
		{"no welcome for the group", nil, aliceLID, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t, c.opts...)
			b.welcome(t.Context(), groupJID, c.user)
			sent := b.fake.Sent()
			switch {
			case c.sent == "" && len(sent) > 0:
				t.Errorf("sent %v", sent)
			case c.sent != "":
				if len(sent) != 1 {
					t.Fatalf("sent %d messages", len(sent))
				}
				m := sent[0].Message
				text := m.GetConversation() + m.GetExtendedTextMessage().GetText()
				mentions := strings.Join(m.GetExtendedTextMessage().GetContextInfo().GetMentionedJID(), ",")
				if text != c.sent || mentions != c.mentions {
					t.Errorf("sent %q mentioning %q", text, mentions)
				}
			}
			if audited := auditLines(t, b); (c.audited == "") != (audited == "") || !strings.Contains(audited, c.audited) {
				t.Errorf("audit %s, want %q", audited, c.audited)
			}
		})
	}
}

func TestJoinedGroupIsRecorded(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		evt  *events.JoinedGroup
		want string
	}{
		{"added", &events.JoinedGroup{}, "joined: you were added to the group"},
		{"by link", &events.JoinedGroup{Reason: "invite"}, "joined: you joined with an invite link"},
		{"created", &events.JoinedGroup{Type: "new"}, "joined: the group was created"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newTestBridge(t)
			sub := b.events.Subscribe()
			c.evt.GroupInfo = types.GroupInfo{JID: groupJID, GroupName: types.GroupName{Name: "Plows"}}
			b.HandleEvent(c.evt)
			if name := b.store.ChatName(t.Context(), opsGroup); name != "Plows" {
				t.Errorf("name %q", name)
			}
			if e := nextEvent(t, sub); e.Content != c.want {
				t.Errorf("event %+v", e)
			}
		})
	}
}
