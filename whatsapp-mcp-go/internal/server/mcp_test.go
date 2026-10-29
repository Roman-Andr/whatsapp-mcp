package server

// The server as clients see it: tools, annotations, results, resources,
// read-only mode, accounts and the HTTP transport.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/storetest"
)

func TestToolsAreRegistered(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	tools := listTools(t, connectClient(t, f))
	if len(tools) != 59 {
		t.Errorf("%d tools", len(tools))
	}
	for _, name := range []string{"send_reaction", "request_chat_history", "view_media", "transcribe_voice_message",
		"wait_for_events", "get_bridge_status", "send_message", "send_file", "export_chat", "list_accounts"} {
		if tools[name] == nil {
			t.Errorf("%s missing", name)
		}
	}
}

func TestToolAnnotationsSeparateReadsFromSends(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	tools := listTools(t, connectClient(t, f))
	for name, tool := range tools {
		h := tool.Annotations
		if h == nil {
			t.Fatalf("%s has no annotations", name)
		}
		if strings.HasPrefix(name, "list_") || strings.HasPrefix(name, "get_") || strings.HasPrefix(name, "search_") ||
			name == "wait_for_events" || name == "download_media" || name == "view_media" {
			if !h.ReadOnlyHint || *h.DestructiveHint {
				t.Errorf("%s is not a read", name)
			}
		}
	}
	for _, name := range []string{"send_message", "send_file", "send_audio_message", "send_reaction"} {
		h := tools[name].Annotations
		if h.ReadOnlyHint || !*h.DestructiveHint || !*h.OpenWorldHint {
			t.Errorf("%s is not a send", name)
		}
	}
}

func TestToolsReturnStructuredResults(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	cs := connectClient(t, f)
	res := callTool(t, cs, "list_chats", map[string]any{"limit": 5})
	chats := structured(t, res)["result"].([]any)
	if len(chats) != 2 || len(res.Content) != 2 {
		t.Fatalf("got %+v", res)
	}
	// One pydantic-style text block per chat.
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "{\n  \"jid\": \""+group+"\",\n  \"name\": \"Snow operations system\",\n  \"last_message_time\": \"2026-10-27T12:05:00+03:00\"") {
		t.Errorf("text %s", text)
	}
	res = callTool(t, cs, "send_message", map[string]any{"recipient": group, "message": "x"})
	got := structured(t, res)
	if got["success"] != false || !strings.HasPrefix(got["message"].(string), "Request error") || got["message_id"] != nil {
		t.Errorf("got %v", got)
	}
	if _, ok := got["message_id"]; !ok {
		t.Error("message_id missing")
	}
	// Invalid input fails the call, not the server.
	res = callTool(t, cs, "list_messages", map[string]any{"after": "yesterday"})
	if !res.IsError || res.Content[0].(*mcp.TextContent).Text != "Error executing tool list_messages" {
		t.Errorf("got %+v", res.Content[0])
	}
	res = callTool(t, cs, "list_messages", map[string]any{"limit": "abc"})
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "Input should be a valid integer") {
		t.Errorf("got %+v", res.Content[0])
	}
	if res = callTool(t, cs, "list_messages", map[string]any{"limit": "1", "include_context": "false"}); res.IsError {
		t.Errorf("lax coercion failed: %+v", res.Content[0])
	}
	res = callTool(t, cs, "no_such_tool", nil)
	if !res.IsError || res.Content[0].(*mcp.TextContent).Text != "Unknown tool: no_such_tool" {
		t.Errorf("got %+v", res.Content)
	}
	res = callTool(t, cs, "get_chat", map[string]any{"chat_jid": "nope"})
	if len(res.Content) != 0 || structured(t, res)["result"] != nil {
		t.Errorf("missing chat: %+v", res)
	}
}

func TestViewMediaReturnsTheImage(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	pic := filepath.Join(t.TempDir(), "pic.png")
	os.WriteFile(pic, tinyPNG(t), 0o600)
	b := storetest.NewBridge(t, s)
	b.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		w.Write([]byte(`{"success": true, "path": "` + pic + `"}`))
	}
	cs := connectClient(t, s)
	res := callTool(t, cs, "view_media", map[string]any{"message_id": "A3", "chat_jid": group})
	img, ok := res.Content[0].(*mcp.ImageContent)
	if !ok || img.MIMEType != "image/png" || res.StructuredContent != nil {
		t.Fatalf("got %+v", res.Content[0])
	}
	res = callTool(t, cs, "view_media", map[string]any{"message_id": "A1", "chat_jid": group})
	if text := res.Content[0].(*mcp.TextContent).Text; text != "{\n  \"success\": false,\n  \"message\": \"Message A1 has no media\"\n}" {
		t.Errorf("got %s", text)
	}
}

func TestReadOnlyModeHidesEveryChangingTool(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	f.Config.ReadOnly = true
	cs := connectClient(t, f)
	tools := listTools(t, cs)
	if tools["list_messages"] == nil || tools["download_media"] == nil {
		t.Error("read tools hidden")
	}
	for _, name := range []string{"send_message", "send_file", "mark_read", "send_typing", "delete_message"} {
		if tools[name] != nil {
			t.Errorf("%s visible", name)
		}
	}
	for name, tool := range tools {
		if !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not read-only", name)
		}
	}
	if res := callTool(t, cs, "send_message", map[string]any{"recipient": "x", "message": "y"}); !res.IsError {
		t.Error("hidden tool callable")
	}
}

