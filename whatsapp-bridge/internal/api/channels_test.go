package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

func TestChannelRoutesValidate(t *testing.T) {
	b, handler := newTestBridge(t)
	b.readAllowlist, _ = policy.NewAllowlist([]string{"120@g.us"})
	for _, c := range []struct{ path, body, want string }{
		{"/api/channel/messages", `{"channel_jid":"123@newsletter"}`, "read allowlist"},
		{"/api/channel/follow", `{"channel":""}`, "channel is required"},
		{"/api/channel/follow", `{"channel":"https://whatsapp.com/channel/abc","follow":true}`, "Not connected"},
	} {
		rec := call(handler, c.path, c.body)
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
}

func TestChannelRoutes(t *testing.T) {
	t.Parallel()
	online := func(f *fakeService) { f.online = true }
	notConnected := `{"message":"Not connected to WhatsApp","success":false}` + "\n"
	runRouteCases(t, []routeCase{
		{name: "channels offline", method: http.MethodGet, path: "/api/channels", code: 500, want: notConnected},
		{name: "channels failure", method: http.MethodGet, path: "/api/channels",
			fake: func(f *fakeService) { f.online, f.err = true, errors.New("iq timed out") },
			code: 500, want: `{"message":"iq timed out","success":false}` + "\n", calls: []string{"Channels"}, timeout: lookupTimeout},
		{name: "channels ok", method: http.MethodGet, path: "/api/channels",
			fake: func(f *fakeService) {
				f.online, f.channels = true, []bridge.Channel{{JID: "1@newsletter", Name: "News", Subscribers: 3}}
			},
			code: 200, want: `{"channels":[{"jid":"1@newsletter","name":"News","subscribers":3,"muted":false,"verified":false}],"success":true}` + "\n",
			calls: []string{"Channels"}, timeout: lookupTimeout},
		{name: "messages unreadable", path: "/api/channel/messages", body: `{"channel_jid":"1@newsletter"}`, fake: unreadable("1@newsletter"),
			code: 403, want: `{"message":"channel 1@newsletter is not in the read allowlist","success":false}` + "\n"},
		{name: "messages offline", path: "/api/channel/messages", body: `{"channel_jid":"1@newsletter"}`, code: 500, want: notConnected},
		{name: "messages failure", path: "/api/channel/messages", body: `{"channel_jid":"1@newsletter","count":5,"before_server_id":9}`,
			fake: func(f *fakeService) { f.online, f.err = true, errors.New("gone") },
			code: 500, want: `{"message":"gone","success":false}` + "\n", calls: []string{"ChannelMessages"},
			check: func(t *testing.T, f *fakeService) {
				t.Helper()
				if req := f.last().req.([]any); req[1] != 5 || req[2] != 9 {
					t.Errorf("request %v", req)
				}
			}},
		{name: "messages ok", path: "/api/channel/messages", body: `{"channel_jid":"1@newsletter"}`, fake: online,
			code: 200, want: `{"messages":null,"success":true}` + "\n", calls: []string{"ChannelMessages"}},
		{name: "messages bad JSON", path: "/api/channel/messages", body: `{`, code: 400, want: "Invalid request format\n"},
		{name: "follow without channel", path: "/api/channel/follow", body: `{"follow":true}`,
			code: 400, want: "channel is required\n",
			audit: []audit.Entry{{Endpoint: "channel_follow", Text: "true", Result: "channel is required"}}},
		{name: "follow ok", path: "/api/channel/follow", body: `{"channel":"1@newsletter","follow":true}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"FollowChannel"},
			audit: []audit.Entry{{Endpoint: "channel_follow", Chat: "1@newsletter", Text: "true", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "follow read-only", path: "/api/channel/follow", body: `{}`, deps: readOnly,
			code: 403, want: readOnlyBody, audit: []audit.Entry{{Endpoint: "channel_follow", Result: msgReadOnly}}},
	})
}
