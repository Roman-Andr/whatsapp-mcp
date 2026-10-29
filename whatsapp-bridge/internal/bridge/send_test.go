package bridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa/watest"
)

// writeFile creates a file in the bridge's media root.
func writeFile(t *testing.T, b *testBridge, name string, data []byte) string {
	t.Helper()
	dir := filepath.Join(b.dir, "outbox")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSendText(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	sub := b.events.Subscribe()
	resp := b.Send(t.Context(), SendMessageRequest{Recipient: "+2000", Message: "plows out at 5"})
	if !resp.Success || resp.Message != "Message sent to +2000" || resp.MessageID != "SENT1" || !resp.Timestamp.Equal(watest.SentAt) {
		t.Fatalf("%+v", resp)
	}
	sent := b.fake.Sent()
	if len(sent) != 1 || sent[0].To != alicePN || sent[0].Message.GetConversation() != "plows out at 5" {
		t.Errorf("sent %+v", sent)
	}
	// recordSent: the message is in the history as ours, and published.
	r := ref(t, b, "SENT1", aliceChat)
	if !r.IsFromMe || r.Content != "plows out at 5" || r.SenderJID != "1000@s.whatsapp.net" {
		t.Errorf("stored %+v", r)
	}
	if name := b.store.ChatName(t.Context(), aliceChat); name != "Alice Full" {
		t.Errorf("chat name %q", name)
	}
	if evt := nextEvent(t, sub); evt.Type != "message" || !evt.IsFromMe || evt.MessageID != "SENT1" || evt.ChatJID != aliceChat {
		t.Errorf("event %+v", evt)
	}
}

func TestSendReplyWithQuoteAndMentions(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	b.fake.Groups[groupJID] = &types.GroupInfo{JID: groupJID, AddressingMode: types.AddressingModeLID}
	mustStore(t, b.store, store.Message{ID: "Q1", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "where are you?", Timestamp: time.Now()})
	resp := b.Send(t.Context(), SendMessageRequest{
		Recipient: opsGroup, Message: "on my way @2000", ReplyToMessageID: "Q1", Mentions: []string{"2000"},
	})
	if !resp.Success {
		t.Fatalf("%+v", resp)
	}
	ext := b.fake.Sent()[0].Message.GetExtendedTextMessage()
	ci := ext.GetContextInfo()
	// In a LID group the mention names the LID, in the text too.
	if ext.GetText() != "on my way @8000" || strings.Join(ci.GetMentionedJID(), ",") != "8000@lid" {
		t.Errorf("text %q mentions %v", ext.GetText(), ci.GetMentionedJID())
	}
	if ci.GetStanzaID() != "Q1" || ci.GetParticipant() != "8000@lid" || ci.GetQuotedMessage().GetConversation() != "where are you?" {
		t.Errorf("quote %+v", ci)
	}
	if got := queryString(t, b, "SELECT quoted_id || '|' || quoted_text || '|' || mentions FROM messages WHERE id = 'SENT1'"); got != `Q1|where are you?|["8000@lid"]` {
		t.Errorf("stored %s", got)
	}
}

func TestSendFiles(t *testing.T) {
	t.Parallel()
	webp := []byte("RIFF....WEBPVP8 ")
	cases := []struct {
		name, file string
		data       []byte
		sticker    bool
		check      func(*waE2E.Message) bool
		media      string
	}{
		{"document", "report.pdf", []byte("%PDF-1.4"), false, func(m *waE2E.Message) bool {
			d := m.GetDocumentMessage()
			return d.GetFileName() == "report.pdf" && d.GetCaption() == "see attached" && d.GetDirectPath() != ""
		}, "document"},
		{"image", "plow.jpg", []byte("\xff\xd8\xff"), false, func(m *waE2E.Message) bool {
			return m.GetImageMessage().GetCaption() == "see attached" && m.GetImageMessage().GetMimetype() == "image/jpeg"
		}, "image"},
		{"video", "clip.mp4", []byte("....ftyp"), false, func(m *waE2E.Message) bool {
			return m.GetVideoMessage().GetCaption() == "see attached"
		}, "video"},
		{"sticker", "smile.webp", webp, true, func(m *waE2E.Message) bool {
			return m.GetStickerMessage().GetMimetype() == "image/webp" && len(m.GetStickerMessage().GetMediaKey()) > 0
		}, "sticker"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			path := writeFile(t, b, c.file, c.data)
			resp := b.Send(t.Context(), SendMessageRequest{Recipient: aliceChat, Message: "see attached", MediaPath: path, Sticker: c.sticker})
			if !resp.Success {
				t.Fatalf("%+v", resp)
			}
			if m := b.fake.Sent()[0].Message; !c.check(m) {
				t.Errorf("message %v", m)
			}
			if r := ref(t, b, "SENT1", aliceChat); r.MediaType != c.media {
				t.Errorf("stored media %q", r.MediaType)
			}
		})
	}
}

