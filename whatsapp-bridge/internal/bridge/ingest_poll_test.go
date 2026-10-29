package bridge

import (
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waWeb"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func hashOf(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}

func TestMatchPollOptions(t *testing.T) {
	t.Parallel()
	got := matchPollOptions([]string{"North", "South"}, [][]byte{hashOf("South"), hashOf("East")})
	if strings.Join(got, ",") != "South,?" {
		t.Errorf("got %v", got)
	}
}

func TestLiveVotesAreDecryptedAndStored(t *testing.T) {
	t.Parallel()
	vote := &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{
		PollCreationMessageKey: &waCommon.MessageKey{ID: new("P1")},
		SenderTimestampMS:      new(time.Now().UnixMilli()),
	}}
	cases := []struct {
		name    string
		poll    bool
		decrypt func(context.Context, *events.Message) (*waE2E.PollVoteMessage, error)
		want    string // stored options; "" for none
		log     string
	}{
		{"vote", true, func(context.Context, *events.Message) (*waE2E.PollVoteMessage, error) {
			return &waE2E.PollVoteMessage{SelectedOptions: [][]byte{hashOf("South")}}, nil
		}, `["South"]`, `msg="poll vote"`},
		{"undecryptable", true, func(context.Context, *events.Message) (*waE2E.PollVoteMessage, error) {
			return nil, errors.New("no secret")
		}, "", "failed to decrypt poll vote"},
		{"unknown poll", false, func(context.Context, *events.Message) (*waE2E.PollVoteMessage, error) {
			return &waE2E.PollVoteMessage{}, nil
		}, "", "vote on unknown poll"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			b.fake.DecryptPollVoteFunc = c.decrypt
			if c.poll {
				if err := b.store.SavePoll(t.Context(), "P1", opsGroup, "Depot?", []string{"North", "South"}, 1); err != nil {
					t.Fatal(err)
				}
			}
			sub := b.events.Subscribe()
			b.HandleEvent(messageEvent(groupJID, aliceLID, "V1", vote))
			if got := queryString(t, b, "SELECT options FROM poll_votes WHERE message_id = 'P1'"); got != c.want {
				t.Errorf("stored %q", got)
			}
			if !strings.Contains(b.logs.String(), c.log) {
				t.Errorf("log lacks %q:\n%s", c.log, b.logs.String())
			}
			if c.want != "" {
				if e := nextEvent(t, sub); e.Type != "poll_vote" || e.Content != "South" || e.SenderName != "Alice Full" {
					t.Errorf("event %+v", e)
				}
			}
		})
	}
}

func TestPollVotesReplaceAndWithdraw(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	st := b.store
	if err := st.SavePoll(t.Context(), "P1", opsGroup, "Depot?", []string{"North", "South"}, 1); err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	steps := []struct {
		name    string
		options []string
		at      time.Time
		want    string
	}{
		{"first vote", []string{"North"}, t0, `["North"]`},
		{"changed", []string{"South"}, t0.Add(time.Minute), `["South"]`},
		{"late copy of an older vote", []string{"North"}, t0.Add(-time.Minute), `["South"]`},
		{"old withdrawal", nil, t0.Add(-2 * time.Minute), `["South"]`},
		{"withdrawal", nil, t0.Add(2 * time.Minute), ""},
	}
	for _, s := range steps {
		err := st.SavePollVote(t.Context(), store.PollVote{MessageID: "P1", ChatJID: opsGroup, VoterJID: "1@lid", VoterName: "David", Options: s.options, Timestamp: s.at})
		if err != nil {
			t.Fatal(err)
		}
		if got := queryString(t, b, "SELECT options FROM poll_votes WHERE message_id = 'P1'"); got != s.want {
			t.Errorf("%s: %q, want %q", s.name, got, s.want)
		}
	}
}

func TestHistoryPollVotesAreStored(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	if err := b.store.SavePoll(t.Context(), "P1", opsGroup, "Depot?", []string{"North", "South"}, 1); err != nil {
		t.Fatal(err)
	}
	web := &waWeb.WebMessageInfo{PollUpdates: []*waWeb.PollUpdate{{
		PollUpdateMessageKey: &waCommon.MessageKey{Participant: new("32547396399312@lid")},
		Vote:                 &waE2E.PollVoteMessage{SelectedOptions: [][]byte{hashOf("North")}},
		SenderTimestampMS:    new(time.Now().UnixMilli()),
	}}}
	b.storeHistoryPollVotes(t.Context(), types.NewJID("120", types.GroupServer), "P1", web)
	if got := queryString(t, b, "SELECT voter_jid || ' ' || options FROM poll_votes"); got != `32547396399312@lid ["North"]` {
		t.Errorf("stored %s", got)
	}
}

func TestKeySender(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	cases := []struct {
		name string
		key  *waCommon.MessageKey
		want types.JID
	}{
		{"ours", &waCommon.MessageKey{FromMe: new(true)}, ownPN},
		{"participant", &waCommon.MessageKey{Participant: new("8000@lid")}, aliceLID},
		{"direct chat", &waCommon.MessageKey{}, groupJID},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := b.keySender(groupJID, c.key); got != c.want {
				t.Errorf("got %s", got)
			}
		})
	}
}
