package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/media"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func messageEvent(chat, sender types.JID, id string, msg *waE2E.Message) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsGroup: chat.Server == types.GroupServer},
			ID:            id, Timestamp: time.Now(), PushName: "Push",
		},
		Message: msg,
	}
}

func protocolEvent(chat, sender types.JID, fromMe bool, kind waE2E.ProtocolMessage_Type, target, text string) *events.Message {
	pm := &waE2E.ProtocolMessage{Type: kind.Enum(), Key: &waCommon.MessageKey{ID: new(target)}}
	if text != "" {
		pm.EditedMessage = &waE2E.Message{Conversation: new(text)}
	}
	evt := messageEvent(chat, sender, "EVT", &waE2E.Message{ProtocolMessage: pm})
	evt.Info.IsFromMe = fromMe
	return evt
}

func TestLiveMessagesAreStoredAndPublished(t *testing.T) {
	t.Parallel()
	jobs := make(chan media.Job, 1)
	b := newOnlineBridge(t, func(d *Deps) {
		d.AutoDownload = media.NewQueue(t.Context(), media.NewAutoDownloadConfig(true, 1<<20, 1<<20), func(_ context.Context, job media.Job) error {
			jobs <- job
			return nil
		}, nil)
	})
	t.Cleanup(b.autoDownload.Close)
	sub := b.events.Subscribe()

	reply := &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{
		Text: new("on my way @1000"),
		ContextInfo: &waE2E.ContextInfo{
			StanzaID: new("Q1"), Participant: new(ownPN.String()),
			QuotedMessage: &waE2E.Message{Conversation: new("where are you?")},
			MentionedJID:  []string{ownPN.String()},
		},
	}}
	b.HandleEvent(messageEvent(groupJID, aliceLID, "M1", reply))
	r := ref(t, b, "M1", opsGroup)
	if r.Content != "on my way @1000" || r.SenderJID != "8000@lid" {
		t.Errorf("stored %+v", r)
	}
	row := queryString(t, b, "SELECT sender_name || '|' || quoted_id || '|' || quoted_text || '|' || mentions FROM messages WHERE id = 'M1'")
	if row != `Alice Full|Q1|where are you?|["1000@s.whatsapp.net"]` {
		t.Errorf("row %s", row)
	}
	evt := nextEvent(t, sub)
	if evt.Type != "message" || evt.ChatJID != opsGroup || evt.SenderName != "Alice Full" || evt.QuotedID != "Q1" || evt.Content != "on my way @1000" {
		t.Errorf("event %+v", evt)
	}
	if logs := b.logs.String(); !strings.Contains(logs, `msg=message`) || !strings.Contains(logs, `direction=←`) || !strings.Contains(logs, `text="on my way @1000"`) {
		t.Errorf("echo line missing:\n%s", logs)
	}

	photo := &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Caption: new("plow"), Mimetype: new("image/jpeg"), FileLength: new(uint64(100))}}
	b.HandleEvent(messageEvent(groupJID, aliceLID, "IMG", photo))
	select {
	case job := <-jobs:
		if job != (media.Job{MessageID: "IMG", ChatJID: opsGroup}) {
			t.Errorf("job %+v", job)
		}
	case <-time.After(time.Second):
		t.Error("the photo was not offered for download")
	}
	if logs := b.logs.String(); !strings.Contains(logs, "media_type=image") {
		t.Errorf("media echo missing:\n%s", logs)
	}
}

func TestMessagesWithoutContentAreSkipped(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	b.HandleEvent(messageEvent(groupJID, aliceLID, "EMPTY", &waE2E.Message{}))
	b.HandleEvent(&events.Message{Info: types.MessageInfo{ID: "NIL"}})
	if b.store.MessageExists(t.Context(), "EMPTY", opsGroup) {
		t.Error("empty message stored")
	}
}

func TestPollsAreStoredWithTheirOptions(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	poll := b.fake.BuildPollCreation("Depot?", []string{"North", "South"}, 1)
	b.HandleEvent(messageEvent(groupJID, aliceLID, "P1", poll))
	options, err := b.store.PollOptions(t.Context(), "P1", opsGroup)
	if err != nil || strings.Join(options, ",") != "North,South" {
		t.Errorf("options %v, %v", options, err)
	}
}

