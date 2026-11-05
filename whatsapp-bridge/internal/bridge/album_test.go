package bridge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa/watest"
)

// newAlbumBridge is an online bridge that does not pause between the
// messages of an album.
func newAlbumBridge(t *testing.T, opts ...func(*Deps)) *testBridge {
	t.Helper()
	return newOnlineBridge(t, append([]func(*Deps){func(d *Deps) { d.AlbumGap = -1 }}, opts...)...)
}

// albumFiles writes files into the media root and returns their paths.
func albumFiles(t *testing.T, b *testBridge, names ...string) []string {
	t.Helper()
	var paths []string
	for _, name := range names {
		paths = append(paths, writeFile(t, b, name, []byte("data of "+name)))
	}
	return paths
}

func TestSendAlbum(t *testing.T) {
	t.Parallel()
	b := newAlbumBridge(t)
	sub := b.events.Subscribe()
	paths := albumFiles(t, b, "a.jpg", "b.mp4", "c.PNG", "d.mov")
	resp := b.SendAlbum(t.Context(), AlbumRequest{Recipient: "+2000", MediaPaths: paths, Caption: "site photos"})
	if !resp.Success || resp.MessageID != "SENT1" || !resp.Timestamp.Equal(watest.SentAt) ||
		resp.Message != "Album of 4 items sent to +2000 (album id SENT1; item ids SENT2, SENT3, SENT4, SENT5)" {
		t.Fatalf("%+v", resp)
	}

	// Every file is uploaded before the first message goes out.
	calls := strings.Join(b.fake.Calls(), " ")
	if !strings.Contains(calls, "Upload Upload Upload Upload SendMessage SendMessage SendMessage SendMessage SendMessage") {
		t.Errorf("calls %s", calls)
	}

	sent := b.fake.Sent()
	if len(sent) != 5 {
		t.Fatalf("sent %d messages", len(sent))
	}
	album := sent[0].Message.GetAlbumMessage()
	if sent[0].To != alicePN || album.GetExpectedImageCount() != 2 || album.GetExpectedVideoCount() != 2 {
		t.Errorf("album message %+v to %v", album, sent[0].To)
	}
	if album.GetContextInfo() != nil || sent[0].Message.GetMessageContextInfo() != nil {
		t.Errorf("the album message carries more than its counts: %v", sent[0].Message)
	}

	wantKinds := []string{"image", "video", "image", "video"}
	wantMIME := []string{"image/jpeg", "video/mp4", "image/png", "video/quicktime"}
	for i, s := range sent[1:] {
		m := s.Message
		assoc := m.GetMessageContextInfo().GetMessageAssociation()
		key := assoc.GetParentMessageKey()
		if s.To != alicePN || assoc.GetAssociationType() != waE2E.MessageAssociation_MEDIA_ALBUM ||
			key.GetID() != "SENT1" || key.GetRemoteJID() != aliceChat || !key.GetFromMe() || key.GetParticipant() != "" {
			t.Errorf("item %d association %v to %v", i, assoc, s.To)
		}
		var caption, mime, path string
		var kind string
		switch {
		case m.GetImageMessage() != nil:
			kind, caption, mime, path = "image", m.GetImageMessage().GetCaption(), m.GetImageMessage().GetMimetype(), m.GetImageMessage().GetDirectPath()
		case m.GetVideoMessage() != nil:
			kind, caption, mime, path = "video", m.GetVideoMessage().GetCaption(), m.GetVideoMessage().GetMimetype(), m.GetVideoMessage().GetDirectPath()
		}
		wantCaption := ""
		if i == 0 {
			wantCaption = "site photos"
		}
		if kind != wantKinds[i] || mime != wantMIME[i] || caption != wantCaption || path == "" {
			t.Errorf("item %d: %s %s %q %q", i, kind, mime, caption, path)
		}
	}

	// The items are stored and published like sent media; the album message has no row.
	for i, id := range []string{"SENT2", "SENT3", "SENT4", "SENT5"} {
		r := ref(t, b, id, aliceChat)
		if !r.IsFromMe || r.MediaType != wantKinds[i] {
			t.Errorf("stored %s: %+v", id, r)
		}
		if evt := nextEvent(t, sub); evt.Type != "message" || evt.MessageID != id || evt.MediaType != wantKinds[i] || !evt.IsFromMe {
			t.Errorf("event %+v", evt)
		}
	}
	if r := ref(t, b, "SENT2", aliceChat); r.Content != "site photos" {
		t.Errorf("caption stored as %q", r.Content)
	}
	if got := queryString(t, b, "SELECT count(*) FROM messages WHERE id = 'SENT1'"); got != "0" {
		t.Errorf("album message stored: %s", got)
	}
	noEvent(t, sub)
}

