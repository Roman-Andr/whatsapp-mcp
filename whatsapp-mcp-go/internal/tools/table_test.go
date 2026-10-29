package tools

// Every tool, called through the MCP layer: its arguments reach the
// handler, the handler reaches the store and the bridge, and the result
// comes back in the shape of its output kind.

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/storetest"
)

// toolCase is one call of a tool: its arguments, what the bridge received
// (as JSON of [endpoint, payload] pairs) and the structured result as JSON,
// or the text of a result without one.
type toolCase struct {
	name  string
	tool  string
	args  map[string]any
	setup func(t *testing.T, s *testStore, b *storetest.Bridge) map[string]any
	calls string
	want  string
	text  string
}

func TestEveryToolThroughMCP(t *testing.T) {
	t.Parallel()
	for _, tc := range toolCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			b := storetest.NewBridge(t, s.Fixture)
			args := tc.args
			if tc.setup != nil {
				if extra := tc.setup(t, s, b); extra != nil {
					args = extra
				}
			}
			res := callTool(t, s.connectClient(t), tc.tool, args)
			if res.IsError {
				t.Fatalf("error: %s\nlog: %s", textOf(res), s.log.String())
			}
			norm := strings.NewReplacer(s.Dir, "STORE_DIR", b.URL, "BRIDGE_URL").Replace
			calls := norm(b.Summary())
			if calls != jsonOf(t, tc.calls) {
				t.Errorf("bridge calls:\n%s\nwant:\n%s", calls, jsonOf(t, tc.calls))
			}
			if tc.text != "" || tc.want == "" {
				if got := norm(textOf(res)); got != tc.text {
					t.Errorf("text:\n%q\nwant:\n%q", got, tc.text)
				}
			}
			if tc.want != "" || tc.text == "" {
				if got := norm(structuredOf(t, res)); got != jsonOf(t, tc.want) {
					t.Errorf("result:\n%s\nwant:\n%s", got, tc.want)
				}
			}
		})
	}
}

func answer(body string) func(http.ResponseWriter, *http.Request, map[string]any) {
	return func(w http.ResponseWriter, _ *http.Request, _ map[string]any) { w.Write([]byte(body)) }
}

