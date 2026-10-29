package watest

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
)

var (
	own   = types.NewJID("1000", types.DefaultUserServer)
	lid   = types.NewJID("9000", types.HiddenUserServer)
	alice = types.NewJID("2000", types.DefaultUserServer)
)

func TestSendRecords(t *testing.T) {
	t.Parallel()
	c := New(own, lid)
	for i, want := range []string{"SENT1", "SENT2"} {
		resp, err := c.SendMessage(t.Context(), alice, &waE2E.Message{Conversation: new("hi")})
		if err != nil || resp.ID != want || !resp.Timestamp.Equal(SentAt) {
			t.Errorf("send %d: %+v %v", i, resp, err)
		}
	}
	if sent := c.Sent(); len(sent) != 2 || sent[0].To != alice || sent[1].Message.GetConversation() != "hi" {
		t.Errorf("sent %+v", sent)
	}
	if calls := strings.Join(c.Calls(), ","); calls != "SendMessage,SendMessage" {
		t.Errorf("calls %s", calls)
	}
}

func TestUnsetOverridePanicsClearly(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "Download called without DownloadFunc") {
			t.Errorf("panic %v", r)
		}
	}()
	New(own, lid).Download(t.Context(), nil)
}

func TestStores(t *testing.T) {
	t.Parallel()
	c := New(own, lid)
	aliceLID := types.NewJID("8000", types.HiddenUserServer)
	c.LIDMap.Put(aliceLID, alice)
	c.Book.Put(alice, types.ContactInfo{FullName: "Alice"})
	if pn, _ := c.LIDs().GetPNForLID(t.Context(), aliceLID); pn != alice {
		t.Errorf("PN %s", pn)
	}
	if l, _ := c.LIDs().GetLIDForPN(t.Context(), alice); l != aliceLID {
		t.Errorf("LID %s", l)
	}
	if contact, _ := c.Contacts().GetContact(t.Context(), alice); !contact.Found || contact.FullName != "Alice" {
		t.Errorf("contact %+v", contact)
	}
	if (&Client{}).LIDs() != nil || (&Client{}).Contacts() != nil {
		t.Error("stores without maps")
	}
}

func TestParseWebMessage(t *testing.T) {
	t.Parallel()
	group := types.NewJID("120", types.GroupServer)
	c := New(own, lid)
	cases := []struct {
		name   string
		chat   types.JID
		key    *waCommon.MessageKey
		sender types.JID
	}{
		{"own message", alice, &waCommon.MessageKey{ID: new("M1"), FromMe: new(true)}, own},
		{"group member", group, &waCommon.MessageKey{ID: new("M2"), Participant: new(alice.String())}, alice},
		{"direct chat", alice, &waCommon.MessageKey{ID: new("M3")}, alice},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			evt, err := c.ParseWebMessage(tc.chat, &waWeb.WebMessageInfo{Key: tc.key, MessageTimestamp: new(uint64(1700000000))})
			if err != nil || evt.Info.Sender != tc.sender || evt.Info.ID != tc.key.GetID() || evt.Info.Timestamp.Unix() != 1700000000 {
				t.Errorf("%+v %v", evt, err)
			}
		})
	}
	if _, err := c.ParseWebMessage(alice, &waWeb.WebMessageInfo{}); err == nil {
		t.Error("message without id parsed")
	}
}
