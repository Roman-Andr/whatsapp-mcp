package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestPresenceEventsAreKept(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	sub := b.events.Subscribe()
	seen := time.Unix(1_800_000_000, 0)
	b.HandleEvent(&events.Presence{From: types.NewJID("79966764464", types.DefaultUserServer), Unavailable: true, LastSeen: seen})
	info := b.presence.last["79966764464@s.whatsapp.net"]
	if info.Online || !info.LastSeen.Equal(seen) || !info.Known {
		t.Errorf("%+v", info)
	}
	if e := nextEvent(t, sub); e.Type != "presence" || e.State != "offline" {
		t.Errorf("event %+v", e)
	}
}

func presenceOn(d *Deps) { d.Presence = true }

func TestPresenceSubscribesOncePerUser(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newOnlineBridge(t, presenceOn)
		var subscribes atomic.Int32
		release := make(chan struct{})
		b.fake.SubscribePresenceFunc = func(context.Context, types.JID) error {
			subscribes.Add(1)
			<-release // several callers arrive while the first subscribes
			return nil
		}
		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() {
				if _, err := b.Presence(t.Context(), alicePN.String()); err != nil {
					t.Error(err)
				}
			})
		}
		synctest.Wait()
		close(release)
		wg.Wait()
		if n := subscribes.Load(); n != 1 {
			t.Errorf("%d subscribes in flight for one user", n)
		}
		calls := strings.Join(b.fake.Calls(), ",")
		if strings.Count(calls, "SendPresence") != 1 {
			t.Errorf("went online more than once: %s", calls)
		}

		// Later calls answer at once from the log.
		b.HandleEvent(&events.Presence{From: alicePN})
		start := time.Now()
		info, err := b.Presence(t.Context(), alicePN.String())
		if err != nil || !info.Online || time.Since(start) != 0 {
			t.Errorf("%+v %v after %s", info, err, time.Since(start))
		}
	})
}

func TestPresenceRetriesAFailedSubscribe(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		b := newOnlineBridge(t, presenceOn)
		fail := true
		b.fake.SubscribePresenceFunc = func(context.Context, types.JID) error {
			if fail {
				fail = false
				return errors.New("not now")
			}
			return nil
		}
		if _, err := b.Presence(t.Context(), alicePN.String()); err == nil || err.Error() != "failed to subscribe: not now" {
			t.Errorf("first: %v", err)
		}
		info, err := b.Presence(t.Context(), alicePN.String())
		if err != nil || info.JID != aliceChat || info.Known {
			t.Errorf("retry: %+v %v", info, err)
		}
	})
}

func TestPresenceRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		opts []func(*Deps)
		jid  string
		want string
	}{
		{"presence off", nil, aliceChat, "presence is off"},
		{"group", []func(*Deps){presenceOn}, opsGroup, "presence is per person, not per group"},
		{"bad JID", []func(*Deps){presenceOn}, "a.b.c@s.whatsapp.net", "unexpected number of dots"},
		{"hidden chat", []func(*Deps){presenceOn, allowRead(t, opsGroup)}, aliceChat, "not in the read allowlist"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t, c.opts...)
			if _, err := b.Presence(t.Context(), c.jid); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want %q", err, c.want)
			}
		})
	}
	t.Run("offline", func(t *testing.T) {
		t.Parallel()
		b := newTestBridge(t, presenceOn)
		if _, err := b.Presence(t.Context(), aliceChat); err == nil || err.Error() != "not connected to WhatsApp" {
			t.Errorf("err %v", err)
		}
	})
}

func TestContactInfo(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	b.fake.About = map[types.JID]string{alicePN: "plowing"}
	b.fake.Blocked = []types.JID{aliceLID}
	info := b.ContactInfo(t.Context(), ContactInfoRequest{JID: aliceChat})
	if !info.Success || info.Name != "Alice Full" || info.About != "plowing" || !info.Blocked || info.OnWhatsApp == nil || !*info.OnWhatsApp {
		t.Errorf("%+v", info)
	}
	group := b.ContactInfo(t.Context(), ContactInfoRequest{JID: opsGroup})
	if !group.Success || group.JID != opsGroup {
		t.Errorf("group %+v", group)
	}
	if bad := b.ContactInfo(t.Context(), ContactInfoRequest{JID: "a.b.c@s.whatsapp.net"}); bad.Success || !strings.HasPrefix(bad.Message, "Invalid JID") {
		t.Errorf("bad %+v", bad)
	}
	offline := newTestBridge(t).ContactInfo(t.Context(), ContactInfoRequest{JID: aliceChat})
	if offline.Message != "Not connected to WhatsApp" {
		t.Errorf("offline %+v", offline)
	}
}

func TestBlocking(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t, allowRead(t, aliceChat))
	if resp := b.SetBlocked(t.Context(), BlockRequest{JID: aliceChat, Blocked: true}); !resp.Success || resp.Message != "Blocked 2000@s.whatsapp.net" {
		t.Errorf("block %+v", resp)
	}
	b.fake.Blocked = append(b.fake.Blocked, bobPN) // hidden by the read allowlist
	list, status := b.Blocklist(t.Context())
	body, _ := json.Marshal(list)
	if status != 200 || string(body) != `{"blocked":[{"jid":"2000@s.whatsapp.net","name":"Alice Full"}],"success":true}` {
		t.Errorf("blocklist %d %s", status, body)
	}
	if resp := b.SetBlocked(t.Context(), BlockRequest{JID: aliceChat}); !resp.Success || resp.Message != "Unblocked 2000@s.whatsapp.net" {
		t.Errorf("unblock %+v", resp)
	}
	if resp := b.SetBlocked(t.Context(), BlockRequest{JID: opsGroup, Blocked: true}); resp.Success || !strings.HasPrefix(resp.Message, "groups cannot be blocked") {
		t.Errorf("group %+v", resp)
	}
	if _, status := newTestBridge(t).Blocklist(t.Context()); status != 500 {
		t.Errorf("offline status %d", status)
	}
}