func TestSendVoiceNote(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	head := append([]byte("OggS"), make([]byte, 23)...)
	head[26] = 1
	head = append(append(head, 8), []byte("OpusHead")...)
	path := writeFile(t, b, "note.ogg", append(head, make([]byte, 100)...))
	resp := b.Send(t.Context(), SendMessageRequest{Recipient: aliceChat, MediaPath: path})
	if !resp.Success {
		t.Fatalf("%+v", resp)
	}
	if audio := b.fake.Sent()[0].Message.GetAudioMessage(); !audio.GetPTT() || audio.GetSeconds() == 0 || len(audio.GetWaveform()) != 64 {
		t.Errorf("audio %+v", audio)
	}
}

func TestSendRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		opts    []func(*Deps)
		offline bool
		req     func(b *testBridge) SendMessageRequest
		want    string
		refused bool
	}{
		{"bad recipient", nil, false, func(*testBridge) SendMessageRequest { return SendMessageRequest{Recipient: "a.b.c@s.whatsapp.net"} }, "Error parsing recipient: ", false},
		{"not allowed", []func(*Deps){allowSend(t, opsGroup)}, false, func(*testBridge) SendMessageRequest { return SendMessageRequest{Recipient: aliceChat} }, "chat 2000@s.whatsapp.net is not in the send allowlist", true},
		{"outside the media roots", nil, false, func(*testBridge) SendMessageRequest {
			return SendMessageRequest{Recipient: aliceChat, MediaPath: "/etc/hostname"}
		}, "", true},
		{"offline", nil, true, func(*testBridge) SendMessageRequest { return SendMessageRequest{Recipient: aliceChat} }, "Not connected to WhatsApp", false},
		{"unknown reply", nil, false, func(*testBridge) SendMessageRequest {
			return SendMessageRequest{Recipient: aliceChat, ReplyToMessageID: "NOPE"}
		}, "Message to reply to not found: NOPE", false},
		{"sticker that is not webp", nil, false, func(b *testBridge) SendMessageRequest {
			return SendMessageRequest{Recipient: aliceChat, Sticker: true, MediaPath: writeFile(t, b, "x.png", []byte("png"))}
		}, "stickers must be .webp files", false},
		{"bad mention", nil, false, func(*testBridge) SendMessageRequest {
			return SendMessageRequest{Recipient: aliceChat, Message: "hi", Mentions: []string{"a.b.c@s.whatsapp.net"}}
		}, "Error resolving mentions: ", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t, c.opts...)
			if c.offline {
				b.fake.Connected = false
			}
			resp := b.Send(t.Context(), c.req(b))
			if resp.Success || resp.Refused != c.refused || !strings.HasPrefix(resp.Message, c.want) {
				t.Errorf("%+v, want %q (refused %v)", resp, c.want, c.refused)
			}
			if len(b.fake.Sent()) != 0 {
				t.Error("sent anyway")
			}
		})
	}
	t.Run("send fails", func(t *testing.T) {
		t.Parallel()
		b := newOnlineBridge(t)
		b.fake.SendMessageFunc = func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
			return whatsmeow.SendResponse{}, errors.New("timed out")
		}
		if resp := b.Send(t.Context(), SendMessageRequest{Recipient: aliceChat, Message: "x"}); resp.Message != "Error sending message: timed out" {
			t.Errorf("%+v", resp)
		}
	})
}

func TestTextStatus(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	if resp := b.Send(t.Context(), SendMessageRequest{Recipient: types.StatusBroadcastJID.String(), Message: "Plows out at 5"}); !resp.Success {
		t.Fatalf("%+v", resp)
	}
	ext := b.fake.Sent()[0].Message.GetExtendedTextMessage()
	if ext.GetText() != "Plows out at 5" || ext.GetBackgroundArgb() != statusBackground || ext.GetTextArgb() != statusText {
		t.Errorf("%+v", ext)
	}
}

