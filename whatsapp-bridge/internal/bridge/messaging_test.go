package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func TestVCard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, person, phone string
		want                []string // substrings; nil for an error
	}{
		{"formatted number", "Dave Bender", "+1 (804) 672-7669", []string{"FN:Dave Bender\n", "waid=18046727669:+18046727669"}},
		{"line breaks in the name", "Dave\nBender", "123", []string{"FN:Dave Bender\n"}},
		{"no name", "", "123", nil},
		{"not a number", "Dave", "call me", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			v, err := vcardFor(c.person, c.phone)
			if c.want == nil {
				if err == nil {
					t.Errorf("accepted: %q", v)
				}
				return
			}
			for _, w := range c.want {
				if !strings.Contains(v, w) {
					t.Errorf("%q lacks %q (%v)", v, w, err)
				}
			}
		})
	}
}

func TestVotePoll(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	b.fake.PollVoteFunc = func(_ context.Context, poll *types.MessageInfo, options []string) (*waE2E.Message, error) {
		if poll.ID != "P1" || poll.Sender != aliceLID {
			t.Errorf("vote on %+v", poll)
		}
		return &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{}}, nil
	}
	mustStore(t, b.store, store.Message{ID: "P1", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "[poll] Depot?: North / South", Timestamp: time.Now()})
	if err := b.store.SavePoll(t.Context(), "P1", opsGroup, "Depot?", []string{"North", "South"}, 1); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		options []string
		want    string
		stored  string
	}{
		{"vote", []string{"South"}, "Voted South in poll P1", `["South"]`},
		{"withdraw", nil, "Withdrew vote in poll P1", ""},
		{"unknown option", []string{"East"}, `"East" is not an option of this poll (options: North / South)`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if resp := b.VotePoll(t.Context(), VoteRequest{ChatJID: opsGroup, MessageID: "P1", Options: c.options}); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
			if got := queryString(t, b, "SELECT options FROM poll_votes WHERE voter_jid = '1000@s.whatsapp.net'"); got != c.stored {
				t.Errorf("stored %q", got)
			}
		})
	}
	mustStore(t, b.store, store.Message{ID: "T1", ChatJID: opsGroup, Content: "text", Timestamp: time.Now()})
	if resp := b.VotePoll(t.Context(), VoteRequest{ChatJID: opsGroup, MessageID: "T1"}); resp.Message != "Message T1 is not a poll the bridge knows" {
		t.Errorf("%+v", resp)
	}
}

func TestSendContact(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	cases := []struct {
		name string
		req  ContactRequest
		want string
	}{
		{"from name and phone", ContactRequest{Recipient: aliceChat, Name: "Dave", Phone: "+1 804"}, "Contact Dave sent to 2000@s.whatsapp.net"},
		{"vCard as given", ContactRequest{Recipient: aliceChat, VCard: "BEGIN:VCARD\nEND:VCARD"}, "Contact Contact sent to 2000@s.whatsapp.net"},
		{"not a vCard", ContactRequest{Recipient: aliceChat, VCard: "hello"}, "vcard must start with BEGIN:VCARD"},
		{"bad phone", ContactRequest{Recipient: aliceChat, Name: "Dave", Phone: "x"}, `phone "x" is not a phone number`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if resp := b.SendContact(t.Context(), c.req); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
		})
	}
	if got := b.fake.Sent()[0].Message.GetContactMessage().GetDisplayName(); got != "Dave" {
		t.Errorf("display name %q", got)
	}
}

func TestForward(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	mustStore(t, b.store, store.Message{ID: "F1", ChatJID: opsGroup, Sender: "8000", Content: "road closed", Timestamp: time.Now()})
	mustStore(t, b.store, store.Message{ID: "DEL", ChatJID: opsGroup, Sender: "8000", Content: "oops", Timestamp: time.Now()})
	if _, err := b.store.MarkDeleted(t.Context(), "DEL", opsGroup, time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	resp := b.Forward(t.Context(), ForwardRequest{ChatJID: opsGroup, MessageID: "F1", Recipient: aliceChat})
	if !resp.Success || resp.Message != "Forwarded F1 to 2000@s.whatsapp.net" {
		t.Fatalf("%+v", resp)
	}
	ext := b.fake.Sent()[0].Message.GetExtendedTextMessage()
	if ext.GetText() != "road closed" || !ext.GetContextInfo().GetIsForwarded() {
		t.Errorf("forwarded %+v", ext)
	}
	refusals := []struct{ name, id, want string }{
		{"unknown", "NOPE", "Message NOPE not found in chat 120@g.us"},
		{"deleted", "DEL", "Message DEL was deleted by its sender"},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			if resp := b.Forward(t.Context(), ForwardRequest{ChatJID: opsGroup, MessageID: c.id, Recipient: aliceChat}); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
		})
	}
	hidden := newOnlineBridge(t, allowRead(t, aliceChat))
	if resp := hidden.Forward(t.Context(), ForwardRequest{ChatJID: opsGroup, MessageID: "F1", Recipient: aliceChat}); !resp.Refused {
		t.Errorf("forwarded from a hidden chat: %+v", resp)
	}
}

func TestStar(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	mustStore(t, b.store, store.Message{ID: "S1", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "keep", Timestamp: time.Now()})
	cases := []struct {
		name    string
		starred bool
		want    string
		stored  string
	}{
		{"star", true, "Starred S1", "1"},
		{"unstar", false, "Unstarred S1", "0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if resp := b.Star(t.Context(), StarRequest{ChatJID: opsGroup, MessageID: "S1", Starred: c.starred}); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
			if got := queryString(t, b, "SELECT starred FROM messages WHERE id = 'S1'"); got != c.stored {
				t.Errorf("starred %q", got)
			}
		})
	}
}

func TestSendFailuresAreReported(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	b.fake.SendMessageFunc = func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		return whatsmeow.SendResponse{}, errors.New("timed out")
	}
	cases := []struct {
		name string
		send func() SendMessageResponse
		want string
	}{
		{"contact", func() SendMessageResponse {
			return b.SendContact(t.Context(), ContactRequest{Recipient: aliceChat, Name: "D", Phone: "1"})
		}, "Error sending contact: timed out"},
		{"location", func() SendMessageResponse {
			return b.SendLocation(t.Context(), LocationRequest{Recipient: aliceChat})
		}, "Error sending location: timed out"},
		{"poll", func() SendMessageResponse {
			return b.SendPoll(t.Context(), PollRequest{Recipient: aliceChat, Question: "Q", Options: []string{"A", "B"}})
		}, "Error sending poll: timed out"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if resp := c.send(); resp.Success || resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
		})
	}
}
