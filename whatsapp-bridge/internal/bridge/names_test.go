package bridge

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func TestStoredChat(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	cases := []struct {
		name string
		chat types.JID
		want types.JID
	}{
		{"phone number", alicePN, alicePN},
		{"known LID", aliceLID, alicePN},
		{"own LID", ownLID, ownPN},
		{"unknown LID", types.NewJID("7777", types.HiddenUserServer), types.NewJID("7777", types.HiddenUserServer)},
		{"group", groupJID, groupJID},
		{"device JID", types.NewADJID("2000", 0, 3), alicePN},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := b.storedChat(t.Context(), c.chat); got != c.want {
				t.Errorf("got %s", got)
			}
		})
	}
	if got := newTestBridge(t).storedChat(t.Context(), aliceLID); got != aliceLID {
		t.Errorf("without a client: %s", got)
	}
}

func TestMergeLIDChats(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	for _, chat := range []string{"8000@lid", "7777@lid"} {
		if err := b.store.SaveChat(t.Context(), chat, "", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	mustStore(t, b.store, store.Message{ID: "L1", ChatJID: "8000@lid", Content: "sent to the LID", Timestamp: time.Now()})
	b.MergeLIDChats(t.Context())
	if r := ref(t, b, "L1", aliceChat); r.Content != "sent to the LID" {
		t.Errorf("merged %+v", r)
	}
	if name := b.store.ChatName(t.Context(), aliceChat); name != "Alice Full" {
		t.Errorf("name %q", name)
	}
	if b.store.MessageExists(t.Context(), "L1", "8000@lid") {
		t.Error("message left under the LID")
	}
	// The unknown LID stays; nothing to merge it into.
	if got := queryString(t, b, "SELECT jid FROM chats WHERE jid = '7777@lid'"); got != "7777@lid" {
		t.Errorf("unknown LID chat %q", got)
	}
	New(Deps{}).MergeLIDChats(t.Context()) // nothing to do without a store or client
}

func TestResolveName(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	b.fake.Book.Put(bobPN, types.ContactInfo{FirstName: "Bob"})
	cases := []struct {
		name     string
		jid, alt types.JID
		push     string
		want     string
	}{
		{"saved contact", alicePN, types.EmptyJID, "", "Alice Full"},
		{"through the LID map", aliceLID, types.EmptyJID, "", "Alice Full"},
		{"through the alternative JID", types.NewJID("7777", types.HiddenUserServer), alicePN, "", "Alice Full"},
		{"first name only", bobPN, types.EmptyJID, "push", "Bob"},
		{"push name", types.NewJID("4000", types.DefaultUserServer), types.EmptyJID, "Push", "Push"},
		{"unknown", types.NewJID("4000", types.DefaultUserServer), types.EmptyJID, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := b.resolveName(t.Context(), c.jid, c.alt, c.push); got != c.want {
				t.Errorf("got %q", got)
			}
		})
	}
	if got := newTestBridge(t).resolveName(t.Context(), alicePN, types.EmptyJID, "Push"); got != "Push" {
		t.Errorf("without a client: %q", got)
	}
}

func TestChatName(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	b.fake.Groups[groupJID] = &types.GroupInfo{JID: groupJID, GroupName: types.GroupName{Name: "Snow ops"}}
	if err := b.store.SaveChat(t.Context(), carlChat, "Carl", time.Now()); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		jid    types.JID
		conv   *waHistorySync.Conversation
		sender string
		want   string
	}{
		{"stored name kept", types.NewJID("c", types.DefaultUserServer), nil, "", "Carl"},
		{"history display name", testGroupJID, &waHistorySync.Conversation{DisplayName: new("Plows")}, "", "Plows"},
		{"history name", testGroupJID, &waHistorySync.Conversation{Name: new("Depot")}, "", "Depot"},
		{"group info", groupJID, nil, "", "Snow ops"},
		{"unknown group", types.NewJID("555", types.GroupServer), nil, "", "Group 555"},
		{"status updates", types.StatusBroadcastJID, nil, "", statusChatName},
		{"contact by LID", aliceLID, nil, "", "Alice Full"},
		{"sender fallback", bobPN, nil, "3000", "3000"},
		{"number fallback", bobPN, nil, "", "3000"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := b.chatName(t.Context(), c.jid, c.conv, c.sender); got != c.want {
				t.Errorf("got %q", got)
			}
		})
	}
}