func TestRequestHistory(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	old := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	mustStore(t, b.store, store.Message{ID: "OLD", ChatJID: opsGroup, Sender: "8000", Content: "first", Timestamp: old})
	mustStore(t, b.store, store.Message{ID: "NEW", ChatJID: opsGroup, Sender: "8000", Content: "later", Timestamp: old.Add(time.Hour)})
	cases := []struct {
		name  string
		req   HistoryRequest
		count int32
		want  string
	}{
		{"from the oldest", HistoryRequest{ChatJID: opsGroup}, defaultHistoryCount, "Requested up to 50 messages before 2025-01-02T03:04:05Z (OLD)."},
		{"before a message, capped", HistoryRequest{ChatJID: opsGroup, BeforeMessageID: "NEW", Count: 9999}, maxHistoryCount, "Requested up to 500 messages before 2025-01-02T04:04:05Z (NEW)."},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := b.RequestHistory(t.Context(), c.req)
			if !resp.Success || !strings.HasPrefix(resp.Message, c.want) {
				t.Errorf("%+v", resp)
			}
			sent := b.fake.Sent()
			req := sent[len(sent)-1].Message.GetProtocolMessage().GetPeerDataOperationRequestMessage().GetHistorySyncOnDemandRequest()
			if req.GetOnDemandMsgCount() != c.count || req.GetChatJID() != opsGroup {
				t.Errorf("request %+v", req)
			}
		})
	}
	refusals := []struct {
		name string
		req  HistoryRequest
		want string
	}{
		{"unknown message", HistoryRequest{ChatJID: opsGroup, BeforeMessageID: "NOPE"}, "Message NOPE not found in 120@g.us"},
		{"empty chat", HistoryRequest{ChatJID: testGroup}, "No stored messages for g@g.us to page back from"},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			if resp := b.RequestHistory(t.Context(), c.req); resp.Success || resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
		})
	}
	if resp := newTestBridge(t).RequestHistory(t.Context(), HistoryRequest{ChatJID: opsGroup}); resp.Message != "Not connected to WhatsApp" {
		t.Errorf("offline %+v", resp)
	}
}

func TestReact(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	mustStore(t, b.store, store.Message{ID: "M1", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "done", Timestamp: time.Now()})
	cases := []struct{ name, emoji, want string }{
		{"react", "👍", "Reacted 👍 on M1"},
		{"remove", "", "Removed reaction on M1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resp := b.React(t.Context(), ReactRequest{ChatJID: opsGroup, MessageID: "M1", Emoji: c.emoji})
			if !resp.Success || resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
			if got := queryString(t, b, "SELECT emoji FROM reactions WHERE message_id = 'M1'"); got != c.emoji {
				t.Errorf("stored %q", got)
			}
		})
	}
	if resp := b.React(t.Context(), ReactRequest{ChatJID: opsGroup, MessageID: "NOPE"}); resp.Message != "Message NOPE not found in chat 120@g.us" {
		t.Errorf("%+v", resp)
	}
}

func TestAuthorJID(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	pnGroup := types.NewJID("555", types.GroupServer)
	b.fake.Groups[pnGroup] = &types.GroupInfo{JID: pnGroup, AddressingMode: types.AddressingModePN}
	cases := []struct {
		name string
		ref  store.MessageRef
		want string // JID; "" for an error
	}{
		{"ours in a LID chat", store.MessageRef{ChatJID: "8000@lid", IsFromMe: true}, "9000@lid"},
		{"ours", store.MessageRef{ChatJID: aliceChat, IsFromMe: true}, "1000@s.whatsapp.net"},
		{"recorded sender", store.MessageRef{ChatJID: opsGroup, SenderJID: "8000@lid"}, "8000@lid"},
		{"direct chat", store.MessageRef{ChatJID: aliceChat}, aliceChat},
		{"legacy row in a LID group", store.MessageRef{ChatJID: opsGroup, Sender: "8000"}, "8000@lid"},
		{"legacy row in a phone-number group", store.MessageRef{ChatJID: "555@g.us", Sender: "2000"}, aliceChat},
		{"sender not recorded", store.MessageRef{ID: "X", ChatJID: opsGroup}, ""},
		{"bad chat", store.MessageRef{ChatJID: "a.b.c@s.whatsapp.net"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := b.authorJID(t.Context(), &c.ref)
			if c.want == "" {
				if err == nil {
					t.Errorf("got %s", got)
				}
				return
			}
			if err != nil || got.String() != c.want {
				t.Errorf("got %s, %v", got, err)
			}
		})
	}
}

func TestStoreSecretsCannotBeSent(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	storeDir := b.dir
	if err := os.MkdirAll(filepath.Join(storeDir, "120@g.us"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"whatsapp.db", "api_token", "120@g.us/photo.jpg"} {
		if err := os.WriteFile(filepath.Join(storeDir, f), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "report.pdf")
	if err := os.WriteFile(outside, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	db := filepath.Join(storeDir, "whatsapp.db")
	link := filepath.Join(t.TempDir(), "innocent.pdf")
	if err := os.Symlink(db, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		path   string
		secret bool
	}{
		{"session database", db, true},
		{"symlink to it", link, true},
		{"missing file", filepath.Join(storeDir, "gone"), true},
		{"file elsewhere", outside, false},
		{"downloaded media", filepath.Join(storeDir, "120@g.us", "photo.jpg"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := isStoreSecret(storeDir, c.path); got != c.secret {
				t.Errorf("isStoreSecret(%s) = %v", c.path, got)
			}
		})
	}
	if _, err := b.buildMediaMessage(t.Context(), nil, db, "", nil); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("err %v", err)
	}
}
