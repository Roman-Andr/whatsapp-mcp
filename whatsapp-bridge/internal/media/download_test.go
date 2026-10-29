package media

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/cbcutil"
	"go.mau.fi/whatsmeow/util/gcmutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"
	"google.golang.org/protobuf/proto"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa/watest"
)

const testChat = "c@s.whatsapp.net"

var (
	ownJID = types.NewJID("1000", types.DefaultUserServer)
	ownLID = types.NewJID("9000", types.HiddenUserServer)
)

// newTestService is a media service on a fresh store in a temporary
// directory, with a fake client and an author lookup that names the sender
// by phone number.
func newTestService(t *testing.T) (*Service, *watest.Client) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.SaveChat(t.Context(), testChat, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	fake := watest.New(ownJID, ownLID)
	s := &Service{
		Client: fake, Store: st, Root: dir,
		Author: func(_ context.Context, ref *store.MessageRef) (types.JID, error) {
			return types.NewJID(ref.Sender, types.DefaultUserServer), nil
		},
	}
	return s, fake
}

// encrypted is an attachment as the CDN serves it: ciphertext plus MAC.
type encrypted struct {
	plaintext, mediaKey, body []byte
	sha                       [32]byte
}

// encryptMedia encrypts an image attachment.
func encryptMedia(t *testing.T, plaintext []byte) encrypted {
	t.Helper()
	mediaKey := bytes.Repeat([]byte{7}, 32)
	expanded := hkdfutil.SHA256(mediaKey, nil, []byte(whatsmeow.MediaImage), mediaKeyExpansion)
	iv, cipherKey, macKey := expanded[:16], expanded[16:48], expanded[48:80]
	ciphertext, err := cbcutil.Encrypt(cipherKey, iv, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	h := hmac.New(sha256.New, macKey)
	h.Write(iv)
	h.Write(ciphertext)
	return encrypted{
		plaintext: plaintext, mediaKey: mediaKey,
		body: append(append([]byte{}, ciphertext...), h.Sum(nil)[:macLength]...),
		sha:  sha256.Sum256(plaintext),
	}
}

func saveMedia(t *testing.T, s *Service, id string, m content.MediaInfo) {
	t.Helper()
	if err := s.Store.SaveMessage(t.Context(), store.Message{
		ID: id, ChatJID: testChat, Sender: "2000", Timestamp: time.Now(), Media: m,
	}); err != nil {
		t.Fatal(err)
	}
}

func imageInfo(e encrypted) content.MediaInfo {
	return content.MediaInfo{
		Type: "image", Filename: "photo.jpg", Mimetype: "image/jpeg", DirectPath: "/v/old.enc?oh=sig",
		MediaKey: e.mediaKey, FileSHA256: e.sha[:], FileEncSHA256: []byte("enc"), FileLength: uint64(len(e.plaintext)),
	}
}

// retryAnswer is the phone's encrypted answer to a re-upload request.
func retryAnswer(t *testing.T, id string, chat types.JID, mediaKey []byte, result waMmsRetry.MediaRetryNotification_ResultType, path string) *events.MediaRetry {
	t.Helper()
	plaintext, err := proto.Marshal(&waMmsRetry.MediaRetryNotification{StanzaID: new(id), DirectPath: new(path), Result: result.Enum()})
	if err != nil {
		t.Fatal(err)
	}
	iv := bytes.Repeat([]byte{1}, 12)
	key := hkdfutil.SHA256(mediaKey, nil, []byte("WhatsApp Media Retry Notification"), 32)
	ciphertext, err := gcmutil.Encrypt(key, iv, plaintext, []byte(id))
	if err != nil {
		t.Fatal(err)
	}
	return &events.MediaRetry{Ciphertext: ciphertext, IV: iv, MessageID: id, ChatID: chat}
}

func TestDownloadSavesAndCaches(t *testing.T) {
	t.Parallel()
	s, fake := newTestService(t)
	e := encryptMedia(t, []byte("jpeg bytes"))
	saveMedia(t, s, "M1", imageInfo(e))
	var downloads atomic.Int32
	fake.DownloadFunc = func(_ context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error) {
		downloads.Add(1)
		if d, ok := msg.(*Downloader); !ok || d.MediaType != whatsmeow.MediaImage || d.DirectPath != "/v/old.enc?oh=sig" {
			t.Errorf("downloading %+v", msg)
		}
		return e.plaintext, nil
	}

	for _, pass := range []string{"first", "cached"} {
		f, err := s.Download(t.Context(), "M1", testChat)
		if err != nil {
			t.Fatalf("%s: %v", pass, err)
		}
		want := filepath.Join(s.Root, "c@s.whatsapp.net", "M1_photo.jpg")
		if f.Path != want || f.Name != "M1_photo.jpg" || f.MediaType != "image" || f.Mimetype != "image/jpeg" {
			t.Errorf("%s: %+v, want path %s", pass, f, want)
		}
		if data, err := os.ReadFile(f.Path); err != nil || string(data) != "jpeg bytes" {
			t.Errorf("%s: file %q, %v", pass, data, err)
		}
	}
	if downloads.Load() != 1 {
		t.Errorf("%d downloads, want 1 (the second call is served from disk)", downloads.Load())
	}
}

func TestDownloadRefusals(t *testing.T) {
	t.Parallel()
	s, fake := newTestService(t)
	e := encryptMedia(t, []byte("x"))
	if err := s.Store.SaveMessage(t.Context(), store.Message{ID: "T1", ChatJID: testChat, Content: "text", Timestamp: time.Now()}); err != nil {
		t.Fatal(err)
	}
	incomplete := imageInfo(e)
	incomplete.MediaKey = nil
	saveMedia(t, s, "INC", incomplete)
	odd := imageInfo(e)
	odd.Type = "hologram"
	saveMedia(t, s, "ODD", odd)
	saveMedia(t, s, "FAIL", imageInfo(e))
	fake.DownloadFunc = func(context.Context, whatsmeow.DownloadableMessage) ([]byte, error) {
		return nil, errors.New("connection reset")
	}

	cases := []struct {
		name, id, want string
	}{
		{"unknown message", "NOPE", "failed to find message: "},
		{"text message", "T1", "not a media message"},
		{"incomplete media keys", "INC", "incomplete media information for download"},
		{"unsupported media type", "ODD", "unsupported media type: hologram"},
		{"download error", "FAIL", "failed to download media: connection reset"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := s.Download(t.Context(), c.id, testChat)
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("err %v, want prefix %q", err, c.want)
			}
		})
	}
}