func TestSendAlbumToGroupWithoutCaption(t *testing.T) {
	t.Parallel()
	b := newAlbumBridge(t)
	paths := albumFiles(t, b, "a.webp", "b.jpeg")
	resp := b.SendAlbum(t.Context(), AlbumRequest{Recipient: opsGroup, MediaPaths: paths})
	if !resp.Success {
		t.Fatalf("%+v", resp)
	}
	sent := b.fake.Sent()
	key := sent[1].Message.GetMessageContextInfo().GetMessageAssociation().GetParentMessageKey()
	if sent[0].Message.GetAlbumMessage().GetExpectedVideoCount() != 0 || sent[1].To != groupJID || key.GetRemoteJID() != opsGroup || key.GetParticipant() != "" {
		t.Errorf("sent %+v key %v", sent[1], key)
	}
	for _, s := range sent[1:] {
		if s.Message.GetImageMessage().Caption != nil {
			t.Errorf("caption %q set without one", s.Message.GetImageMessage().GetCaption())
		}
	}
}

func TestSendAlbumPausesBetweenMessages(t *testing.T) {
	t.Parallel()
	const gap = 40 * time.Millisecond
	b := newOnlineBridge(t, func(d *Deps) { d.AlbumGap = gap })
	paths := albumFiles(t, b, "a.jpg", "b.jpg", "c.jpg")
	start := time.Now()
	if resp := b.SendAlbum(t.Context(), AlbumRequest{Recipient: aliceChat, MediaPaths: paths}); !resp.Success {
		t.Fatalf("%+v", resp)
	}
	// One pause before each of the three items.
	if elapsed := time.Since(start); elapsed < 3*gap {
		t.Errorf("album took %v, want at least %v", elapsed, 3*gap)
	}
}

func TestSendAlbumRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		req     func(t *testing.T, b *testBridge) AlbumRequest
		opts    []func(*Deps)
		want    string
		refused bool
	}{
		{"one file", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg")}
		}, nil, "An album takes 2 to 10 files, got 1 (use send_file for a single file)", false},
		{"no files", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: aliceChat}
		}, nil, "An album takes 2 to 10 files, got 0", false},
		{"eleven files", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			var names []string
			for i := range 11 {
				names = append(names, fmt.Sprintf("%d.jpg", i))
			}
			return AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, names...)}
		}, nil, "An album takes 2 to 10 files, got 11", false},
		{"bad recipient", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: "not a jid!", MediaPaths: albumFiles(t, b, "a.jpg", "b.jpg")}
		}, nil, "Error parsing recipient", false},
		{"status", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: "status@broadcast", MediaPaths: albumFiles(t, b, "a.jpg", "b.jpg")}
		}, nil, "An album cannot be posted as a status", false},
		{"gif", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg", "b.gif")}
		}, nil, "b.gif cannot be part of an album: only JPEG, PNG and WebP images and MP4, MOV and 3GP videos are allowed", false},
		{"document", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg", "report.pdf")}
		}, nil, "report.pdf cannot be part of an album", false},
		{"no extension", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg", "photo")}
		}, nil, "photo cannot be part of an album", false},
		{"missing file", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: aliceChat, MediaPaths: []string{albumFiles(t, b, "a.jpg")[0], filepath.Join(b.dir, "outbox", "gone.jpg")}}
		}, nil, "media file not found", false},
		{"directory", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			dir := albumFiles(t, b, "a.jpg")[0] + ".dir.jpg"
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			return AlbumRequest{Recipient: aliceChat, MediaPaths: []string{albumFiles(t, b, "b.jpg")[0], dir}}
		}, nil, "media path is not a regular file", false},
		{"outside the media roots", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			outside := filepath.Join(t.TempDir(), "b.jpg")
			if err := os.WriteFile(outside, []byte("x"), 0600); err != nil {
				t.Fatal(err)
			}
			return AlbumRequest{Recipient: aliceChat, MediaPaths: []string{albumFiles(t, b, "a.jpg")[0], outside}}
		}, nil, "outside the media roots", true},
		{"store secret", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			// Files directly in the store directory hold the session and token.
			return AlbumRequest{Recipient: aliceChat, MediaPaths: []string{albumFiles(t, b, "a.jpg")[0], writeStoreFile(t, b, "api_token.jpg")}}
		}, nil, "refusing to send", false},
		{"not in the send allowlist", func(t *testing.T, b *testBridge) AlbumRequest {
			t.Helper()
			return AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg", "b.jpg")}
		}, []func(*Deps){allowSend(t, "9999")}, "is not in the send allowlist", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newAlbumBridge(t, c.opts...)
			resp := b.SendAlbum(t.Context(), c.req(t, b))
			if resp.Success || resp.Refused != c.refused || !strings.Contains(resp.Message, c.want) {
				t.Errorf("%+v, want %q (refused %v)", resp, c.want, c.refused)
			}
			// Nothing was uploaded or sent.
			if calls := b.fake.Calls(); len(calls) != 0 {
				t.Errorf("calls %v", calls)
			}
		})
	}
}

