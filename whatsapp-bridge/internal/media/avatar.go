package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
)

// maxAvatar bounds a profile picture download; WhatsApp serves them at a
// few hundred kilobytes.
const maxAvatar = 10 << 20

// AvatarPath is where a profile picture is cached, under DefaultRoot.
func AvatarPath(jid types.JID) string {
	return avatarPathIn(DefaultRoot, jid)
}

func avatarPathIn(root string, jid types.JID) string {
	return filepath.Join(root, "avatars", content.SafeFileName(jid.ToNonAD().String())+".jpg")
}

// Avatar fetches a profile or group picture into store/avatars/ and
// returns its absolute path.
func (s *Service) Avatar(ctx context.Context, jid types.JID) (string, error) {
	info, err := s.Client.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{})
	if err != nil {
		return "", err
	}
	if info == nil || info.URL == "" {
		return "", fmt.Errorf("no profile picture, or it is hidden from you")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("picture download answered HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatar))
	if err != nil {
		return "", err
	}
	path := avatarPathIn(s.root(), jid)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {

		return "", err
	}
	abs, _ := filepath.Abs(path)
	return abs, nil
}