func TestDownloadAsksThePhoneToReuploadExpiredMedia(t *testing.T) {
	t.Parallel()
	chat := types.NewJID("c", types.DefaultUserServer)
	cases := []struct {
		name    string
		result  waMmsRetry.MediaRetryNotification_ResultType
		sendErr error
		want    string // error prefix; "" for success
	}{
		{"phone re-uploads", waMmsRetry.MediaRetryNotification_SUCCESS, nil, ""},
		{"phone lost the file", waMmsRetry.MediaRetryNotification_NOT_FOUND, nil, "failed to download media: download failed with status code 404; re-upload request failed: phone answered NOT_FOUND"},
		{"receipt not sent", 0, errors.New("offline"), "failed to download media: download failed with status code 404; re-upload request failed: failed to send retry receipt: offline"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			s, fake := newTestService(t)
			e := encryptMedia(t, []byte("old photo"))
			saveMedia(t, s, "OLD", imageInfo(e))
			var paths []string
			fake.DownloadFunc = func(_ context.Context, msg whatsmeow.DownloadableMessage) ([]byte, error) {
				paths = append(paths, msg.GetDirectPath())
				if len(paths) == 1 {
					return nil, whatsmeow.ErrMediaDownloadFailedWith404
				}
				return e.plaintext, nil
			}
			fake.MediaRetryFunc = func(_ context.Context, info *types.MessageInfo, key []byte) error {
				if info.ID != "OLD" || info.Chat != chat || info.Sender.User != "2000" || !bytes.Equal(key, e.mediaKey) {
					t.Errorf("retry receipt for %+v", info)
				}
				if c.sendErr != nil {
					return c.sendErr
				}
				// An answer for the same id in another chat is not ours.
				s.HandleRetry(retryAnswer(t, "OLD", types.NewJID("other", types.DefaultUserServer), key, c.result, "/v/wrong.enc"))
				s.HandleRetry(retryAnswer(t, "OLD", chat, key, c.result, "/v/new.enc"))
				return nil
			}

			f, err := s.Download(t.Context(), "OLD", testChat)
			if c.want != "" {
				if err == nil || err.Error() != c.want {
					t.Fatalf("err %v, want %q", err, c.want)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(f.Path); string(data) != "old photo" {
				t.Errorf("file %q", data)
			}
			if len(paths) != 2 || paths[1] != "/v/new.enc" {
				t.Errorf("downloaded from %v", paths)
			}
			stored, err := s.Store.Media(t.Context(), "OLD", testChat)
			if err != nil || stored.DirectPath != "/v/new.enc" {
				t.Errorf("stored path %q, %v", stored.DirectPath, err)
			}
		})
	}
}

func TestReuploadGivesUp(t *testing.T) {
	t.Parallel()
	media := &store.Media{MediaInfo: content.MediaInfo{MediaKey: []byte("k")}, Sender: "2000"}
	newService := func() *Service {
		fake := watest.New(ownJID, ownLID)
		fake.MediaRetryFunc = func(context.Context, *types.MessageInfo, []byte) error { return nil }
		return &Service{Client: fake, Author: func(context.Context, *store.MessageRef) (types.JID, error) {
			return types.NewJID("2000", types.DefaultUserServer), nil
		}}
	}
	t.Run("the phone does not answer", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			_, err := newService().requestReupload(t.Context(), "M1", testChat, media)
			if err == nil || err.Error() != "no answer from the phone within 45s (is it online?)" {
				t.Errorf("err %v", err)
			}
			if waited := time.Since(start); waited != ReuploadTimeout {
				t.Errorf("waited %s", waited)
			}
		})
	})
	t.Run("the caller gives up", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if _, err := newService().requestReupload(ctx, "M1", testChat, media); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("err %v", err)
			}
		})
	})
	t.Run("an unparsable chat", func(t *testing.T) {
		t.Parallel()
		if _, err := newService().requestReupload(t.Context(), "M1", "a.b.c@s.whatsapp.net", media); err == nil {
			t.Error("no error")
		}
	})
}

