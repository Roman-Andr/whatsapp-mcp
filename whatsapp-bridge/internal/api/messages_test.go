package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func TestMessagingRoutesValidate(t *testing.T) {
	b, handler := newTestBridge(t, "120@g.us")
	now := time.Now()
	mustStore(t, b.store, store.Message{ID: "P1", ChatJID: "120@g.us", Sender: "5", SenderJID: "5@lid", Content: "[poll] Depot?: North / South", Timestamp: now})
	b.store.SavePoll(context.Background(), "P1", "120@g.us", "Depot?", []string{"North", "South"}, 1)
	mustStore(t, b.store, store.Message{ID: "T1", ChatJID: "120@g.us", Sender: "5", SenderJID: "5@lid", Content: "hello", Timestamp: now})
	mustStore(t, b.store, store.Message{ID: "X1", ChatJID: "c@s.whatsapp.net", Sender: "c", Content: "secret", Timestamp: now})
	b.readAllowlist, _ = policy.NewAllowlist([]string{"120@g.us"})

	for _, c := range []struct{ path, body, want string }{
		{"/api/poll/vote", `{"chat_jid":"120@g.us","message_id":"P1","options":["West"]}`, "not an option"},
		{"/api/poll/vote", `{"chat_jid":"120@g.us","message_id":"T1","options":["North"]}`, "not a poll"},
		{"/api/poll/vote", `{"chat_jid":"120@g.us","message_id":"P1","options":["North"]}`, "Not connected"},
		{"/api/contact", `{"recipient":"120@g.us","name":"Dave","phone":"call me"}`, "not a phone number"},
		{"/api/contact", `{"recipient":"999@g.us","name":"Dave","phone":"+1 555 0100"}`, "send allowlist"},
		{"/api/sticker", `{"recipient":"120@g.us","media_path":"/tmp/missing-sticker.png"}`, "not found"},
		{"/api/forward", `{"chat_jid":"c@s.whatsapp.net","message_id":"X1","recipient":"120@g.us"}`, "read allowlist"},
		{"/api/forward", `{"chat_jid":"120@g.us","message_id":"T1","recipient":"999@g.us"}`, "send allowlist"},
		{"/api/star", `{"chat_jid":"120@g.us","message_id":"T1","starred":true}`, "Not connected"},
		{"/api/delete", `{"chat_jid":"120@g.us","message_id":"T1"}`, "Not connected"},
	} {
		rec := call(handler, c.path, c.body)
		if !strings.Contains(rec.Body.String(), c.want) {
			t.Errorf("%s %s: %d %s", c.path, c.body, rec.Code, rec.Body)
		}
	}
}

