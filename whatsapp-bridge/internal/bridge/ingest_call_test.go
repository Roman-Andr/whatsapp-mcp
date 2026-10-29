package bridge

import (
	"strings"
	"testing"
	"time"

	waBinary "go.mau.fi/whatsmeow/binary"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

func TestCallsBecomeOneLineInTheChat(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	sub := b.events.Subscribe()
	caller := types.NewJID("c", types.DefaultUserServer)
	meta := func(id string) types.BasicCallMeta {
		return types.BasicCallMeta{From: caller, CallCreator: caller, CallID: id, Timestamp: time.Now()}
	}
	video := &waBinary.Node{Tag: "offer", Content: []waBinary.Node{{Tag: "video"}}}
	steps := []struct {
		name, id string
		evt      any
		want     string // stored text; "" for none
		state    string // published state
	}{
		{"video call rings", "X1", &events.CallOffer{BasicCallMeta: meta("X1"), Data: video}, "[call] incoming video call", "ringing"},
		{"nobody answers", "X1", &events.CallTerminate{BasicCallMeta: meta("X1"), Reason: "timeout"}, "[call] missed video call", "missed"},
		{"voice call rings", "X2", &events.CallOffer{BasicCallMeta: meta("X2")}, "[call] incoming voice call", "ringing"},
		{"answered", "X2", &events.CallAccept{BasicCallMeta: meta("X2")}, "[call] voice call, answered", "answered"},
		{"hung up", "X2", &events.CallTerminate{BasicCallMeta: meta("X2")}, "[call] voice call, ended (hung up)", "hung up"},
		{"group call notice", "X4", &events.CallOfferNotice{BasicCallMeta: meta("X4"), Media: "video", Type: "group"}, "[call] incoming group video call", "ringing"},
		// An end without a known offer (bridge restarted mid-call) adds nothing.
		{"orphan end", "X3", &events.CallTerminate{BasicCallMeta: meta("X3")}, "", ""},
	}
	for _, s := range steps {
		b.HandleEvent(s.evt)
		got := queryString(t, b, "SELECT content FROM messages WHERE id = ?", "call-"+s.id)
		if got != s.want {
			t.Errorf("%s: stored %q, want %q", s.name, got, s.want)
		}
		if s.state == "" {
			noEvent(t, sub)
			continue
		}
		if e := nextEvent(t, sub); e.Type != "call" || e.State != s.state {
			t.Errorf("%s: event %+v", s.name, e)
		}
	}
	if !strings.Contains(b.logs.String(), `msg=call`) {
		t.Errorf("calls not logged:\n%s", b.logs.String())
	}
}

func TestRejectedCalls(t *testing.T) {
	t.Parallel()
	reject := func(msg string) func(*Deps) {
		return func(d *Deps) { d.RejectCalls = CallRejection{Enabled: true, Message: msg} }
	}
	readOnly := func(d *Deps) { d.ReadOnly = true }
	exhausted := func(d *Deps) {
		d.Limiter = policy.NewRateLimiter(1, 1)
		d.Limiter.Take()
	}
	cases := []struct {
		name   string
		opts   []func(*Deps)
		sent   int
		result string // audited result; "" for no audit line
	}{
		{"declined silently", []func(*Deps){reject("")}, 0, ""},
		{"declined with a reply", []func(*Deps){reject("Please write instead.")}, 1, "Message sent to 2000@s.whatsapp.net"},
		{"read-only skips the reply", []func(*Deps){reject("Please write."), readOnly}, 0, "skipped: read-only mode"},
		{"rate limit skips the reply", []func(*Deps){reject("Please write."), exhausted}, 0, "skipped: send rate limit reached"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t, c.opts...)
			b.HandleEvent(&events.CallOffer{BasicCallMeta: types.BasicCallMeta{From: alicePN, CallID: "R1", Timestamp: time.Now()}})
			if got := queryString(t, b, "SELECT content FROM messages WHERE id = 'call-R1'"); got != "[call] voice call, rejected automatically" {
				t.Errorf("stored %q", got)
			}
			if n := len(b.fake.Sent()); n != c.sent {
				t.Errorf("%d replies sent", n)
			}
			if !strings.Contains(strings.Join(b.fake.Calls(), ","), "RejectCall") {
				t.Error("call not rejected")
			}
			audited := auditLines(t, b)
			if c.result == "" {
				if audited != "" {
					t.Errorf("audited %s", audited)
				}
			} else if !strings.Contains(audited, `"endpoint":"call_reply"`) || !strings.Contains(audited, c.result) {
				t.Errorf("audit %s, want %q", audited, c.result)
			}
		})
	}
}

func TestCallRejecterConfig(t *testing.T) {
	t.Parallel()
	if newCallRejecter(CallRejection{Message: "ignored"}) != nil {
		t.Error("rejecting without WHATSAPP_REJECT_CALLS")
	}
	if r := newCallRejecter(CallRejection{Enabled: true, Message: "I can't take calls, please write."}); r == nil || r.message != "I can't take calls, please write." {
		t.Errorf("%+v", r)
	}
}