func TestDownloadVerifiesPlaintextWhenTheCiphertextHashDiffers(t *testing.T) {
	t.Parallel()
	s, fake := newTestService(t)
	e := encryptMedia(t, []byte("re-uploaded photo"))
	saveMedia(t, s, "RE", imageInfo(e))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "https://web.whatsapp.com" {
			t.Errorf("origin %q", r.Header.Get("Origin"))
		}
		w.Write(e.body)
	}))
	defer server.Close()
	s.Host = server.URL
	fake.DownloadFunc = func(context.Context, whatsmeow.DownloadableMessage) ([]byte, error) {
		return nil, whatsmeow.ErrInvalidMediaEncSHA256
	}
	f, err := s.Download(t.Context(), "RE", testChat)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(f.Path); string(data) != "re-uploaded photo" {
		t.Errorf("file %q", data)
	}
}

func TestDownloadVerifyingPlaintext(t *testing.T) {
	t.Parallel()
	e := encryptMedia(t, []byte("screenshot bytes"))
	other := sha256.Sum256([]byte("other"))
	cases := []struct {
		name   string
		serve  []byte
		status int
		path   string
		sha    []byte
		want   string // error prefix; "" for success
	}{
		{"valid file keeps the signature", e.body, http.StatusOK, "/v/f.enc?oh=sig", e.sha[:], ""},
		{"tampered MAC", append(append([]byte{}, e.body[:len(e.body)-macLength]...), make([]byte, macLength)...), http.StatusOK, "/v/f.enc", e.sha[:], "media MAC mismatch"},
		{"other plaintext", e.body, http.StatusOK, "/v/f.enc", other[:], "plaintext hash mismatch"},
		{"no plaintext hash", e.body, http.StatusOK, "/v/f.enc", nil, "no plaintext hash to verify against"},
		{"CDN error", nil, http.StatusNotFound, "/v/f.enc", e.sha[:], "download failed with status code 404"},
		{"truncated file", []byte("short"), http.StatusOK, "/v/f.enc", e.sha[:], "downloaded file too short"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			var gotQuery string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.RawQuery
				w.WriteHeader(c.status)
				w.Write(c.serve)
			}))
			defer server.Close()
			s := &Service{Host: server.URL}
			data, err := s.downloadVerifyingPlaintext(t.Context(), c.path, e.mediaKey, c.sha, whatsmeow.MediaImage)
			if c.want != "" {
				if err == nil || !strings.HasPrefix(err.Error(), c.want) {
					t.Errorf("err %v, want %q", err, c.want)
				}
				return
			}
			if err != nil || string(data) != "screenshot bytes" {
				t.Fatalf("data=%q err=%v", data, err)
			}
			if !strings.HasPrefix(gotQuery, "oh=sig&mms-type=image") {
				t.Errorf("query %q lost the signature", gotQuery)
			}
		})
	}
}