func TestReactions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		msg     *waE2E.Message
		decrypt func(context.Context, *events.Message) (*waE2E.ReactionMessage, error)
		want    string // stored emoji; "" for none
	}{
		{"plain", &waE2E.Message{ReactionMessage: &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: new("M1")}, Text: new("👍")}}, nil, "👍"},
		{"encrypted", &waE2E.Message{EncReactionMessage: &waE2E.EncReactionMessage{}}, func(context.Context, *events.Message) (*waE2E.ReactionMessage, error) {
			return &waE2E.ReactionMessage{Key: &waCommon.MessageKey{ID: new("M1")}, Text: new("🔥")}, nil
		}, "🔥"},
		{"undecryptable", &waE2E.Message{EncReactionMessage: &waE2E.EncReactionMessage{}}, func(context.Context, *events.Message) (*waE2E.ReactionMessage, error) {
			return nil, errors.New("no secret")
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			b.fake.DecryptReactionFunc = c.decrypt
			mustStore(t, b.store, store.Message{ID: "M1", ChatJID: opsGroup, Content: "plowed", Timestamp: time.Now(), IsFromMe: true})
			sub := b.events.Subscribe()
			b.HandleEvent(messageEvent(groupJID, aliceLID, "R1", c.msg))
			got := queryString(t, b, "SELECT emoji FROM reactions WHERE message_id = 'M1' AND chat_jid = ?", opsGroup)
			if got != c.want {
				t.Errorf("emoji %q, want %q", got, c.want)
			}
			if c.want != "" {
				if evt := nextEvent(t, sub); evt.Type != "reaction" || evt.Emoji != c.want || evt.SenderName != "Alice Full" {
					t.Errorf("event %+v", evt)
				}
			} else if !strings.Contains(b.logs.String(), "failed to decrypt reaction") {
				t.Errorf("failure not logged:\n%s", b.logs.String())
			}
		})
	}
}

func TestEncryptedEdits(t *testing.T) {
	t.Parallel()
	edit := func(kind waE2E.SecretEncryptedMessage_SecretEncType) *waE2E.Message {
		return &waE2E.Message{SecretEncryptedMessage: &waE2E.SecretEncryptedMessage{
			TargetMessageKey: &waCommon.MessageKey{ID: new("M1")}, SecretEncType: kind.Enum(),
		}}
	}
	cases := []struct {
		name    string
		msg     *waE2E.Message
		decrypt func(context.Context, *events.Message) (*waE2E.Message, error)
		want    string
	}{
		{"edit applied", edit(waE2E.SecretEncryptedMessage_MESSAGE_EDIT), func(context.Context, *events.Message) (*waE2E.Message, error) {
			return &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{EditedMessage: &waE2E.Message{Conversation: new("route C")}}}, nil
		}, "route C"},
		{"undecryptable edit", edit(waE2E.SecretEncryptedMessage_MESSAGE_EDIT), func(context.Context, *events.Message) (*waE2E.Message, error) {
			return nil, errors.New("no secret")
		}, "route A"},
		{"event edit ignored", edit(waE2E.SecretEncryptedMessage_EVENT_EDIT), nil, "route A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			b.fake.DecryptSecretFunc = c.decrypt
			mustStore(t, b.store, store.Message{ID: "M1", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "route A", Timestamp: time.Now()})
			b.HandleEvent(messageEvent(groupJID, aliceLID, "E1", c.msg))
			if got := ref(t, b, "M1", opsGroup).Content; got != c.want {
				t.Errorf("content %q, want %q", got, c.want)
			}
		})
	}
}

func TestOnlyAuthorsEditOrDeleteMessages(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	group := types.NewJID("120", types.GroupServer)
	david := types.NewJID("1", types.HiddenUserServer)
	mallory := types.NewJID("2", types.HiddenUserServer)
	mustStore(t, b.store, store.Message{ID: "D1", ChatJID: opsGroup, Sender: "1", SenderJID: "1@lid", Content: "route A", Timestamp: time.Now()})
	mustStore(t, b.store, store.Message{ID: "OLD", ChatJID: opsGroup, Sender: "1", Content: "legacy row", Timestamp: time.Now()})
	mustStore(t, b.store, store.Message{ID: "ME", ChatJID: opsGroup, Content: "mine", Timestamp: time.Now(), IsFromMe: true})

	steps := []struct {
		name     string
		evt      *events.Message
		id, want string
		deleted  bool
	}{
		{"forged edit", protocolEvent(group, mallory, false, waE2E.ProtocolMessage_MESSAGE_EDIT, "D1", "send money to X"), "D1", "route A", false},
		{"forged edit of mine", protocolEvent(group, mallory, false, waE2E.ProtocolMessage_MESSAGE_EDIT, "ME", "forged"), "ME", "mine", false},
		{"forged revoke", protocolEvent(group, mallory, false, waE2E.ProtocolMessage_REVOKE, "D1", ""), "D1", "route A", false},
		{"author's edit", protocolEvent(group, david, false, waE2E.ProtocolMessage_MESSAGE_EDIT, "D1", "route B"), "D1", "route B", false},
		{"edit of a legacy row", protocolEvent(group, david, false, waE2E.ProtocolMessage_MESSAGE_EDIT, "OLD", "legacy edited"), "OLD", "legacy edited", false},
		{"edit without text", protocolEvent(group, david, false, waE2E.ProtocolMessage_MESSAGE_EDIT, "D1", ""), "D1", "route B", false},
		{"own revoke", protocolEvent(group, types.EmptyJID, true, waE2E.ProtocolMessage_REVOKE, "ME", ""), "ME", "mine", true},
	}
	// The steps build on each other, so they run in order.
	for _, s := range steps {
		b.processMessage(t.Context(), s.evt, true)
		if r := ref(t, b, s.id, opsGroup); r.Content != s.want || r.Deleted != s.deleted {
			t.Errorf("%s: %q deleted=%v", s.name, r.Content, r.Deleted)
		}
	}
}

