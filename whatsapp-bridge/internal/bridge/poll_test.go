package bridge

import (
	"math"
	"strings"
	"testing"
)

func TestValidatePoll(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		req        PollRequest
		selectable int // -1 for a refusal
	}{
		{"default single choice", PollRequest{Question: "Depot?", Options: []string{"North", "South"}}, 1},
		{"two choices", PollRequest{Question: "Depot?", Options: []string{"A", "B", "C"}, SelectableCount: new(2)}, 2},
		{"any number", PollRequest{Question: "Depot?", Options: []string{"A", "B"}, SelectableCount: new(0)}, 0},
		{"no question", PollRequest{Options: []string{"A", "B"}}, -1},
		{"one option", PollRequest{Question: "Q", Options: []string{"A"}}, -1},
		{"duplicate option", PollRequest{Question: "Q", Options: []string{"A", "A"}}, -1},
		{"blank option", PollRequest{Question: "Q", Options: []string{"A", " "}}, -1},
		{"too many choices", PollRequest{Question: "Q", Options: []string{"A", "B"}, SelectableCount: new(3)}, -1},
		{"too many options", PollRequest{Question: "Q", Options: strings.Split("a b c d e f g h i j k l m", " ")}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			n, err := validatePoll(c.req)
			if c.selectable < 0 {
				if err == nil {
					t.Error("accepted")
				}
			} else if err != nil || n != c.selectable {
				t.Errorf("%d, %v", n, err)
			}
		})
	}
}

func TestValidateLocation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		req  LocationRequest
		ok   bool
	}{
		{"Moscow", LocationRequest{Latitude: 55.75, Longitude: 37.62}, true},
		{"latitude out of range", LocationRequest{Latitude: 91}, false},
		{"longitude out of range", LocationRequest{Longitude: -181}, false},
		{"not a number", LocationRequest{Latitude: math.NaN()}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if err := validateLocation(c.req); (err == nil) != c.ok {
				t.Errorf("err %v", err)
			}
		})
	}
}

func TestSendPollAndLocation(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	poll := b.SendPoll(t.Context(), PollRequest{Recipient: opsGroup, Question: "Depot?", Options: []string{"North", "South"}})
	if !poll.Success || poll.Message != "Poll sent to 120@g.us" {
		t.Fatalf("%+v", poll)
	}
	if options, err := b.store.PollOptions(t.Context(), poll.MessageID, opsGroup); err != nil || strings.Join(options, ",") != "North,South" {
		t.Errorf("stored options %v, %v", options, err)
	}
	if r := ref(t, b, poll.MessageID, opsGroup); !strings.HasPrefix(r.Content, "[poll] Depot?") {
		t.Errorf("stored %q", r.Content)
	}

	loc := b.SendLocation(t.Context(), LocationRequest{Recipient: aliceChat, Latitude: 55.75, Longitude: 37.62, Name: "Depot", Address: "Main st"})
	if !loc.Success || loc.Message != "Location sent to 2000@s.whatsapp.net" {
		t.Fatalf("%+v", loc)
	}
	if l := b.fake.Sent()[1].Message.GetLocationMessage(); l.GetName() != "Depot" || l.GetAddress() != "Main st" || l.GetDegreesLatitude() != 55.75 {
		t.Errorf("location %+v", l)
	}
	if resp := b.SendPoll(t.Context(), PollRequest{Recipient: opsGroup}); resp.Message != "question is required" {
		t.Errorf("%+v", resp)
	}
	if resp := b.SendLocation(t.Context(), LocationRequest{Recipient: aliceChat, Latitude: 99}); resp.Message != "latitude must be between -90 and 90" {
		t.Errorf("%+v", resp)
	}
}