func TestMessageRoutes(t *testing.T) {
	t.Parallel()
	const c = "c@s.whatsapp.net"
	runRouteCases(t, []routeCase{
		// decode
		{name: "send GET", method: http.MethodGet, path: "/api/send",
			code: 405, want: "Method not allowed\n"},
		{name: "send bad JSON", path: "/api/send", body: `{"recipient":`,
			code: 400, want: "Invalid request format\n"},
		{name: "send body over 1 MiB", path: "/api/send", body: `{"message":"` + strings.Repeat("x", maxBody) + `"}`,
			code: 400, want: "Invalid request format\n"},
		// send
		{name: "send without recipient", path: "/api/send", body: `{"message":"hi"}`,
			code: 400, want: "Recipient is required\n",
			audit: []audit.Entry{{Endpoint: "send", Text: "hi", Result: "Recipient is required"}}},
		{name: "send without content", path: "/api/send", body: `{"recipient":"1"}`,
			code: 400, want: "Message or media path is required\n",
			audit: []audit.Entry{{Endpoint: "send", Chat: "1", Result: "Message or media path is required"}}},
		{name: "send recipient checked first", path: "/api/send", body: `{}`,
			code: 400, want: "Recipient is required\n",
			audit: []audit.Entry{{Endpoint: "send", Result: "Recipient is required"}}},
		{name: "send ok", path: "/api/send", body: `{"recipient":"1","message":"hi","reply_to_message_id":"Q"}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, header: map[string]string{"Content-Type": "application/json"}, calls: []string{"Send"},
			audit: []audit.Entry{{Endpoint: "send", Chat: "1", TargetID: "Q", Text: "hi", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "send file", path: "/api/send", body: `{"recipient":"1","message":"look","media_path":"/x/y/report.pdf"}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"Send"},
			audit: []audit.Entry{{Endpoint: "send", Chat: "1", Text: "[file report.pdf] look", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "send failure", path: "/api/send", body: `{"recipient":"1","message":"hi"}`, fake: answer(sendFailed),
			code: 500, want: failedBody, calls: []string{"Send"},
			audit: []audit.Entry{{Endpoint: "send", Chat: "1", Text: "hi", Result: "Not connected to WhatsApp"}}},
		{name: "send refused", path: "/api/send", body: `{"recipient":"x","message":"hi"}`, fake: answer(refused),
			code: 403, want: refuseBody, calls: []string{"Send"},
			audit: []audit.Entry{{Endpoint: "send", Chat: "x", Text: "hi", Result: refused.Message}}},
		{name: "send read-only", path: "/api/send", body: `{"recipient":"1","message":"hi"}`, deps: readOnly,
			code: 403, want: readOnlyBody,
			audit: []audit.Entry{{Endpoint: "send", Result: msgReadOnly}}},
		{name: "send rate limited", path: "/api/send", body: `{"recipient":"1","message":"hi"}`, deps: exhausted,
			code: 429, want: limitBody, header: map[string]string{"Retry-After": "61"},
			audit: []audit.Entry{{Endpoint: "send", Result: limitMsg}}},
		// react
		{name: "react without message", path: "/api/react", body: `{"chat_jid":"c","emoji":"x"}`,
			code: 400, want: "chat_jid and message_id are required\n",
			audit: []audit.Entry{{Endpoint: "react", Chat: "c", Text: "x", Result: "chat_jid and message_id are required"}}},
		{name: "react ok", path: "/api/react", body: `{"chat_jid":"c","message_id":"T","emoji":"x"}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"React"},
			audit: []audit.Entry{{Endpoint: "react", Chat: "c", TargetID: "T", Text: "x", Success: true, Result: "sent", MessageID: "M1"}}},
		// poll, location
		{name: "poll without recipient", path: "/api/poll", body: `{"question":"Q","options":["A","B"]}`,
			code: 400, want: "recipient is required\n",
			audit: []audit.Entry{{Endpoint: "poll", Text: "Q [A / B]", Result: "recipient is required"}}},
		{name: "poll ok", path: "/api/poll", body: `{"recipient":"1","question":"Q","options":["A","B"]}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"SendPoll"},
			audit: []audit.Entry{{Endpoint: "poll", Chat: "1", Text: "Q [A / B]", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "location without recipient", path: "/api/location", body: `{"latitude":1,"longitude":2}`,
			code: 400, want: "recipient is required\n",
			audit: []audit.Entry{{Endpoint: "location", Text: bridge.LocationRequest{Latitude: 1, Longitude: 2}.Summary(), Result: "recipient is required"}}},
		{name: "location ok", path: "/api/location", body: `{"recipient":"1","latitude":1,"longitude":2}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"SendLocation"},
			audit: []audit.Entry{{Endpoint: "location", Chat: "1", Text: bridge.LocationRequest{Latitude: 1, Longitude: 2}.Summary(), Success: true, Result: "sent", MessageID: "M1"}}},
		// edit, delete
		{name: "edit without text", path: "/api/edit", body: `{"chat_jid":"c","message_id":"T"}`,
			code: 400, want: "chat_jid, message_id and message are required\n",
			audit: []audit.Entry{{Endpoint: "edit", Chat: "c", TargetID: "T", Result: "chat_jid, message_id and message are required"}}},
		{name: "edit ok", path: "/api/edit", body: `{"chat_jid":"c","message_id":"T","message":"new"}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"Edit"},
			audit: []audit.Entry{{Endpoint: "edit", Chat: "c", TargetID: "T", Text: "new", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "delete without chat", path: "/api/delete", body: `{"message_id":"T"}`,
			code: 400, want: "chat_jid and message_id are required\n",
			audit: []audit.Entry{{Endpoint: "delete", TargetID: "T", Result: "chat_jid and message_id are required"}}},
		{name: "delete ok", path: "/api/delete", body: `{"chat_jid":"c","message_id":"T"}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"Delete"},
			audit: []audit.Entry{{Endpoint: "delete", Chat: "c", TargetID: "T", Success: true, Result: "sent", MessageID: "M1"}}},
		// read, typing: signal only, so never rate limited
		{name: "read without chat", path: "/api/read", body: `{"message_ids":["A","B"]}`,
			code: 400, want: "chat_jid is required\n",
			audit: []audit.Entry{{Endpoint: "read", TargetID: "A,B", Result: "chat_jid is required"}}},
		{name: "read ok despite the rate limit", path: "/api/read", body: `{"chat_jid":"c","message_ids":["A","B"]}`, deps: exhausted, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"MarkRead"},
			audit: []audit.Entry{{Endpoint: "read", Chat: "c", TargetID: "A,B", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "read read-only", path: "/api/read", body: `{"chat_jid":"c"}`, deps: readOnly,
			code: 403, want: readOnlyBody, audit: []audit.Entry{{Endpoint: "read", Result: msgReadOnly}}},
		{name: "typing without chat", path: "/api/typing", body: `{"state":"composing"}`,
			code: 400, want: "chat_jid is required\n",
			audit: []audit.Entry{{Endpoint: "typing", Text: "composing", Result: "chat_jid is required"}}},
		{name: "typing ok", path: "/api/typing", body: `{"chat_jid":"c","state":"paused"}`, deps: exhausted, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"SendTyping"},
			audit: []audit.Entry{{Endpoint: "typing", Chat: "c", Text: "paused", Success: true, Result: "sent", MessageID: "M1"}}},
		// history: neither guarded nor audited
		{name: "history without chat", path: "/api/history", body: `{}`,
			code: 400, want: "chat_jid is required\n"},
		{name: "history unreadable", path: "/api/history", body: `{"chat_jid":"` + c + `"}`, fake: unreadable(c),
			code: 403, want: `{"success":false,"message":"chat ` + c + ` is not in the read allowlist"}` + "\n"},
		{name: "history ok in read-only mode", path: "/api/history", body: `{"chat_jid":"c"}`, deps: readOnly, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"RequestHistory"}},
		{name: "history failure", path: "/api/history", body: `{"chat_jid":"c"}`, fake: answer(sendFailed),
			code: 500, want: failedBody, calls: []string{"RequestHistory"}},
		// download
		{name: "download without chat", path: "/api/download", body: `{"message_id":"M"}`,
			code: 400, want: "Message ID and Chat JID are required\n"},
		{name: "download unreadable", path: "/api/download", body: `{"message_id":"M","chat_jid":"` + c + `"}`, fake: unreadable(c),
			code: 403, want: `{"success":false,"message":"chat ` + c + ` is not in the read allowlist"}` + "\n"},
		{name: "download failure", path: "/api/download", body: `{"message_id":"M","chat_jid":"c"}`,
			fake: func(f *fakeService) { f.err = errors.New("no media") },
			code: 500, want: `{"success":false,"message":"Failed to download media: no media"}` + "\n",
			calls: []string{"DownloadMedia"}, timeout: downloadTimeout},
		{name: "download ok", path: "/api/download", body: `{"message_id":"M","chat_jid":"c"}`,
			fake: func(f *fakeService) {
				f.download = &bridge.DownloadMediaResponse{Success: true, Message: "ok", Filename: "a.jpg", Path: "/a.jpg"}
			},
			code: 200, want: `{"success":true,"message":"ok","filename":"a.jpg","path":"/a.jpg"}` + "\n",
			calls: []string{"DownloadMedia"}, timeout: downloadTimeout},
		// vote, contact, sticker, forward, star
		{name: "vote without message", path: "/api/poll/vote", body: `{"chat_jid":"c","options":["A","B"]}`,
			code: 400, want: "chat_jid and message_id are required\n",
			audit: []audit.Entry{{Endpoint: "vote", Chat: "c", Text: "A / B", Result: "chat_jid and message_id are required"}}},
		{name: "vote ok", path: "/api/poll/vote", body: `{"chat_jid":"c","message_id":"P","options":["A"]}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"VotePoll"},
			audit: []audit.Entry{{Endpoint: "vote", Chat: "c", TargetID: "P", Text: "A", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "vote rate limited", path: "/api/poll/vote", body: `{}`, deps: exhausted,
			code: 429, want: limitBody, audit: []audit.Entry{{Endpoint: "vote", Result: limitMsg}}},
		{name: "contact without recipient", path: "/api/contact", body: `{"name":"Dave","phone":"1"}`,
			code: 400, want: "recipient is required\n",
			audit: []audit.Entry{{Endpoint: "contact", Text: "Dave 1", Result: "recipient is required"}}},
		{name: "contact ok", path: "/api/contact", body: `{"recipient":"r","name":"Dave","phone":"1"}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"SendContact"},
			audit: []audit.Entry{{Endpoint: "contact", Chat: "r", Text: "Dave 1", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "sticker without file", path: "/api/sticker", body: `{"recipient":"r"}`,
			code: 400, want: "recipient and media_path are required\n",
			audit: []audit.Entry{{Endpoint: "sticker", Chat: "r", Text: "[sticker .]", Result: "recipient and media_path are required"}}},
		{name: "sticker ok", path: "/api/sticker", body: `{"recipient":"r","media_path":"/s/cat.webp"}`, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"Send"},
			audit: []audit.Entry{{Endpoint: "sticker", Chat: "r", Text: "[sticker cat.webp]", Success: true, Result: "sent", MessageID: "M1"}},
			check: func(t *testing.T, f *fakeService) {
				t.Helper()
				if req := f.last().req.(bridge.SendMessageRequest); !req.Sticker || req.MediaPath != "/s/cat.webp" {
					t.Errorf("request %+v", req)
				}
			}},
		{name: "forward without recipient", path: "/api/forward", body: `{"chat_jid":"c","message_id":"T"}`,
			code: 400, want: "chat_jid, message_id and recipient are required\n",
			audit: []audit.Entry{{Endpoint: "forward", TargetID: "c/T", Result: "chat_jid, message_id and recipient are required"}}},
		{name: "forward refused", path: "/api/forward", body: `{"chat_jid":"c","message_id":"T","recipient":"x"}`, fake: answer(refused),
			code: 403, want: refuseBody, calls: []string{"Forward"},
			audit: []audit.Entry{{Endpoint: "forward", Chat: "x", TargetID: "c/T", Result: refused.Message}}},
		{name: "star without message", path: "/api/star", body: `{"chat_jid":"c"}`,
			code: 400, want: "chat_jid and message_id are required\n",
			audit: []audit.Entry{{Endpoint: "star", Chat: "c", Result: "chat_jid and message_id are required"}}},
		{name: "star ok despite the rate limit", path: "/api/star", body: `{"chat_jid":"c","message_id":"T"}`, deps: exhausted, fake: answer(sentOK),
			code: 200, want: sentOKBody, calls: []string{"Star"},
			audit: []audit.Entry{{Endpoint: "star", Chat: "c", TargetID: "T", Success: true, Result: "sent", MessageID: "M1"}}},
		{name: "star read-only", path: "/api/star", body: `{}`, deps: readOnly,
			code: 403, want: readOnlyBody, audit: []audit.Entry{{Endpoint: "star", Result: msgReadOnly}}},
	})
}
