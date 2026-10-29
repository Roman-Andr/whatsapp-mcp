package bridge

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/util/cbcutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func TestDownloadMedia(t *testing.T) {
	t.Parallel()
	plaintext := []byte("jpeg bytes")
	mediaKey := bytes.Repeat([]byte{7}, 32)
	expanded := hkdfutil.SHA256(mediaKey, nil, []byte(whatsmeow.MediaImage), 112)
	iv, cipherKey, macKey := expanded[:16], expanded[16:48], expanded[48:80]
	ciphertext, err := cbcutil.Encrypt(cipherKey, iv, plaintext)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, macKey)
	mac.Write(iv)
	mac.Write(ciphertext)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(append(ciphertext, mac.Sum(nil)[:10]...))
	}))
	t.Cleanup(cdn.Close)
	sum := sha256.Sum256(plaintext)
	photo := content.MediaInfo{
		Type: "image", Filename: "plow.jpg", Mimetype: "image/jpeg", DirectPath: "/v/x.enc",
		MediaKey: mediaKey, FileSHA256: sum[:], FileEncSHA256: []byte("enc"), FileLength: uint64(len(plaintext)),
	}

	cases := []struct {
		name     string
		download func(context.Context, whatsmeow.DownloadableMessage) ([]byte, error)
	}{
		{"through whatsmeow", func(context.Context, whatsmeow.DownloadableMessage) ([]byte, error) { return plaintext, nil }},
		{"through the CDN after a hash mismatch", func(context.Context, whatsmeow.DownloadableMessage) ([]byte, error) {
			return nil, whatsmeow.ErrInvalidMediaEncSHA256
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			b.media.Host = cdn.URL
			b.fake.DownloadFunc = c.download
			mustStore(t, b.store, store.Message{ID: "IMG", ChatJID: opsGroup, Sender: "8000", Timestamp: time.Now(), Media: photo})
			resp, err := b.DownloadMedia(t.Context(), "IMG", opsGroup)
			if err != nil {
				t.Fatal(err)
			}
			if !resp.Success || resp.Filename != "IMG_plow.jpg" || resp.MediaType != "image" || resp.Message != "Successfully downloaded image media" {
				t.Errorf("%+v", resp)
			}
			if data, _ := os.ReadFile(resp.Path); string(data) != "jpeg bytes" {
				t.Errorf("file %q", data)
			}
		})
	}
	t.Run("unknown message", func(t *testing.T) {
		t.Parallel()
		if _, err := newOnlineBridge(t).DownloadMedia(t.Context(), "NOPE", opsGroup); err == nil || !strings.HasPrefix(err.Error(), "failed to find message") {
			t.Errorf("err %v", err)
		}
	})
}
