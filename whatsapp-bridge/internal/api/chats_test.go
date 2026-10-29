package api

import (
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
)

func TestChatRoutesValidate(t *testing.T) {
	b, handler := newTestBridge(t, "120@g.us")
	for _, c := range []struct{ path, body, want string }{
		{"/api/chat", `{"chat_jid":"120@g.us","action":"explode"}`, "unknown action"},
		{"/api/chat", `{"chat_jid":"999@g.us","action":"archive"}`, "send allowlist"},
		{"/api/chat", `{"chat_jid":"120@g.us","action":"mute","mute_hours":-1}`, "must not be negative"},
		{"/api/chat", `{"chat_jid":"120@g.us","action":"pin"}`, "Not connected"},
		{"/api/label/chat", `{"chat_jid":"120@g.us","label":"Paid","labeled":true}`, "no label"},
		{"/api/label/edit", `{"name":"","color":1}`, "name is required"},
		{"/api/label/edit", `{"name":"X","color":40}`, "color must be"},
	} {
		rec := call(handler, c.path, c.body)
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
	b.readOnly = true
	if rec := call(handler, "/api/chat", `{"chat_jid":"120@g.us","action":"pin"}`); rec.Code != 403 {
		t.Errorf("read-only: %d", rec.Code)
	}
}

func TestChatRoutes(t *testing.T) {
	t.Parallel()
	runRouteCases(t, []routeCase{
		{name: "chat without chat", path: "/api/chat", body: `{"action":"pin"}`,
			code: 400, want: "chat_jid is required\n",
			audit: []audit.Entry{{Endpoint: "chat", Text: "pin", Result: "chat_jid is required"}}},
		{name: "chat ok despite the rate limit", path: "/api/chat", body: `{"chat_jid":"c","action":"pin"}`, deps: exhausted, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"ChatAction"},
			audit: []audit.Entry{{Endpoint: "chat", Chat: "c", Text: "pin", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "chat read-only", path: "/api/chat", body: `{"chat_jid":"c"}`, deps: readOnly,
			code: 403, want: readOnlyBody, audit: []audit.Entry{{Endpoint: "chat", Result: msgReadOnly}}},
		{name: "label chat without chat", path: "/api/label/chat", body: `{"label":"Paid","labeled":true}`,
			code: 400, want: "chat_jid is required\n",
			audit: []audit.Entry{{Endpoint: "label_chat", Text: "Paid true", Result: "chat_jid is required"}}},
		{name: "label chat refused", path: "/api/label/chat", body: `{"chat_jid":"x","label":"Paid"}`, fake: answer(refused),
			code: 403, want: refuseBody, calls: []string{"LabelChat"},
			audit: []audit.Entry{{Endpoint: "label_chat", Chat: "x", Text: "Paid false", Result: refused.Message}}},
		{name: "label edit has no field checks", path: "/api/label/edit", body: `{"label_id":"3","name":"Paid"}`, fake: answer(sendFailed),
			code: 500, want: failedBody, calls: []string{"EditLabel"},
			audit: []audit.Entry{{Endpoint: "label_edit", TargetID: "3", Text: "Paid", Result: "Not connected to WhatsApp"}}},
	})
}
