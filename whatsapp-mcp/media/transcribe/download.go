package transcribe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var sha256Re = regexp.MustCompile(`^[0-9a-f]{64}$`)

// download fetches url to path unless path exists. It writes to a temporary
// file in the same directory and renames it at the end, so an interrupted
// download (the "small" model is 466 MiB) never leaves a truncated model that
// whisper-cli would choke on later. Hugging Face names the SHA-256 of an LFS
// file in X-Linked-Etag on its redirect; when present it is checked.
func download(ctx context.Context, url, path string) error {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 0 {
		return nil
	}
	// The models are the user's own cache, like the transcripts: private.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var want string
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		if req.Response != nil && want == "" {
			want = linkedSHA256(req.Response.Header)
		}
		return nil
	}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: %s", url, resp.Status)
	}
	if want == "" {
		want = linkedSHA256(resp.Header)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}
	if resp.ContentLength > 0 && n != resp.ContentLength {
		return fmt.Errorf("download %s: got %d of %d bytes", url, n, resp.ContentLength)
	}
	if n == 0 {
		return fmt.Errorf("download %s: empty file", url)
	}
	if got := hex.EncodeToString(h.Sum(nil)); want != "" && got != want {
		return fmt.Errorf("download %s: sha256 %s, want %s", url, got, want)
	}
	return os.Rename(tmp.Name(), path)
}

func linkedSHA256(h http.Header) string {
	etag := strings.Trim(h.Get("X-Linked-Etag"), `"`)
	if sha256Re.MatchString(etag) {
		return etag
	}
	return ""
}