func TestResourcesAreReadable(t *testing.T) {
	t.Parallel()
	f := newStore(t)
	cs := connectClient(t, f)
	ctx := t.Context()
	listed, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	uris := map[string]bool{}
	for _, r := range listed.Resources {
		uris[r.URI] = true
	}
	for _, u := range []string{"whatsapp://chats", "whatsapp://chats/unread", "whatsapp://status"} {
		if !uris[u] {
			t.Errorf("%s not listed", u)
		}
	}
	templates, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil || len(templates.ResourceTemplates) != 1 || templates.ResourceTemplates[0].URITemplate != "whatsapp://chat/{chat_jid}" {
		t.Fatalf("templates %v %v", templates, err)
	}
	read := func(uri string) string {
		res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
		if err != nil {
			t.Fatal(err)
		}
		return res.Contents[0].Text
	}
	assertContains(t, read("whatsapp://chat/"+group), "Main street")
	chats := read("whatsapp://chats")
	// One-space indent, times in the space-separated str(datetime) form.
	assertContains(t, chats, "[\n {\n  \"jid\": \""+group+"\"", `"last_message_time": "2026-10-27 12:05:00+03:00"`)
	assertContains(t, read("whatsapp://status"), `"connected": false`)
}

func TestHTTPTransportRequiresTheToken(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, newStore(t))
	srv := httptest.NewServer(s.httpHandler("s3cret-token-0123456789"))
	defer srv.Close()
	init := `{"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": {"protocolVersion": "2025-06-18", "capabilities": {}, "clientInfo": {"name": "t", "version": "0"}}}`
	post := func(auth string) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(init))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		buf := new(strings.Builder)
		b := make([]byte, 4096)
		n, _ := resp.Body.Read(b)
		buf.Write(b[:n])
		return resp, buf.String()
	}
	if resp, _ := post(""); resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != "Bearer" {
		t.Errorf("no token: %d", resp.StatusCode)
	}
	if resp, _ := post("Bearer wrong"); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", resp.StatusCode)
	}
	resp, body := post("Bearer s3cret-token-0123456789")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "protocolVersion") {
		t.Errorf("good token: %d %s", resp.StatusCode, body)
	}
}

func TestSeveralAccounts(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	work := t.TempDir()
	storetest.Exec(t, filepath.Join(work, "messages.db"), storetest.BridgeSchema+
		"INSERT INTO chats (jid, name, last_message_time) VALUES ('w@g.us', 'Work crew', '2026-10-27 10:00:00+03:00')")
	os.WriteFile(filepath.Join(work, "api_token"), []byte("worktoken\n"), 0o600)
	b := storetest.NewBridge(t, nil)
	s.Config.Accounts = `{"work": {"store_dir": "` + work + `", "api_url": "` + b.Srv.URL + `/api/"}}`
	cs := connectClient(t, s)
	tools := listTools(t, cs)
	schema, _ := json.Marshal(tools["list_chats"].InputSchema)
	assertContains(t, string(schema), `"account"`)
	assertContains(t, tools["send_message"].Description, "account: Which WhatsApp account to use")

	result := func(res *mcp.CallToolResult) []string {
		var out []string
		for _, c := range structured(t, res)["result"].([]any) {
			out = append(out, c.(map[string]any)["jid"].(string))
		}
		return out
	}
	if got := result(callTool(t, cs, "list_chats", map[string]any{"account": "work"})); len(got) != 1 || got[0] != "w@g.us" {
		t.Errorf("work chats %v", got)
	}
	for _, jid := range result(callTool(t, cs, "list_chats", nil)) {
		if jid == "w@g.us" {
			t.Error("work chat in the default account")
		}
	}
	callTool(t, cs, "send_message", map[string]any{"recipient": "w@g.us", "message": "hi", "account": "work"})
	if p := b.Calls(); len(p) != 1 || p[0].Endpoint != "send" || p[0].Auth != "Bearer worktoken" {
		t.Errorf("posts %+v", p)
	}
	callTool(t, cs, "reconnect_bridge", map[string]any{"account": "work"})
	if p := b.Calls(); len(p) != 2 || p[1].Endpoint != "connection/reconnect" || p[1].Auth != "Bearer worktoken" {
		t.Errorf("posts %+v", p)
	}
	if res := callTool(t, cs, "list_chats", map[string]any{"account": "home"}); !res.IsError {
		t.Error("unknown account accepted")
	}
	if _, err := s.Registry().Resolve("home"); err == nil || err.Error() != "Unknown WhatsApp account 'home'; configured: default, work" {
		t.Errorf("got %v", err)
	}
	s.Config.Accounts = "[1]"
	if _, err := New(s.Config, DefaultDeps(s.Config, logging.Discard())); err == nil {
		t.Error("invalid WHATSAPP_ACCOUNTS accepted")
	}
}

func TestSubscribedClientsHearAboutUpdates(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, newStore(t))
	ctx := t.Context()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	updated := make(chan string, 1)
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, &mcp.ClientOptions{
		ResourceUpdatedHandler: func(_ context.Context, req *mcp.ResourceUpdatedNotificationRequest) { updated <- req.Params.URI },
	})
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	if err := cs.Subscribe(ctx, &mcp.SubscribeParams{URI: "whatsapp://chats"}); err != nil {
		t.Fatal(err)
	}
	// Under the newest protocol Subscribe opens a subscriptions/listen
	// stream in the background; publish until it is in place.
	deadline := time.After(5 * time.Second)
	for {
		s.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: "whatsapp://chats"})
		select {
		case uri := <-updated:
			if uri != "whatsapp://chats" {
				t.Errorf("got %s", uri)
			}
			return
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("no update")
		}
	}
}
