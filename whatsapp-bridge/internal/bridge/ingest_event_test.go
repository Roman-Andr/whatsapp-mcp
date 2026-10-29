package bridge

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/util/gcmutil"
	"google.golang.org/protobuf/proto"
)

var eventSecret = []byte("0123456789abcdef0123456789abcdef")

// encryptedResponse is an event response encrypted the way a responder's
// client does it.
func encryptedResponse(t *testing.T, responder, creator types.JID, eventID string, answer waE2E.EventResponseMessage_EventResponseType) *waE2E.Message {
	t.Helper()
	key, ad := eventResponseKey(responder, eventID, creator, eventSecret)
	plain, err := proto.Marshal(&waE2E.EventResponseMessage{Response: answer.Enum(), TimestampMS: new(time.Now().UnixMilli())})
	if err != nil {
		t.Fatal(err)
	}
	iv := []byte("123456789012")
	sealed, err := gcmutil.Encrypt(key, iv, plain, ad)
	if err != nil {
		t.Fatal(err)
	}
	return &waE2E.Message{EncEventResponseMessage: &waE2E.EncEventResponseMessage{
		EventCreationMessageKey: &waCommon.MessageKey{ID: new(eventID), Participant: new(creator.String()), RemoteJID: new(opsGroup)},
		EncPayload:              sealed, EncIV: iv,
	}}
}

func TestEventResponsesAreDecrypted(t *testing.T) {
	t.Parallel()
	creator := types.NewJID("7", types.HiddenUserServer)
	cases := []struct {
		name   string
		secret []byte
		want   string // stored response; "" for none
	}{
		{"known event", eventSecret, "going"},
		{"event from before the link", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			b.fake.MessageSecretFunc = func(_ context.Context, chat, sender types.JID, id types.MessageID) ([]byte, types.JID, error) {
				if chat != groupJID || sender != creator || id != "EV1" {
					t.Errorf("secret of %s by %s in %s", id, sender, chat)
				}
				return c.secret, types.EmptyJID, nil
			}
			sub := b.events.Subscribe()
			b.HandleEvent(messageEvent(groupJID, aliceLID, "R1", encryptedResponse(t, aliceLID, creator, "EV1", waE2E.EventResponseMessage_GOING)))
			if got := queryString(t, b, "SELECT response FROM event_responses WHERE message_id = 'EV1'"); got != c.want {
				t.Errorf("stored %q", got)
			}
			if c.want != "" {
				if e := nextEvent(t, sub); e.Type != "event_response" || e.Content != "going" || e.SenderName != "Alice Full" {
					t.Errorf("event %+v", e)
				}
			}
		})
	}
}

func TestEventResponseKeyRoundTrip(t *testing.T) {
	t.Parallel()
	responder := types.NewJID("5", types.HiddenUserServer)
	creator := types.NewJID("7", types.HiddenUserServer)
	key, ad := eventResponseKey(responder, "EV1", creator, eventSecret)
	plain, _ := proto.Marshal(&waE2E.EventResponseMessage{Response: waE2E.EventResponseMessage_GOING.Enum()})
	iv := []byte("123456789012")
	sealed, err := gcmutil.Encrypt(key, iv, plain, ad)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := gcmutil.Decrypt(key, iv, sealed, ad)
	if err != nil || string(opened) != string(plain) {
		t.Fatalf("round trip: %v", err)
	}
	if string(ad) != "EV1\x005@lid" {
		t.Errorf("additional data %q", ad)
	}
}

func TestKeyAuthor(t *testing.T) {
	t.Parallel()
	me := types.MessageInfo{MessageSource: types.MessageSource{Chat: groupJID, Sender: ownPN}}
	dm := types.MessageInfo{MessageSource: types.MessageSource{Chat: alicePN, Sender: alicePN}}
	cases := []struct {
		name string
		info types.MessageInfo
		key  *waCommon.MessageKey
		want types.JID
	}{
		{"our own event", me, &waCommon.MessageKey{FromMe: new(true)}, ownPN},
		{"direct chat", dm, &waCommon.MessageKey{RemoteJID: new(bobPN.String())}, bobPN},
		{"group", me, &waCommon.MessageKey{Participant: new(aliceLID.String())}, aliceLID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := keyAuthor(c.info, c.key)
			if err != nil || got != c.want {
				t.Errorf("got %s, %v", got, err)
			}
		})
	}
}

func TestEventResponsesReplaceEachOther(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	g := types.NewJID("g", types.GroupServer)
	who := types.NewJID("5", types.HiddenUserServer)
	now := time.Now()
	b.storeEventResponse(t.Context(), g, "EV1", who, types.EmptyJID, "Dave", &waE2E.EventResponseMessage{Response: waE2E.EventResponseMessage_MAYBE.Enum()}, now, false)
	b.storeEventResponse(t.Context(), g, "EV1", who, types.EmptyJID, "", &waE2E.EventResponseMessage{Response: waE2E.EventResponseMessage_GOING.Enum()}, now.Add(time.Minute), false)
	b.storeEventResponse(t.Context(), g, "EV1", who, types.EmptyJID, "", &waE2E.EventResponseMessage{Response: waE2E.EventResponseMessage_NOT_GOING.Enum()}, now.Add(-time.Minute), false)
	if got := queryString(t, b, "SELECT response || '|' || responder_name FROM event_responses WHERE message_id = 'EV1'"); got != "going|Dave" {
		t.Errorf("stored %q", got)
	}
}
