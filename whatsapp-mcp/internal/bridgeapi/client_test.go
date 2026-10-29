package bridgeapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
)

func TestEndpointsPostTheirPayloads(t *testing.T) {
	t.Parallel()
	eight, yes, empty := 8.0, true, ""
	cases := []struct {
		name     string
		call     func(ctx context.Context, c *Client) any
		endpoint string
		payload  string
	}{
		{"send message", func(ctx context.Context, c *Client) any {
			return c.SendMessage(ctx, group, "hi @18046727669", "A1", []string{"18046727669"})
		}, "send", `{"recipient":"` + group + `","message":"hi @18046727669","reply_to_message_id":"A1","mentions":["18046727669"]}`},
		{"react", func(ctx context.Context, c *Client) any { return c.SendReaction(ctx, group, "A1", "👍") },
			"react", `{"chat_jid":"` + group + `","message_id":"A1","emoji":"👍"}`},
		{"poll", func(ctx context.Context, c *Client) any {
			return c.SendPoll(ctx, group, "Depot?", []string{"North", "South"}, 1)
		}, "poll", `{"recipient":"` + group + `","question":"Depot?","options":["North","South"],"selectable_count":1}`},
		{"poll without options", func(ctx context.Context, c *Client) any { return c.SendPoll(ctx, group, "Depot?", nil, 1) },
			"poll", `{"recipient":"` + group + `","question":"Depot?","options":[],"selectable_count":1}`},
		{"location", func(ctx context.Context, c *Client) any { return c.SendLocation(ctx, group, 55.75, 37.62, "Depot", "") },
			"location", `{"recipient":"` + group + `","latitude":55.75,"longitude":37.62,"name":"Depot"}`},
		{"location with address", func(ctx context.Context, c *Client) any { return c.SendLocation(ctx, group, 1, 2, "", "Main St") },
			"location", `{"recipient":"` + group + `","latitude":1.0,"longitude":2.0,"address":"Main St"}`},
		{"edit", func(ctx context.Context, c *Client) any { return c.EditMessage(ctx, group, "A2", "fixed text") },
			"edit", `{"chat_jid":"` + group + `","message_id":"A2","message":"fixed text"}`},
		{"delete", func(ctx context.Context, c *Client) any { return c.DeleteMessage(ctx, group, "A2") },
			"delete", `{"chat_jid":"` + group + `","message_id":"A2"}`},
		{"vote", func(ctx context.Context, c *Client) any { return c.VotePoll(ctx, group, "P1", []string{"North"}) },
			"poll/vote", `{"chat_jid":"` + group + `","message_id":"P1","options":["North"]}`},
		{"contact", func(ctx context.Context, c *Client) any { return c.SendContact(ctx, group, "Ann", "+1", "") },
			"contact", `{"recipient":"` + group + `","name":"Ann","phone":"+1"}`},
		{"contact with vcard", func(ctx context.Context, c *Client) any { return c.SendContact(ctx, group, "Ann", "+1", "BEGIN:VCARD") },
			"contact", `{"recipient":"` + group + `","name":"Ann","phone":"+1","vcard":"BEGIN:VCARD"}`},
		{"forward", func(ctx context.Context, c *Client) any {
			return c.ForwardMessage(ctx, group, "A1", "5@s.whatsapp.net")
		},
			"forward", `{"chat_jid":"` + group + `","message_id":"A1","recipient":"5@s.whatsapp.net"}`},
		{"star", func(ctx context.Context, c *Client) any { return c.StarMessage(ctx, group, "A1", true) },
			"star", `{"chat_jid":"` + group + `","message_id":"A1","starred":true}`},
		{"block", func(ctx context.Context, c *Client) any { return c.BlockContact(ctx, "5@s.whatsapp.net", false) },
			"block", `{"jid":"5@s.whatsapp.net","blocked":false}`},
		{"follow", func(ctx context.Context, c *Client) any { return c.FollowChannel(ctx, "1@newsletter", true) },
			"channel/follow", `{"channel":"1@newsletter","follow":true}`},
		{"read chat", func(ctx context.Context, c *Client) any { return c.MarkRead(ctx, group, nil) },
			"read", `{"chat_jid":"` + group + `"}`},
		{"read messages", func(ctx context.Context, c *Client) any { return c.MarkRead(ctx, group, []string{"A1"}) },
			"read", `{"chat_jid":"` + group + `","message_ids":["A1"]}`},
		{"typing", func(ctx context.Context, c *Client) any { return c.SendTyping(ctx, group, "paused") },
			"typing", `{"chat_jid":"` + group + `","state":"paused"}`},
		{"mute", func(ctx context.Context, c *Client) any { return c.ManageChat(ctx, group, "mute", &eight) },
			"chat", `{"chat_jid":"` + group + `","action":"mute","mute_hours":8.0}`},
		{"archive", func(ctx context.Context, c *Client) any { return c.ManageChat(ctx, group, "archive", nil) },
			"chat", `{"chat_jid":"` + group + `","action":"archive"}`},
		{"label chat", func(ctx context.Context, c *Client) any { return c.LabelChat(ctx, group, "Paid", false) },
			"label/chat", `{"chat_jid":"` + group + `","label":"Paid","labeled":false}`},
		{"new label", func(ctx context.Context, c *Client) any { return c.EditLabel(ctx, "Paid", "", 2, false) },
			"label/edit", `{"name":"Paid","color":2,"deleted":false}`},
		{"edit label", func(ctx context.Context, c *Client) any { return c.EditLabel(ctx, "Paid", "3", 2, true) },
			"label/edit", `{"name":"Paid","color":2,"deleted":true,"label_id":"3"}`},
		{"create group", func(ctx context.Context, c *Client) any {
			return c.CreateGroup(ctx, "Crew", []string{"79966764464"}, false)
		}, "group/create", `{"name":"Crew","participants":["79966764464"]}`},
		{"create community", func(ctx context.Context, c *Client) any { return c.CreateGroup(ctx, "Crews", nil, true) },
			"group/create", `{"name":"Crews","participants":[],"community":true}`},
		{"leave", func(ctx context.Context, c *Client) any { return c.LeaveGroup(ctx, group) },
			"group/leave", `{"chat_jid":"` + group + `"}`},
		{"participants", func(ctx context.Context, c *Client) any {
			return c.UpdateGroupParticipants(ctx, group, "promote", []string{"18046727669"})
		}, "group/participants", `{"chat_jid":"` + group + `","action":"promote","participants":["18046727669"]}`},
		{"settings", func(ctx context.Context, c *Client) any {
			return c.UpdateGroupSettings(ctx, group, GroupSettings{Description: &empty, AnnounceOnly: &yes})
		}, "group/settings", `{"chat_jid":"` + group + `","description":"","announce_only":true}`},
		{"every setting", func(ctx context.Context, c *Client) any {
			name := "Crew"
			return c.UpdateGroupSettings(ctx, group, GroupSettings{Name: &name, Locked: &yes, JoinApproval: &yes})
		}, "group/settings", `{"chat_jid":"` + group + `","name":"Crew","locked":true,"join_approval":true}`},
		{"join", func(ctx context.Context, c *Client) any { return c.JoinGroup(ctx, "https://chat.whatsapp.com/AbC") },
			"group/join", `{"invite":"https://chat.whatsapp.com/AbC"}`},
		{"join requests", func(ctx context.Context, c *Client) any { return c.GroupJoinRequests(ctx, group) },
			"group/requests", `{"chat_jid":"` + group + `"}`},
		{"approve requests", func(ctx context.Context, c *Client) any {
			return c.UpdateGroupJoinRequests(ctx, group, "approve", []string{"5@lid"})
		}, "group/requests/update", `{"chat_jid":"` + group + `","action":"approve","participants":["5@lid"]}`},
		{"reset invite", func(ctx context.Context, c *Client) any { return c.ResetGroupInviteLink(ctx, group) },
			"group/invite/reset", `{"chat_jid":"` + group + `"}`},
		{"disappearing", func(ctx context.Context, c *Client) any { return c.SetDisappearingMessages(ctx, group, "7d") },
			"chat/disappearing", `{"chat_jid":"` + group + `","timer":"7d"}`},
		{"link", func(ctx context.Context, c *Client) any { return c.LinkCommunityGroup(ctx, "1@g.us", group, false) },
			"community/link", `{"community_jid":"1@g.us","group_jid":"` + group + `"}`},
		{"unlink", func(ctx context.Context, c *Client) any { return c.LinkCommunityGroup(ctx, "1@g.us", group, true) },
			"community/link", `{"community_jid":"1@g.us","group_jid":"` + group + `","unlink":true}`},
		{"community groups", func(ctx context.Context, c *Client) any { return c.CommunityGroups(ctx, "1@g.us") },
			"community/groups", `{"community_jid":"1@g.us"}`},
		{"contact info", func(ctx context.Context, c *Client) any { return c.ContactInfo(ctx, "5@s.whatsapp.net", true) },
			"contact/info", `{"jid":"5@s.whatsapp.net","picture":true}`},
		{"presence", func(ctx context.Context, c *Client) any { return c.Presence(ctx, "5@s.whatsapp.net") },
			"presence", `{"jid":"5@s.whatsapp.net"}`},
		{"channel messages", func(ctx context.Context, c *Client) any { return c.ChannelMessages(ctx, "1@newsletter", 10, 0) },
			"channel/messages", `{"channel_jid":"1@newsletter","count":10,"before_server_id":0}`},
		{"group info", func(ctx context.Context, c *Client) any { return c.GroupInfo(ctx, group, true) },
			"group", `{"chat_jid":"` + group + `","invite_link":true}`},
		{"history", func(ctx context.Context, c *Client) any { return c.History(ctx, group, 50, "") },
			"history", `{"chat_jid":"` + group + `","count":50}`},
		{"history before", func(ctx context.Context, c *Client) any { return c.History(ctx, group, 50, "A1") },
			"history", `{"chat_jid":"` + group + `","count":50,"before_message_id":"A1"}`},
		{"download", func(ctx context.Context, c *Client) any { return c.Download(ctx, "A1", group) },
			"download", `{"message_id":"A1","chat_jid":"` + group + `"}`},
		{"sticker", func(ctx context.Context, c *Client) any {
			return c.Sticker(ctx, pyfmt.NewOMap("recipient", group, "media_path", "/s.webp"))
		}, "sticker", `{"recipient":"` + group + `","media_path":"/s.webp"}`},
		{"reconnect", func(ctx context.Context, c *Client) any { return c.ReconnectBridge(ctx) },
			"connection/reconnect", `{}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newBridge(t)
			tc.call(t.Context(), b.client(t, ""))
			calls := b.calls()
			if len(calls) != 1 {
				t.Fatalf("%d calls", len(calls))
			}
			if calls[0].Endpoint != tc.endpoint {
				t.Errorf("endpoint %q, want %q", calls[0].Endpoint, tc.endpoint)
			}
			if got, want := jsonOf(t, calls[0].Payload), jsonOf(t, tc.payload); got != want {
				t.Errorf("payload %s, want %s", got, want)
			}
		})
	}
}

func TestMissingFieldsAreRefusedWithoutACall(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		call func(ctx context.Context, c *Client) Outcome
		want string
	}{
		{"message without recipient", func(ctx context.Context, c *Client) Outcome { return c.SendMessage(ctx, "", "hi", "", nil) },
			"Recipient must be provided"},
		{"empty message", func(ctx context.Context, c *Client) Outcome { return c.SendMessage(ctx, group, "", "", nil) },
			"Message must be provided"},
		{"reaction without message", func(ctx context.Context, c *Client) Outcome { return c.SendReaction(ctx, group, "", "x") },
			"chat_jid and message_id must be provided"},
		{"poll without recipient", func(ctx context.Context, c *Client) Outcome { return c.SendPoll(ctx, "", "q", nil, 1) },
			"Recipient must be provided"},
		{"location without recipient", func(ctx context.Context, c *Client) Outcome { return c.SendLocation(ctx, "", 1, 2, "", "") },
			"Recipient must be provided"},
		{"edit without chat", func(ctx context.Context, c *Client) Outcome { return c.EditMessage(ctx, "", "A1", "x") },
			"chat_jid and message_id must be provided"},
		{"empty edit", func(ctx context.Context, c *Client) Outcome { return c.EditMessage(ctx, group, "A2", "") },
			"The new text must not be empty (use delete_message to remove a message)"},
		{"delete without message", func(ctx context.Context, c *Client) Outcome { return c.DeleteMessage(ctx, group, "") },
			"chat_jid and message_id must be provided"},
		{"read without chat", func(ctx context.Context, c *Client) Outcome { return c.MarkRead(ctx, "", nil) },
			"chat_jid must be provided"},
		{"typing without chat", func(ctx context.Context, c *Client) Outcome { return c.SendTyping(ctx, "", "paused") },
			"chat_jid must be provided"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newBridge(t)
			if got, want := tc.call(t.Context(), b.client(t, "")), Fail(tc.want); !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
			if n := len(b.calls()); n != 0 {
				t.Errorf("%d calls", n)
			}
		})
	}
}

func TestPostSendsTheToken(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "api_token"), []byte("tok123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := newBridge(t)
	o := b.client(t, dir).SendMessage(t.Context(), group, "hi", "", nil)
	if want := (Outcome{true, "ok", "NEW"}); o != want {
		t.Errorf("got %+v, want %+v", o, want)
	}
	if got := b.calls()[0].Auth; got != "Bearer tok123" {
		t.Errorf("Authorization %q", got)
	}
}

func TestAnswersBecomeOutcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
		want   Outcome
	}{
		{"success", 200, `{"success": true, "message": "ok", "message_id": "NEW"}`, Outcome{true, "ok", "NEW"}},
		{"rejected token", 401, "Unauthorized",
			Fail("Bridge rejected the API token (check store/api_token or WHATSAPP_BRIDGE_TOKEN)")},
		{"refusal", 403, `{"success": false, "message": "chat 120@g.us is not in the send allowlist"}`,
			Fail("chat 120@g.us is not in the send allowlist")},
		{"error without success", 400, `{"message": "too late"}`, Fail("too late")},
		{"no message", 200, `{"success": true}`, Outcome{true, "Unknown response", nil}},
		{"not JSON", 502, "Bad Gateway\n", Fail("Error: HTTP 502 - Bad Gateway")},
		{"not an object", 200, `[1]`, Fail("Error: HTTP 200 - [1]")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newBridge(t)
			b.answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}
			if got := b.client(t, "").Send(t.Context(), pyfmt.NewOMap()); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRequestFailures(t *testing.T) {
	t.Parallel()
	t.Run("timeout", func(t *testing.T) {
		t.Parallel()
		b := newBridge(t)
		release := make(chan struct{})
		b.answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) { <-release }
		t.Cleanup(func() { close(release) })
		r := b.client(t, "").post(t.Context(), "history", pyfmt.NewOMap("chat_jid", group), 50*time.Millisecond)
		if got, want := pyfmt.Compact(r), `{"success": false, "message": "Bridge did not answer within 0s"}`; got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	})
	t.Run("unreachable", func(t *testing.T) {
		t.Parallel()
		c := &Client{BaseURL: "http://127.0.0.1:1/api", HTTP: NewHTTPClient()}
		o := c.Send(t.Context(), pyfmt.NewOMap())
		const prefix = "Request error (is the bridge running?): "
		if o.OK || len(o.Message) <= len(prefix) || o.Message[:len(prefix)] != prefix {
			t.Errorf("got %+v", o)
		}
		r := c.Channels(t.Context())
		if msg := pyfmt.AsString(r.Get("message")); r.Get("success") != false || msg[:len(prefix)] != prefix {
			t.Errorf("got %s", pyfmt.Compact(r))
		}
	})
	t.Run("cancelled call", func(t *testing.T) {
		t.Parallel()
		b := newBridge(t)
		arrived, ended := make(chan struct{}), make(chan struct{})
		b.answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			close(arrived)
			<-r.Context().Done()
			close(ended)
		}
		ctx, cancel := context.WithCancel(t.Context())
		go func() { <-arrived; cancel() }()
		if o := b.client(t, "").Download(ctx, "A1", group); o.Get("success") != false {
			t.Errorf("got %s", pyfmt.Compact(o))
		}
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatal("the bridge request was not cancelled with the call")
		}
	})
}

func TestGetAnswers(t *testing.T) {
	t.Parallel()
	b := newBridge(t)
	b.answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		switch r.URL.Path {
		case "/api/blocklist":
			w.Write([]byte(`{"success": true, "blocked": [{"jid": "5@s.whatsapp.net"}]}`))
		default:
			w.Write([]byte(`["not an object"]`))
		}
	}
	c := b.client(t, "")
	if got, want := pyfmt.Compact(c.Blocklist(t.Context())), `{"success": true, "blocked": [{"jid": "5@s.whatsapp.net"}]}`; got != want {
		t.Errorf("blocklist %s, want %s", got, want)
	}
	want := `{"success": false, "message": "Request error (is the bridge running?): unexpected answer: [\"not an object\"]"}`
	if got := pyfmt.Compact(c.Channels(t.Context())); got != want {
		t.Errorf("channels %s, want %s", got, want)
	}
}

func TestStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"connected", 200, `{"connected": true, "session": {"state": "connected", "needs_action": false}}`,
			`{"connected": true, "session": {"state": "connected", "needs_action": false}}`},
		{"session lost", 200, `{"connected": false, "session": {"state": "logged_out", "reason": "scan the QR code", "needs_action": true}}`,
			`{"connected": false, "session": {"state": "logged_out", "reason": "scan the QR code", "needs_action": true}, ` +
				`"warning": "WhatsApp session logged_out: scan the QR code. No messages are received or sent until this is fixed."}`},
		{"session lost without reason", 200, `{"connected": false, "session": {"state": "replaced", "needs_action": true}}`,
			`{"connected": false, "session": {"state": "replaced", "needs_action": true}, ` +
				`"warning": "WhatsApp session replaced: . No messages are received or sent until this is fixed."}`},
		{"error status", 500, "boom\n", `{"connected": false, "error": "HTTP 500 - boom"}`},
		{"not JSON", 200, "nope", `{"connected": false, "error": "HTTP 200 - nope"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newBridge(t)
			b.answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}
			if got := pyfmt.Compact(b.client(t, "").Status(t.Context())); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
	t.Run("unreachable", func(t *testing.T) {
		t.Parallel()
		c := &Client{BaseURL: "http://127.0.0.1:1/api", HTTP: NewHTTPClient()}
		s := c.Status(t.Context())
		const prefix = "Bridge not reachable: "
		if msg := pyfmt.AsString(s.Get("error")); s.Get("connected") != false || len(msg) < len(prefix) || msg[:len(prefix)] != prefix {
			t.Errorf("got %s", pyfmt.Compact(s))
		}
	})
}

func TestReconnectBridgeReportsRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		status int
		body   string
		want   Outcome
	}{
		{"reconnected", 200, `{"success": true, "message": "Reconnected to WhatsApp", "message_id": "X"}`,
			Outcome{true, "Reconnected to WhatsApp", nil}},
		{"too soon", 429, `{"success": false, "message": "reconnected less than 30s ago; try again in 20s"}`,
			Fail("reconnected less than 30s ago; try again in 20s")},
		{"not linked", 409, `{"success": false, "message": "not linked to WhatsApp: pair the device first"}`,
			Fail("not linked to WhatsApp: pair the device first")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newBridge(t)
			b.answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			}
			if got := b.client(t, "").ReconnectBridge(t.Context()); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		chat   string
		filter Filter
		lines  []string
		want   []any
	}{
		{"session events pass the chat filter", group, nil, []string{
			`data: {"type": "message", "chat_jid": "other@g.us"}`, `data: {"type": "session", "state": "logged_out"}`,
			`data: {"type": "message", "chat_jid": "` + group + `"}`,
		}, []any{"session", "message"}},
		{"comments and other fields are skipped", "", nil, []string{
			": keepalive", "event: message", `data: {"type": "message", "chat_jid": "a"}`, `data: [1]`,
		}, []any{"message"}},
		{"the read allowlist applies", "", func(context.Context) (func(string) bool, error) {
			return func(chat string) bool { return chat == group }, nil
		}, []string{
			`data: {"type": "message", "chat_jid": "other@g.us"}`, `data: {"type": "session"}`,
			`data: {"type": "reaction", "chat_jid": "` + group + `"}`,
		}, []any{"session", "reaction"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				c := scriptClient(scripted(0, tc.lines...)...)
				var got []any
				err := c.Events(t.Context(), time.Minute, tc.chat, tc.filter, func(ev *pyfmt.OMap) bool {
					got = append(got, ev.Get("type"))
					return true
				})
				if err != nil || !reflect.DeepEqual(got, tc.want) {
					t.Errorf("got %v %v, want %v", got, err, tc.want)
				}
			})
		})
	}
}

