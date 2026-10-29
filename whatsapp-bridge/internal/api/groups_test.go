package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

func TestGroupManagementValidatesBeforeCallingWhatsApp(t *testing.T) {
	b, handler := newTestBridge(t, "120@g.us", "79966764464")
	cases := []struct {
		path, body string
		code       int
		want       string
	}{
		{"/api/group/create", `{"name":"","participants":["79966764464"]}`, 500, "name is required"},
		{"/api/group/create", `{"name":"A name that is far too long for WhatsApp","participants":["79966764464"]}`, 500, "allows 25"},
		{"/api/group/create", `{"name":"Crew","participants":["5550001"]}`, 403, "send allowlist"},
		{"/api/group/create", `{"name":"Crew","participants":[]}`, 500, "participants are required"},
		{"/api/group/leave", `{"chat_jid":"999@g.us"}`, 403, "send allowlist"},
		{"/api/group/leave", `{"chat_jid":"79966764464@s.whatsapp.net"}`, 500, "not a group"},
		{"/api/group/participants", `{"chat_jid":"120@g.us","action":"ban","participants":["1"]}`, 500, "unknown action"},
		{"/api/group/participants", `{"chat_jid":"120@g.us","action":"add","participants":["5550001"]}`, 403, "send allowlist"},
		{"/api/group/settings", `{"chat_jid":"120@g.us"}`, 500, "nothing to change"},
		{"/api/group/settings", `{"chat_jid":"120@g.us","name":""}`, 500, "1 to 25"},
		{"/api/group/join", `{"invite":"https://example.com/a b"}`, 500, "invite must be"},
	}
	for _, c := range cases {
		rec := call(handler, c.path, c.body)
		if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
	if n := len(readAudit(t)); n != len(cases) {
		t.Errorf("%d audit entries for %d calls", n, len(cases))
	}
	b.readOnly = true
	if rec := call(handler, "/api/group/leave", `{"chat_jid":"120@g.us"}`); rec.Code != http.StatusForbidden {
		t.Errorf("read-only leave: %d", rec.Code)
	}
}

func TestGroupExtrasValidate(t *testing.T) {
	b, handler := newTestBridge(t, "120@g.us")
	b.readAllowlist, _ = policy.NewAllowlist([]string{"120@g.us"})
	for _, c := range []struct{ path, body, want string }{
		{"/api/group/requests", `{"chat_jid":"999@g.us"}`, "read allowlist"},
		{"/api/group/requests", `{"chat_jid":"120@g.us"}`, "Not connected"},
		{"/api/group/requests/update", `{"chat_jid":"120@g.us","action":"maybe","participants":["1"]}`, "approve or reject"},
		{"/api/group/requests/update", `{"chat_jid":"999@g.us","action":"approve","participants":["1"]}`, "send allowlist"},
		{"/api/group/invite/reset", `{"chat_jid":"120@g.us"}`, "Not connected"},
		{"/api/chat/disappearing", `{"chat_jid":"120@g.us","timer":"3d"}`, "off, 24h, 7d or 90d"},
		{"/api/chat/disappearing", `{"chat_jid":"999@g.us","timer":"7d"}`, "send allowlist"},
		{"/api/community/link", `{"community_jid":"120@g.us","group_jid":"999@g.us"}`, "send allowlist"},
		{"/api/community/groups", `{"community_jid":"999@g.us"}`, "read allowlist"},
		{"/api/group/create", `{"name":"Crews","community":true}`, "Not connected"},
	} {
		rec := call(handler, c.path, c.body)
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
}

func TestGroupRoutes(t *testing.T) {
	t.Parallel()
	changed := func(f *fakeService) {
		f.group = bridge.GroupChangeResponse{Success: true, Message: "done", Participants: []bridge.ParticipantResult{{JID: "1@s.whatsapp.net", Success: true}}}
	}
	changedBody := `{"success":true,"message":"done","participants":[{"jid":"1@s.whatsapp.net","success":true}]}` + "\n"
	groupRefused := func(f *fakeService) { f.group = bridge.GroupChangeResponse{Message: "not allowed", Refused: true} }
	groupFailed := func(f *fakeService) { f.group = bridge.GroupChangeResponse{Message: "boom"} }
	body := func(f *fakeService) {
		f.body, f.bodyStatus = map[string]any{"success": true, "requests": []string{}}, 200
	}
	runRouteCases(t, []routeCase{
		// group info: read only, not audited
		{name: "info GET", method: http.MethodGet, path: "/api/group", code: 405, want: "Method not allowed\n"},
		{name: "info without chat", path: "/api/group", body: `{}`, code: 400, want: "chat_jid is required\n"},
		{name: "info unreadable", path: "/api/group", body: `{"chat_jid":"g"}`, fake: unreadable("g"),
			code: 403, want: `{"success":false,"message":"chat g is not in the read allowlist","announce_only":false,"locked":false,"member_count":0}` + "\n"},
		{name: "info ok", path: "/api/group", body: `{"chat_jid":"g"}`, deps: readOnly,
			fake: func(f *fakeService) {
				f.groupInfo = bridge.GroupInfoResponse{Success: true, JID: "g", Name: "Crew", MemberCount: 2}
			},
			code: 200, want: `{"success":true,"jid":"g","name":"Crew","announce_only":false,"locked":false,"member_count":2}` + "\n",
			calls: []string{"GroupInfo"}, timeout: lookupTimeout},
		{name: "info failure", path: "/api/group", body: `{"chat_jid":"g"}`,
			fake: func(f *fakeService) { f.groupInfo = bridge.GroupInfoResponse{Message: "not a group"} },
			code: 500, want: `{"success":false,"message":"not a group","announce_only":false,"locked":false,"member_count":0}` + "\n",
			calls: []string{"GroupInfo"}, timeout: lookupTimeout},
		// changes with the 60 s timeout
		{name: "create ok", path: "/api/group/create", body: `{"name":"Crew","participants":["1","2"]}`, fake: changed,
			code: 200, want: changedBody, calls: []string{"CreateGroup"}, timeout: groupChangeTimeout,
			audit: []audit.Entry{{Endpoint: "group_create", Text: "Crew [1, 2]", Success: true, Result: "done"}}},
		{name: "create bad JSON", path: "/api/group/create", body: `[`, code: 400, want: "Invalid request format\n"},
		{name: "create refused", path: "/api/group/create", body: `{"name":"Crew"}`, fake: groupRefused,
			code: 403, want: `{"success":false,"message":"not allowed"}` + "\n", calls: []string{"CreateGroup"}, timeout: groupChangeTimeout,
			audit: []audit.Entry{{Endpoint: "group_create", Text: "Crew []", Result: "not allowed"}}},
		{name: "leave failure", path: "/api/group/leave", body: `{"chat_jid":"g"}`, fake: groupFailed,
			code: 500, want: `{"success":false,"message":"boom"}` + "\n", calls: []string{"LeaveGroup"}, timeout: groupChangeTimeout,
			audit: []audit.Entry{{Endpoint: "group_leave", Chat: "g", Result: "boom"}}},
		{name: "leave read-only", path: "/api/group/leave", body: `{"chat_jid":"g"}`, deps: readOnly,
			code: 403, want: readOnlyBody, audit: []audit.Entry{{Endpoint: "group_leave", Result: msgReadOnly}}},
		{name: "participants ok", path: "/api/group/participants", body: `{"chat_jid":"g","action":"add","participants":["1"]}`, fake: changed,
			code: 200, want: changedBody, calls: []string{"UpdateParticipants"}, timeout: groupChangeTimeout,
			audit: []audit.Entry{{Endpoint: "group_participants", Chat: "g", Text: "add 1", Success: true, Result: "done"}}},
		{name: "settings ok", path: "/api/group/settings", body: `{"chat_jid":"g","name":"New"}`, fake: changed,
			code: 200, want: changedBody, calls: []string{"UpdateGroupSettings"}, timeout: groupChangeTimeout,
			audit: []audit.Entry{{Endpoint: "group_settings", Chat: "g", Text: `{"chat_jid":"g","name":"New"}`, Success: true, Result: "done"}}},
		{name: "join rate limited", path: "/api/group/join", body: `{"invite":"x"}`, deps: exhausted,
			code: 429, want: limitBody, header: map[string]string{"Retry-After": "61"},
			audit: []audit.Entry{{Endpoint: "group_join", Result: limitMsg}}},
		{name: "join ok", path: "/api/group/join", body: `{"invite":"ABC"}`, fake: changed,
			code: 200, want: changedBody, calls: []string{"JoinGroup"}, timeout: groupChangeTimeout,
			audit: []audit.Entry{{Endpoint: "group_join", Text: "ABC", Success: true, Result: "done"}}},
		// join requests, invite links, disappearing messages, communities
		{name: "requests", path: "/api/group/requests", body: `{"chat_jid":"g"}`, fake: body,
			code: 200, want: `{"requests":[],"success":true}` + "\n", calls: []string{"GroupRequests"}},
		{name: "requests GET", method: http.MethodGet, path: "/api/group/requests", code: 405, want: "Method not allowed\n"},
		{name: "requests update without timeout", path: "/api/group/requests/update", body: `{"chat_jid":"g","action":"approve","participants":["1","2"]}`, fake: changed,
			code: 200, want: changedBody, calls: []string{"UpdateGroupRequests"},
			audit: []audit.Entry{{Endpoint: "group_requests", Chat: "g", Text: "approve 1, 2", Success: true, Result: "done"}}},
		{name: "invite reset without field checks", path: "/api/group/invite/reset", body: `{}`, fake: groupFailed,
			code: 500, want: `{"success":false,"message":"boom"}` + "\n", calls: []string{"ResetInviteLink"},
			audit: []audit.Entry{{Endpoint: "group_invite_reset", Result: "boom"}}},
		{name: "disappearing without chat", path: "/api/chat/disappearing", body: `{"timer":"7d"}`,
			code: 400, want: "chat_jid is required\n",
			audit: []audit.Entry{{Endpoint: "disappearing", Text: "7d", Result: "chat_jid is required"}}},
		{name: "disappearing ok", path: "/api/chat/disappearing", body: `{"chat_jid":"g","timer":"7d"}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"SetDisappearing"},
			audit: []audit.Entry{{Endpoint: "disappearing", Chat: "g", Text: "7d", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "community link", path: "/api/community/link", body: `{"community_jid":"c","group_jid":"g"}`, fake: changed,
			code: 200, want: changedBody, calls: []string{"LinkCommunity"},
			audit: []audit.Entry{{Endpoint: "community_link", Chat: "c", TargetID: "g", Success: true, Result: "done"}}},
		{name: "community groups", path: "/api/community/groups", body: `{"community_jid":"c"}`,
			fake: func(f *fakeService) { f.body, f.bodyStatus = map[string]any{"success": false, "message": "nope"}, 403 },
			code: 403, want: `{"message":"nope","success":false}` + "\n", calls: []string{"SubGroups"}},
		{name: "community groups bad JSON", path: "/api/community/groups", body: `x`, code: 400, want: "Invalid request format\n"},
	})
}
