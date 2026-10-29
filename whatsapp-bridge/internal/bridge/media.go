package bridge

import (
	"context"
	"fmt"
)

// DownloadMedia saves a message's attachment under store/<chat>/ and
// returns its absolute path. Files already downloaded are returned as they
// are. The caller checks the read allowlist.
func (b *Bridge) DownloadMedia(ctx context.Context, messageID, chatJID string) (*DownloadMediaResponse, error) {
	f, err := b.media.Download(ctx, messageID, chatJID)
	if err != nil {
		return nil, err
	}
	return &DownloadMediaResponse{
		Success: true, Filename: f.Name, Path: f.Path, MediaType: f.MediaType, Mimetype: f.Mimetype,
		Message: fmt.Sprintf("Successfully downloaded %s media", f.MediaType),
	}, nil
}