func TestEventsEnd(t *testing.T) {
	t.Parallel()
	t.Run("when fn says so", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			c := scriptClient(scripted(0, `data: {"chat_jid": "a"}`, `data: {"chat_jid": "b"}`)...)
			var got []any
			err := c.Events(t.Context(), time.Minute, "", nil, func(ev *pyfmt.OMap) bool {
				got = append(got, ev.Get("chat_jid"))
				return false
			})
			if err != nil || !reflect.DeepEqual(got, []any{"a"}) {
				t.Errorf("got %v %v", got, err)
			}
		})
	})
	t.Run("after the read timeout", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			if err := silentClient().Events(t.Context(), time.Hour, "", nil, nil); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d != 30*time.Second {
				t.Errorf("ended after %v", d)
			}
		})
	})
	t.Run("on an undecodable event", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			err := scriptClient(scripted(0, "data: {nope")...).Events(t.Context(), time.Minute, "", nil, nil)
			if err == nil {
				t.Error("no error")
			}
		})
	})
	t.Run("with the filter's error", func(t *testing.T) {
		t.Parallel()
		bad := errors.New("bad allowlist")
		err := scriptClient().Events(t.Context(), time.Minute, "", func(context.Context) (func(string) bool, error) { return nil, bad }, nil)
		if !errors.Is(err, bad) {
			t.Errorf("got %v", err)
		}
	})
	t.Run("with the HTTP status", func(t *testing.T) {
		t.Parallel()
		b := newBridge(t)
		b.answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("no\n"))
		}
		err := b.client(t, "").Events(t.Context(), time.Minute, "", nil, nil)
		if want := "403 Forbidden for url: " + b.srv.URL + "/api/events (no)"; err == nil || err.Error() != want {
			t.Errorf("got %v, want %s", err, want)
		}
	})
}