func toolCases() []toolCase {
	return []toolCase{
		{name: "search_contacts", tool: "search_contacts", args: map[string]any{"query": "david"}, calls: "null", want: `{"result":[{"jid":"18046727669@s.whatsapp.net","name":"David Bender","phone_number":"18046727669"}]}`},
		{name: "list_messages", tool: "list_messages", args: map[string]any{"chat_jid": direct, "include_context": "false"}, calls: "null", want: `{"result":"[2026-10-27 11:00:00 +03:00] Chat: 32547396399312 From: David Bender: [id: D1 | chat: 32547396399312@lid] direct hello\n"}`},
		{name: "list_chats", tool: "list_chats", args: map[string]any{"limit": 1, "include_last_message": false}, calls: "null", want: `{"result":[{"archived":false,"jid":"120363431597928274@g.us","labels":[],"last_is_from_me":null,"last_message":null,"last_message_time":"2026-10-27T12:05:00+03:00","last_sender":null,"muted_until":null,"name":"Snow operations system","pinned":false,"unread_count":0}]}`},
		{name: "list_unread_chats", tool: "list_unread_chats", args: map[string]any{}, calls: "null", want: `{"result":[]}`},
		{name: "get_chat", tool: "get_chat", args: map[string]any{"chat_jid": direct}, calls: "null", want: `{"result":{"archived":false,"jid":"32547396399312@lid","labels":[],"last_is_from_me":false,"last_message":"direct hello","last_message_time":"2026-10-27T11:00:00+03:00","last_sender":"32547396399312","muted_until":null,"name":"David Bender","pinned":false,"unread_count":0}}`},
		{name: "get_chat missing", tool: "get_chat", args: map[string]any{"chat_jid": "x@g.us"}, calls: "null", want: `{"result":null}`},
		{name: "get_direct_chat_by_contact", tool: "get_direct_chat_by_contact", args: map[string]any{"sender_phone_number": "+18046727669"}, calls: "null", want: `{"result":{"archived":false,"jid":"32547396399312@lid","labels":[],"last_is_from_me":false,"last_message":"direct hello","last_message_time":"2026-10-27T11:00:00+03:00","last_sender":"32547396399312","muted_until":null,"name":"David Bender","pinned":false,"unread_count":0}}`},
		{name: "get_contact_chats", tool: "get_contact_chats", args: map[string]any{"jid": "28952508764308@lid"}, calls: "null", want: `{"result":[{"archived":false,"jid":"120363431597928274@g.us","labels":[],"last_is_from_me":false,"last_message":"see screenshot","last_message_time":"2026-10-27T12:05:00+03:00","last_sender":"28952508764308","muted_until":null,"name":"Snow operations system","pinned":false,"unread_count":0}]}`},
		{name: "get_last_interaction", tool: "get_last_interaction", args: map[string]any{"jid": direct}, calls: "null", want: `{"result":"[2026-10-27 12:00:00 +03:00] Chat: Snow operations system From: David Bender: [id: A1 | chat: 120363431597928274@g.us] The plow route on Main street is wrong\n"}`},
		{name: "get_last_interaction none", tool: "get_last_interaction", args: map[string]any{"jid": "9@lid"}, calls: "null", want: `{"result":null}`},
		{name: "get_message_context", tool: "get_message_context", args: map[string]any{"message_id": "A2", "before": 0, "after": 0}, calls: "null", want: `{"result":"[2026-10-27 12:01:00 +03:00] Chat: Snow operations system From: Me: [id: A2 | chat: 120363431597928274@g.us | edited] (reply to A1 David Bender: \"The plow route on Main street is wrong\") Fixed it [reactions: 👍 David Bender]\n"}`},
		{name: "send_message", tool: "send_message", args: map[string]any{"recipient": group, "message": "hi", "mentions": []string{"1"}}, calls: `[["send",{"mentions":["1"],"message":"hi","recipient":"120363431597928274@g.us"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "send_message without recipient", tool: "send_message", args: map[string]any{"recipient": "", "message": "hi"}, calls: "null", want: `{"message":"Recipient must be provided","success":false}`},
		{name: "send_file", tool: "send_file", setup: func(t *testing.T, s *testStore, b *storetest.Bridge) map[string]any {
			t.Helper()
			return map[string]any{"recipient": group, "media_path": writeFile(t, filepath.Join(s.Dir, "r.pdf"), []byte("%PDF")), "caption": "Invoice", "reply_to_message_id": "A1"}
		}, calls: `[["send",{"media_path":"STORE_DIR/r.pdf","message":"Invoice","recipient":"120363431597928274@g.us","reply_to_message_id":"A1"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "send_file missing", tool: "send_file", args: map[string]any{"recipient": group, "media_path": "/nonexistent/r.pdf"}, calls: "null", want: `{"message":"Media file not found: /nonexistent/r.pdf","message_id":null,"success":false}`},
		{name: "send_audio_message", tool: "send_audio_message", setup: func(t *testing.T, s *testStore, b *storetest.Bridge) map[string]any {
			t.Helper()
			return map[string]any{"recipient": group, "media_path": writeFile(t, filepath.Join(s.Dir, "v.ogg"), []byte("OggS"))}
		}, calls: `[["send",{"media_path":"STORE_DIR/v.ogg","recipient":"120363431597928274@g.us"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "send_reaction", tool: "send_reaction", args: map[string]any{"chat_jid": group, "message_id": "A1", "emoji": "👍"}, calls: `[["react",{"chat_jid":"120363431597928274@g.us","emoji":"👍","message_id":"A1"}]]`, want: `{"message":"ok","success":true}`},
		{name: "send_poll", tool: "send_poll", args: map[string]any{"recipient": group, "question": "Depot?", "options": []string{"N", "S"}}, calls: `[["poll",{"options":["N","S"],"question":"Depot?","recipient":"120363431597928274@g.us","selectable_count":1}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "send_location", tool: "send_location", args: map[string]any{"recipient": group, "latitude": "55.75", "longitude": 37, "name": "Depot"}, calls: `[["location",{"latitude":55.75,"longitude":37,"name":"Depot","recipient":"120363431597928274@g.us"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "edit_message", tool: "edit_message", args: map[string]any{"chat_jid": group, "message_id": "A2", "new_text": "fixed"}, calls: `[["edit",{"chat_jid":"120363431597928274@g.us","message":"fixed","message_id":"A2"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "delete_message", tool: "delete_message", args: map[string]any{"chat_jid": group, "message_id": "A2"}, calls: `[["delete",{"chat_jid":"120363431597928274@g.us","message_id":"A2"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "vote_poll", tool: "vote_poll", args: map[string]any{"chat_jid": group, "message_id": "P1", "options": []string{"N"}}, calls: `[["poll/vote",{"chat_jid":"120363431597928274@g.us","message_id":"P1","options":["N"]}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "send_contact", tool: "send_contact", args: map[string]any{"recipient": group, "name": "Dave", "phone": "+1"}, calls: `[["contact",{"name":"Dave","phone":"+1","recipient":"120363431597928274@g.us"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "send_sticker", tool: "send_sticker", setup: func(t *testing.T, s *testStore, b *storetest.Bridge) map[string]any {
			t.Helper()
			return map[string]any{"recipient": group, "media_path": writeFile(t, filepath.Join(s.Dir, "s.webp"), []byte("RIFF")), "reply_to_message_id": "A1"}
		}, calls: `[["sticker",{"media_path":"STORE_DIR/s.webp","recipient":"120363431597928274@g.us","reply_to_message_id":"A1"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "forward_message", tool: "forward_message", args: map[string]any{"chat_jid": group, "message_id": "A1", "recipient": direct}, calls: `[["forward",{"chat_jid":"120363431597928274@g.us","message_id":"A1","recipient":"32547396399312@lid"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "star_message", tool: "star_message", args: map[string]any{"chat_jid": group, "message_id": "A1"}, calls: `[["star",{"chat_jid":"120363431597928274@g.us","message_id":"A1","starred":true}]]`, want: `{"message":"ok","success":true}`},
		{name: "mark_read", tool: "mark_read", args: map[string]any{"chat_jid": group, "message_ids": []string{"A1"}}, calls: `[["read",{"chat_jid":"120363431597928274@g.us","message_ids":["A1"]}]]`, want: `{"message":"ok","success":true}`},
		{name: "send_typing", tool: "send_typing", args: map[string]any{"chat_jid": group}, calls: `[["typing",{"chat_jid":"120363431597928274@g.us","state":"composing"}]]`, want: `{"message":"ok","success":true}`},
		{name: "get_group_info", tool: "get_group_info", args: map[string]any{"chat_jid": group}, calls: `[["group",{"chat_jid":"120363431597928274@g.us","invite_link":true}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "get_group_info not a group", tool: "get_group_info", args: map[string]any{"chat_jid": direct}, calls: "null", want: `{"result":{"message":"'32547396399312@lid' is not a group JID (…@g.us)","success":false}}`},
		{name: "manage_chat", tool: "manage_chat", args: map[string]any{"chat_jid": group, "action": "mute", "mute_hours": 8}, calls: `[["chat",{"action":"mute","chat_jid":"120363431597928274@g.us","mute_hours":8}]]`, want: `{"message":"ok","success":true}`},
		{name: "list_labels", tool: "list_labels", args: map[string]any{}, calls: "null", want: `{"result":[]}`},
		{name: "label_chat", tool: "label_chat", args: map[string]any{"chat_jid": group, "label": "Paid"}, calls: `[["label/chat",{"chat_jid":"120363431597928274@g.us","label":"Paid","labeled":true}]]`, want: `{"message":"ok","success":true}`},
		{name: "edit_label", tool: "edit_label", args: map[string]any{"name": "Paid", "color": 2}, calls: `[["label/edit",{"color":2,"deleted":false,"name":"Paid"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "create_group", tool: "create_group", args: map[string]any{"name": "Crew", "participants": []string{"1"}}, calls: `[["group/create",{"name":"Crew","participants":["1"]}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "leave_group", tool: "leave_group", args: map[string]any{"chat_jid": group}, calls: `[["group/leave",{"chat_jid":"120363431597928274@g.us"}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "update_group_participants", tool: "update_group_participants", args: map[string]any{"chat_jid": group, "action": "add", "participants": []string{"1"}}, calls: `[["group/participants",{"action":"add","chat_jid":"120363431597928274@g.us","participants":["1"]}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "update_group_settings", tool: "update_group_settings", args: map[string]any{"chat_jid": group, "name": "Crew", "join_approval": true}, calls: `[["group/settings",{"chat_jid":"120363431597928274@g.us","join_approval":true,"name":"Crew"}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "join_group", tool: "join_group", args: map[string]any{"invite": "AbC"}, calls: `[["group/join",{"invite":"AbC"}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "get_group_join_requests", tool: "get_group_join_requests", args: map[string]any{"chat_jid": group}, calls: `[["group/requests",{"chat_jid":"120363431597928274@g.us"}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "update_group_join_requests", tool: "update_group_join_requests", args: map[string]any{"chat_jid": group, "action": "approve", "participants": []string{"1"}}, calls: `[["group/requests/update",{"action":"approve","chat_jid":"120363431597928274@g.us","participants":["1"]}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "reset_group_invite_link", tool: "reset_group_invite_link", args: map[string]any{"chat_jid": group}, calls: `[["group/invite/reset",{"chat_jid":"120363431597928274@g.us"}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "set_disappearing_messages", tool: "set_disappearing_messages", args: map[string]any{"chat_jid": group, "timer": "7d"}, calls: `[["chat/disappearing",{"chat_jid":"120363431597928274@g.us","timer":"7d"}]]`, want: `{"message":"ok","success":true}`},
		{name: "link_community_group", tool: "link_community_group", args: map[string]any{"community_jid": "1@g.us", "group_jid": group, "unlink": true}, calls: `[["community/link",{"community_jid":"1@g.us","group_jid":"120363431597928274@g.us","unlink":true}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "list_community_groups", tool: "list_community_groups", args: map[string]any{"community_jid": "1@g.us"}, calls: `[["community/groups",{"community_jid":"1@g.us"}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "request_chat_history", tool: "request_chat_history", args: map[string]any{"chat_jid": group, "before_message_id": "A1"}, calls: `[["history",{"before_message_id":"A1","chat_jid":"120363431597928274@g.us","count":50}]]`, want: `{"message":"ok","success":true}`},
		{name: "request_chat_history without chat", tool: "request_chat_history", args: map[string]any{"chat_jid": ""}, calls: "null", want: `{"message":"chat_jid must be provided","success":false}`},
		{name: "download_media", tool: "download_media", args: map[string]any{"message_id": "A3", "chat_jid": group}, setup: func(_ *testing.T, _ *testStore, b *storetest.Bridge) map[string]any {
			b.Answer = answer(`{"success": true, "path": "/store/a.jpg", "media_type": "image", "mimetype": "image/jpeg"}`)
			return nil
		}, calls: `[["download",{"chat_jid":"120363431597928274@g.us","message_id":"A3"}]]`, want: `{"result":{"file_path":"/store/a.jpg","media_type":"image","message":"Media downloaded successfully","mimetype":"image/jpeg","success":true}}`},
		{name: "download_media failed", tool: "download_media", args: map[string]any{"message_id": "A3", "chat_jid": group}, setup: func(_ *testing.T, _ *testStore, b *storetest.Bridge) map[string]any {
			b.Answer = answer(`{"success": false}`)
			return nil
		}, calls: `[["download",{"chat_jid":"120363431597928274@g.us","message_id":"A3"}]]`, want: `{"result":{"message":"Failed to download media","success":false}}`},
		{name: "view_media without media", tool: "view_media", args: map[string]any{"message_id": "A1", "chat_jid": group}, calls: "null", text: "{\n  \"success\": false,\n  \"message\": \"Message A1 has no media\"\n}"},
		{name: "get_contact_info", tool: "get_contact_info", args: map[string]any{"jid": "18046727669"}, calls: `[["contact/info",{"jid":"18046727669","picture":false}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "view_profile_picture none", tool: "view_profile_picture", args: map[string]any{"jid": "18046727669"}, setup: func(_ *testing.T, _ *testStore, b *storetest.Bridge) map[string]any {
			b.Answer = answer(`{"success": true, "picture_error": "no picture set"}`)
			return nil
		}, calls: `[["contact/info",{"jid":"18046727669","picture":true}]]`, text: "{\n  \"success\": false,\n  \"message\": \"no picture set\"\n}"},
		{name: "block_contact", tool: "block_contact", args: map[string]any{"jid": "1", "blocked": true}, calls: `[["block",{"blocked":true,"jid":"1"}]]`, want: `{"message":"ok","success":true}`},
		{name: "list_blocked_contacts", tool: "list_blocked_contacts", args: map[string]any{}, setup: func(_ *testing.T, _ *testStore, b *storetest.Bridge) map[string]any {
			b.Answer = answer(`{"success": true, "blocked": [{"jid": "1@s.whatsapp.net"}]}`)
			return nil
		}, calls: "null", want: `{"result":{"blocked":[{"jid":"1@s.whatsapp.net"}],"success":true}}`},
		{name: "get_presence", tool: "get_presence", args: map[string]any{"jid": "18046727669"}, calls: `[["presence",{"jid":"18046727669"}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "list_statuses", tool: "list_statuses", args: map[string]any{}, calls: "null", want: `{"result":"No messages to display."}`},
		{name: "post_status", tool: "post_status", args: map[string]any{"text": "Plows out"}, calls: `[["send",{"message":"Plows out","recipient":"status@broadcast"}]]`, want: `{"message":"ok","message_id":"NEW","success":true}`},
		{name: "post_status empty", tool: "post_status", args: map[string]any{}, calls: "null", want: `{"message":"text or media_path must be given","message_id":null,"success":false}`},
		{name: "list_channels", tool: "list_channels", args: map[string]any{}, setup: func(_ *testing.T, _ *testStore, b *storetest.Bridge) map[string]any {
			b.Answer = answer(`{"success": true, "channels": [{"jid": "1@newsletter"}]}`)
			return nil
		}, calls: "null", want: `{"result":{"channels":[{"jid":"1@newsletter"}],"success":true}}`},
		{name: "get_channel_messages", tool: "get_channel_messages", args: map[string]any{"channel_jid": "1@newsletter"}, calls: `[["channel/messages",{"before_server_id":0,"channel_jid":"1@newsletter","count":20}]]`, want: `{"result":{"message":"ok","message_id":"NEW","success":true}}`},
		{name: "follow_channel", tool: "follow_channel", args: map[string]any{"channel": "1@newsletter"}, calls: `[["channel/follow",{"channel":"1@newsletter","follow":true}]]`, want: `{"message":"ok","success":true}`},
		{name: "transcribe_voice_message not audio", tool: "transcribe_voice_message", args: map[string]any{"message_id": "A1", "chat_jid": group}, calls: "null", want: `{"result":{"message":"Message A1 is not an audio message","success":false}}`},
		{name: "wait_for_events", tool: "wait_for_events", args: map[string]any{"timeout_seconds": 1}, calls: "null", want: `{"result":{"events":[],"success":true,"waited_seconds":0}}`},
		{name: "export_chat bad format", tool: "export_chat", args: map[string]any{"chat_jid": group, "format": "pdf"}, calls: "null", want: `{"result":{"message":"format must be one of txt, json, html","success":false}}`},
		{name: "list_accounts", tool: "list_accounts", args: map[string]any{}, setup: func(_ *testing.T, _ *testStore, b *storetest.Bridge) map[string]any {
			b.Answer = answer(`{"connected": true, "jid": "1@s.whatsapp.net"}`)
			return nil
		}, calls: "null", want: `{"result":[{"api_url":"BRIDGE_URL","connected":true,"default":true,"error":null,"jid":"1@s.whatsapp.net","name":"default","store_dir":"STORE_DIR"}]}`},
		{name: "get_bridge_status", tool: "get_bridge_status", args: map[string]any{}, setup: func(_ *testing.T, _ *testStore, b *storetest.Bridge) map[string]any {
			b.Answer = answer(`{"connected": true}`)
			return nil
		}, calls: "null", want: `{"result":{"connected":true}}`},
		{name: "reconnect_bridge", tool: "reconnect_bridge", args: map[string]any{}, calls: `[["connection/reconnect",{}]]`, want: `{"message":"ok","success":true}`},
	}
}