func TestSendAlbumOffline(t *testing.T) {
	t.Parallel()
	b := newAlbumBridge(t)
	b.fake.Connected = false
	resp := b.SendAlbum(t.Context(), AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg", "b.jpg")})
	if resp.Success || resp.Message != "Not connected to WhatsApp" || len(b.fake.Calls()) != 0 {
		t.Errorf("%+v %v", resp, b.fake.Calls())
	}
}

func TestSendAlbumUploadFailureSendsNothing(t *testing.T) {
	t.Parallel()
	b := newAlbumBridge(t)
	var uploads atomic.Int32
	b.fake.UploadFunc = func(context.Context, []byte, whatsmeow.MediaType) (whatsmeow.UploadResponse, error) {
		if uploads.Add(1) == 3 {
			return whatsmeow.UploadResponse{}, errors.New("cdn down")
		}
		return whatsmeow.UploadResponse{URL: "u", DirectPath: "/p"}, nil
	}
	resp := b.SendAlbum(t.Context(), AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg", "b.jpg", "c.jpg", "d.jpg")})
	if resp.Success || resp.MessageID != "" || resp.Message != "Error uploading c.jpg (nothing was sent): cdn down" {
		t.Errorf("%+v", resp)
	}
	if len(b.fake.Sent()) != 0 || uploads.Load() != 3 {
		t.Errorf("sent %d, uploads %d", len(b.fake.Sent()), uploads.Load())
	}
}

func TestSendAlbumParentFailureSendsNoItems(t *testing.T) {
	t.Parallel()
	b := newAlbumBridge(t)
	b.fake.SendMessageFunc = func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		return whatsmeow.SendResponse{}, errors.New("socket closed")
	}
	resp := b.SendAlbum(t.Context(), AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg", "b.jpg")})
	if resp.Success || resp.MessageID != "" || resp.Message != "Error sending album (nothing was delivered): socket closed" {
		t.Errorf("%+v", resp)
	}
	if got := queryString(t, b, "SELECT count(*) FROM messages"); got != "0" {
		t.Errorf("%s rows stored", got)
	}
}

func TestSendAlbumStopsAtFirstFailedItem(t *testing.T) {
	t.Parallel()
	b := newAlbumBridge(t)
	var sends atomic.Int32
	b.fake.SendMessageFunc = func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		n := sends.Add(1)
		if n == 4 { // the album message and two items went out; the third item fails
			return whatsmeow.SendResponse{}, errors.New("timed out")
		}
		return whatsmeow.SendResponse{ID: fmt.Sprintf("M%d", n), Timestamp: watest.SentAt}, nil
	}
	resp := b.SendAlbum(t.Context(), AlbumRequest{Recipient: aliceChat, Caption: "cap", MediaPaths: albumFiles(t, b, "a.jpg", "b.jpg", "c.jpg", "d.jpg", "e.jpg")})
	want := "Album to " + aliceChat + " is incomplete: 2 of 5 items delivered. Album id M1; delivered items: M2 (a.jpg), M3 (b.jpg). " +
		"Item 3 (c.jpg) failed: timed out. The remaining 2 were not sent and nothing was retracted; delete_message removes what was delivered."
	if resp.Success || resp.MessageID != "M1" || resp.Message != want {
		t.Errorf("%+v\nwant %s", resp, want)
	}
	// The album message and two items were accepted; nothing after the failure was tried.
	if len(b.fake.Sent()) != 3 || sends.Load() != 4 {
		t.Errorf("sent %d, attempts %d", len(b.fake.Sent()), sends.Load())
	}
	// What was delivered is in the history, the rest is not.
	for _, id := range []string{"M2", "M3"} {
		ref(t, b, id, aliceChat)
	}
	if got := queryString(t, b, "SELECT count(*) FROM messages"); got != "2" {
		t.Errorf("%s rows stored", got)
	}
}

func TestSendAlbumStopsWhenCancelled(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t, func(d *Deps) { d.AlbumGap = time.Hour })
	ctx, cancel := context.WithCancel(t.Context())
	b.fake.SendMessageFunc = func(context.Context, types.JID, *waE2E.Message) (whatsmeow.SendResponse, error) {
		cancel() // the caller gives up right after the album message went out
		return whatsmeow.SendResponse{ID: "P1", Timestamp: watest.SentAt}, nil
	}
	resp := b.SendAlbum(ctx, AlbumRequest{Recipient: aliceChat, MediaPaths: albumFiles(t, b, "a.jpg", "b.jpg")})
	if resp.Success || resp.MessageID != "P1" || !strings.Contains(resp.Message, "0 of 2 items delivered") ||
		!strings.Contains(resp.Message, "delivered items: none") || !strings.Contains(resp.Message, "context canceled") {
		t.Errorf("%+v", resp)
	}
	if len(b.fake.Sent()) != 1 {
		t.Errorf("sent %d", len(b.fake.Sent()))
	}
}