func TestGroupAdminsDeleteAnyMessage(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	admin := types.NewJID("7", types.HiddenUserServer)
	b.fake.Groups[groupJID] = &types.GroupInfo{JID: groupJID, Participants: []types.GroupParticipant{{JID: admin, IsAdmin: true}}}
	mustStore(t, b.store, store.Message{ID: "D1", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "spam", Timestamp: time.Now()})
	sub := b.events.Subscribe()
	b.HandleEvent(protocolEvent(groupJID, admin, false, waE2E.ProtocolMessage_REVOKE, "D1", ""))
	if !ref(t, b, "D1", opsGroup).Deleted {
		t.Error("admin deletion not applied")
	}
	if evt := nextEvent(t, sub); evt.Type != "delete" || evt.MessageID != "D1" {
		t.Errorf("event %+v", evt)
	}
}

func TestReactionsFollowTheChatTheMessageIsStoredIn(t *testing.T) {
	t.Parallel()
	st := openTestStore(t)
	mustStore(t, st, store.Message{ID: "S1", ChatJID: carlChat, Content: "sent by number", Timestamp: time.Now(), IsFromMe: true})
	cases := []struct{ name, id, want string }{
		{"stored under the number", "S1", carlChat},
		{"unknown message", "NOPE", "c@lid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := st.ResolveChat(t.Context(), c.id, "c@lid", carlChat); got != c.want {
				t.Errorf("resolved to %s", got)
			}
		})
	}
}

func TestUndecryptableMessagesLeaveAPlaceholderARealOneReplaces(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	sub := b.events.Subscribe()
	group := types.NewJID("g", types.GroupServer)
	info := types.MessageInfo{
		MessageSource: types.MessageSource{Chat: group, Sender: types.NewJID("5", types.HiddenUserServer), IsGroup: true},
		ID:            "U1", Timestamp: time.Now(),
	}
	b.HandleEvent(&events.UndecryptableMessage{Info: info})
	if r := ref(t, b, "U1", testGroup); r.Content != unavailablePlaceholder {
		t.Fatalf("placeholder: %+v", r)
	}
	if evt := nextEvent(t, sub); evt.Type != "unavailable" || evt.MessageID != "U1" {
		t.Errorf("event %+v", evt)
	}
	// A repeated failure does not publish again.
	b.HandleEvent(&events.UndecryptableMessage{Info: info})
	noEvent(t, sub)
	// Hidden failures and our own messages leave nothing.
	b.HandleEvent(&events.UndecryptableMessage{Info: types.MessageInfo{MessageSource: info.MessageSource, ID: "U2", Timestamp: time.Now()}, DecryptFailMode: events.DecryptFailHide})
	own := info
	own.ID, own.IsFromMe = "U3", true
	b.HandleEvent(&events.UndecryptableMessage{Info: own})
	for _, id := range []string{"U2", "U3"} {
		if b.store.MessageExists(t.Context(), id, testGroup) {
			t.Errorf("%s stored", id)
		}
	}

	// The resend replaces the placeholder.
	mustStore(t, b.store, store.Message{ID: "U1", ChatJID: testGroup, Sender: "5", Content: "Parcel delivered", Timestamp: info.Timestamp})
	if r := ref(t, b, "U1", testGroup); r.Content != "Parcel delivered" {
		t.Errorf("after resend: %q", r.Content)
	}
	// ... and a late duplicate failure cannot bring the placeholder back.
	b.onUndecryptable(t.Context(), &events.UndecryptableMessage{Info: info})
	if r := ref(t, b, "U1", testGroup); r.Content != "Parcel delivered" {
		t.Errorf("placeholder overwrote the message: %q", r.Content)
	}
}

func TestEditedText(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  *waE2E.Message
	}{
		{"top level", &waE2E.Message{Conversation: new("new")}},
		{"edited message wrapper", &waE2E.Message{EditedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{Conversation: new("new")}}}},
		{"protocol message", &waE2E.Message{ProtocolMessage: &waE2E.ProtocolMessage{EditedMessage: &waE2E.Message{Conversation: new("new")}}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := editedText(c.msg); got != "new" {
				t.Errorf("got %q", got)
			}
		})
	}
}
