package api

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

func TestContactRoutesValidate(t *testing.T) {
	b, handler := newTestBridge(t, "79966764464")
	b.readAllowlist, _ = policy.NewAllowlist([]string{"79966764464"})
	for _, c := range []struct{ path, body, want string }{
		{"/api/contact/info", `{"jid":"5550001"}`, "read allowlist"},
		{"/api/contact/info", `{"jid":"79966764464"}`, "Not connected"},
		{"/api/block", `{"jid":"120@g.us","blocked":true}`, "groups cannot be blocked"},
		{"/api/block", `{"jid":"5550001","blocked":true}`, "send allowlist"},
		{"/api/presence", `{"jid":"79966764464"}`, "WHATSAPP_PRESENCE"},
	} {
		rec := call(handler, c.path, c.body)
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
	b.presenceEnabled = true
	if rec := call(handler, "/api/presence", `{"jid":"5550001"}`); !strings.Contains(rec.Body.String(), "read allowlist") {
		t.Errorf("presence outside read allowlist: %s", rec.Body)
	}
}

func TestContactRoutes(t *testing.T) {
	t.Parallel()
	runRouteCases(t, []routeCase{
		{name: "info refused is 500", path: "/api/contact/info", body: `{"jid":"5"}`,
			fake: func(f *fakeService) { f.contact = bridge.ContactInfo{Message: "5 is not in the read allowlist"} },
			code: 500, want: `{"success":false,"message":"5 is not in the read allowlist","blocked":false}` + "\n",
			calls: []string{"ContactInfo"}, timeout: lookupTimeout},
		{name: "info ok", path: "/api/contact/info", body: `{"jid":"5"}`,
			fake: func(f *fakeService) {
				f.contact = bridge.ContactInfo{Success: true, JID: "5@s.whatsapp.net", Name: "Eve"}
			},
			code: 200, want: `{"success":true,"jid":"5@s.whatsapp.net","name":"Eve","blocked":false}` + "\n",
			calls: []string{"ContactInfo"}, timeout: lookupTimeout},
		{name: "info GET", method: http.MethodGet, path: "/api/contact/info", code: 405, want: "Method not allowed\n"},
		{name: "blocklist answers any method", method: http.MethodGet, path: "/api/blocklist",
			fake: func(f *fakeService) {
				f.body, f.bodyStatus = map[string]any{"success": true, "blocked": []string{"5"}}, 200
			},
			code: 200, want: `{"blocked":["5"],"success":true}` + "\n", calls: []string{"Blocklist"}, timeout: lookupTimeout},
		{name: "block without jid", path: "/api/block", body: `{"blocked":true}`,
			code: 400, want: "jid is required\n",
			audit: []audit.Entry{{Endpoint: "block", Text: "true", Result: "jid is required"}}},
		{name: "block ok", path: "/api/block", body: `{"jid":"5","blocked":false}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"SetBlocked"},
			audit: []audit.Entry{{Endpoint: "block", Chat: "5", Text: "false", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "block read-only", path: "/api/block", body: `{"jid":"5"}`, deps: readOnly,
			code: 403, want: readOnlyBody, audit: []audit.Entry{{Endpoint: "block", Result: msgReadOnly}}},
		{name: "presence refused is 400", path: "/api/presence", body: `{"jid":"5"}`,
			fake: func(f *fakeService) { f.err = errors.New("5 is not in the read allowlist") },
			code: 400, want: `{"message":"5 is not in the read allowlist","success":false}` + "\n", calls: []string{"Presence"}},
		{name: "presence ok", path: "/api/presence", body: `{"jid":"5"}`,
			fake: func(f *fakeService) {
				f.presence = bridge.PresenceInfo{JID: "5@s.whatsapp.net", Online: true, Known: true, Updated: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
			},
			code: 200, want: `{"presence":{"jid":"5@s.whatsapp.net","online":true,"updated":"2026-01-02T03:04:05Z","known":true},"success":true}` + "\n",
			calls: []string{"Presence"}},
		{name: "presence bad JSON", path: "/api/presence", body: `{`, code: 400, want: "Invalid request format\n"},
	})
}