func TestWaitForEvents(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		timeout   int
		maxEvents int
		events    []scriptedLine
		waited    int
		got       []any
	}{
		{"returns two seconds after the last event, 4.5 s rounded to even", 30, 20,
			append(scripted(time.Second, `data: {"id": "X1"}`), scripted(2500*time.Millisecond, `data: {"id": "X2"}`)...), 4, []any{"X1", "X2"}},
		{"returns at the timeout without events", 3, 20, nil, 3, []any{}},
		{"a timeout under one second is one second", 0, 20, nil, 1, []any{}},
		{"a timeout over ten minutes is ten minutes", 3600, 20, nil, 600, []any{}},
		{"returns once max_events arrived", 30, 2,
			scripted(time.Second, `data: {"id": "X1"}`, `data: {"id": "X2"}`, `data: {"id": "X3"}`), 1, []any{"X1", "X2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				r := scriptClient(tc.events...).WaitForEvents(t.Context(), tc.timeout, "", tc.maxEvents, nil)
				var ids []any
				events, _ := r.Get("events").([]any)
				for _, e := range events {
					ids = append(ids, e.(*pyfmt.OMap).Get("id"))
				}
				if ids == nil {
					ids = []any{}
				}
				if r.Get("success") != true || r.Get("waited_seconds") != tc.waited || !reflect.DeepEqual(ids, tc.got) {
					t.Errorf("got %s, want %d s and %v", pyfmt.Compact(r), tc.waited, tc.got)
				}
			})
		})
	}
	t.Run("reports a failed stream", func(t *testing.T) {
		t.Parallel()
		c := &Client{BaseURL: "http://127.0.0.1:1/api", HTTP: NewHTTPClient()}
		r := c.WaitForEvents(t.Context(), 1, "", 20, nil)
		const prefix = "Event stream failed: "
		if msg := pyfmt.AsString(r.Get("message")); r.Get("success") != false || len(msg) < len(prefix) || msg[:len(prefix)] != prefix {
			t.Errorf("got %s", pyfmt.Compact(r))
		}
	})
}