func TestMediaRetryWaitersRouteByChatAndMessageID(t *testing.T) {
	t.Parallel()
	chat := types.NewJID("c", types.DefaultUserServer)
	w := &retryWaiters{}
	ch := w.register(retryKey{chat: chat.String(), id: "M1"})
	w.deliver(&events.MediaRetry{MessageID: "other", ChatID: chat})
	w.deliver(&events.MediaRetry{MessageID: "M1", ChatID: types.NewJID("d", types.DefaultUserServer)})
	w.deliver(&events.MediaRetry{MessageID: "M1", ChatID: chat})
	select {
	case evt := <-ch:
		if evt.MessageID != "M1" || evt.ChatID != chat {
			t.Errorf("got %s in %s", evt.MessageID, evt.ChatID)
		}
	default:
		t.Error("retry answer not delivered")
	}
	w.unregister(retryKey{chat: chat.String(), id: "M1"})
	w.deliver(&events.MediaRetry{MessageID: "M1", ChatID: chat}) // must not panic or block
}

func TestMediaDirStaysInStore(t *testing.T) {
	t.Parallel()
	for _, jid := range []string{"123@g.us", "../../x", "a/b@s.whatsapp.net", "1:2@lid"} {
		t.Run(jid, func(t *testing.T) {
			t.Parallel()
			if dir := Dir(jid); filepath.Dir(dir) != DefaultRoot {
				t.Errorf("Dir(%q) = %q escapes store/", jid, dir)
			}
		})
	}
}

func TestExtractDirectPath(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, url, want string }{
		{"keeps the signature", "https://mmg.whatsapp.net/v/t62/file.enc?ccb=11-4&oh=sig&oe=123", "/v/t62/file.enc?ccb=11-4&oh=sig&oe=123"},
		{"not a CDN URL", "file.enc", "file.enc"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := extractDirectPathFromURL(c.url); got != c.want {
				t.Errorf("got %q", got)
			}
		})
	}
}

func TestMediaFileNameKeepsMessagePrefix(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, id, file, want string }{
		{"sender path stripped", "M1", "x/L1_contract.pdf", "M1_L1_contract.pdf"},
		{"traversal stripped", "../M1", "../../a.pdf", "M1_a.pdf"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := FileName(c.id, c.file); got != c.want {
				t.Errorf("got %q", got)
			}
		})
	}
}

func TestMediaTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		stored string
		wa     whatsmeow.MediaType
		mms    string
	}{
		{"image", whatsmeow.MediaImage, "image"},
		{"sticker", whatsmeow.MediaImage, "image"},
		{"video", whatsmeow.MediaVideo, "video"},
		{"audio", whatsmeow.MediaAudio, "audio"},
		{"document", whatsmeow.MediaDocument, "document"},
	}
	for _, c := range cases {
		t.Run(c.stored, func(t *testing.T) {
			t.Parallel()
			got, err := whatsmeowMediaType(c.stored)
			if err != nil || got != c.wa || mmsTypeFor(got) != c.mms {
				t.Errorf("%s → %s (%v), mms %s", c.stored, got, err, mmsTypeFor(got))
			}
		})
	}
	for _, err := range []error{whatsmeow.ErrMediaDownloadFailedWith403, whatsmeow.ErrMediaDownloadFailedWith404, whatsmeow.ErrMediaDownloadFailedWith410} {
		if !isExpiredMediaError(err) {
			t.Errorf("%v not treated as expired", err)
		}
	}
	if isExpiredMediaError(errors.New("other")) {
		t.Error("any error treated as expired")
	}
}
