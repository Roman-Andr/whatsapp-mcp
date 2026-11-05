package bridge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// An album is one AlbumMessage that names how many images and videos follow,
// then that many ordinary image and video messages, each pointing back at the
// AlbumMessage through MessageContextInfo.MessageAssociation (type
// MEDIA_ALBUM, ParentMessageKey = the AlbumMessage's key). whatsmeow has no
// helper for this; SendMessage passes the protobuf through unchanged.

// Limits of one album. The maximum is this bridge's choice, not the
// protocol's: it keeps the number of messages one call puts on the wire small.
const (
	MinAlbumItems = 2
	MaxAlbumItems = 10
	// DefaultAlbumGap is the pause between the messages of an album, so a
	// call does not turn into a burst (see README, Safety).
	DefaultAlbumGap = 2 * time.Second
)

// albumTypes are the file types an album takes, by extension.
var albumTypes = map[string]struct{ mime, kind string }{
	".jpg": {"image/jpeg", "image"}, ".jpeg": {"image/jpeg", "image"},
	".png": {"image/png", "image"}, ".webp": {"image/webp", "image"},
	".mp4": {"video/mp4", "video"}, ".mov": {"video/quicktime", "video"}, ".3gp": {"video/3gpp", "video"},
}

// albumItem is one file of an album: checked, then uploaded.
type albumItem struct {
	path, name, mime, kind string
	upload                 whatsmeow.UploadResponse
}

// SendAlbum sends several pictures and videos as one album. Everything that
// can be checked is checked, and every file uploaded, before the first
// message goes out. After that the AlbumMessage and then the items are sent
// in order, and the first failure stops the album: what was delivered stays
// (retracting would mean more sends) and the answer says exactly what.
func (b *Bridge) SendAlbum(ctx context.Context, req AlbumRequest) SendMessageResponse {
	chat, items, refused := b.checkAlbum(ctx, req)
	if refused != nil {
		return *refused
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	if failure := b.uploadAlbum(ctx, items); failure != nil {
		return *failure
	}
	return b.deliverAlbum(ctx, chat, req, items)
}

// checkAlbum validates a request without touching WhatsApp: recipient,
// send allowlist, item count, every file's place, kind and type. It returns
// the chat and the items, or the response that refuses the call.
func (b *Bridge) checkAlbum(ctx context.Context, req AlbumRequest) (types.JID, []albumItem, *SendMessageResponse) {
	refuse := func(resp SendMessageResponse) (types.JID, []albumItem, *SendMessageResponse) {
		return types.EmptyJID, nil, &resp
	}
	chat, err := jid.ParseRecipient(req.Recipient)
	if err != nil {
		return refuse(failed("Error parsing recipient: %v", err))
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return refuse(blockedResponse(err))
	}
	if chat == types.StatusBroadcastJID {
		return refuse(failed("An album cannot be posted as a status"))
	}
	if n := len(req.MediaPaths); n < MinAlbumItems || n > MaxAlbumItems {
		return refuse(failed("An album takes %d to %d files, got %d (use send_file for a single file)", MinAlbumItems, MaxAlbumItems, n))
	}
	items := make([]albumItem, len(req.MediaPaths))
	for i, path := range req.MediaPaths {
		if path, err = b.mediaRoots.Resolve(path); err != nil {
			if errors.Is(err, policy.ErrOutsideMediaRoots) {
				return refuse(blockedResponse(err))
			}
			return refuse(failed("%v", err))
		}
		typ, ok := albumTypes[strings.ToLower(filepath.Ext(path))]
		if !ok {
			return refuse(failed("%s cannot be part of an album: only JPEG, PNG and WebP images and MP4, MOV and 3GP videos are allowed", filepath.Base(path)))
		}
		if err := b.checkMediaFile(path); err != nil {
			return refuse(failed("%v", err))
		}
		items[i] = albumItem{path: path, name: filepath.Base(path), mime: typ.mime, kind: typ.kind}
	}
	return chat, items, nil
}

// uploadAlbum uploads every item, one after the other, keeping only what the
// messages need. A failure here means nothing has been sent yet.
func (b *Bridge) uploadAlbum(ctx context.Context, items []albumItem) *SendMessageResponse {
	for i := range items {
		data, err := b.readMediaFile(items[i].path)
		if err != nil {
			resp := failed("%s: %v", items[i].name, err)
			return &resp
		}
		mediaType := whatsmeow.MediaImage
		if items[i].kind == "video" {
			mediaType = whatsmeow.MediaVideo
		}
		if items[i].upload, err = b.client.Upload(ctx, data, mediaType); err != nil {
			resp := failed("Error uploading %s (nothing was sent): %v", items[i].name, err)
			return &resp
		}
	}
	return nil
}

// deliverAlbum sends the album message, then the items in order, a pause
// apart, and records each delivered item like any sent media message.
func (b *Bridge) deliverAlbum(ctx context.Context, chat types.JID, req AlbumRequest, items []albumItem) SendMessageResponse {
	var images, videos uint32
	for _, item := range items {
		if item.kind == "image" {
			images++
		} else {
			videos++
		}
	}
	parent, err := b.client.SendMessage(ctx, chat, &waE2E.Message{AlbumMessage: &waE2E.AlbumMessage{
		ExpectedImageCount: &images, ExpectedVideoCount: &videos,
	}})
	if err != nil {
		return failed("Error sending album (nothing was delivered): %v", err)
	}
	parentKey := &waCommon.MessageKey{RemoteJID: new(chat.String()), FromMe: new(true), ID: new(parent.ID)}

	var delivered []string
	for i, item := range items {
		if err := b.pause(ctx); err != nil {
			return b.partialAlbum(req.Recipient, parent, items, delivered, i, err)
		}
		caption := ""
		if i == 0 {
			caption = req.Caption
		}
		resp, err := b.client.SendMessage(ctx, chat, albumItemMessage(item, caption, parentKey))
		if err != nil {
			return b.partialAlbum(req.Recipient, parent, items, delivered, i, err)
		}
		delivered = append(delivered, resp.ID)
		b.recordSent(ctx, chat, resp, store.Message{Content: caption, Media: albumMediaInfo(item)})
	}
	return SendMessageResponse{
		Success: true, MessageID: parent.ID, Timestamp: parent.Timestamp,
		Message: fmt.Sprintf("Album of %d items sent to %s (album id %s; item ids %s)", len(items), req.Recipient, parent.ID, strings.Join(delivered, ", ")),
	}
}

// pause waits the gap between two messages of an album.
func (b *Bridge) pause(ctx context.Context) error {
	if b.albumGap <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(b.albumGap)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// partialAlbum is the answer when item failed after the album message went
// out: delivered holds the ids of the items before it.
func (b *Bridge) partialAlbum(recipient string, parent whatsmeow.SendResponse, items []albumItem, delivered []string, failedAt int, cause error) SendMessageResponse {
	var sent []string
	for i, id := range delivered {
		sent = append(sent, fmt.Sprintf("%s (%s)", id, items[i].name))
	}
	list := "none"
	if len(sent) > 0 {
		list = strings.Join(sent, ", ")
	}
	b.log.Warn("album stopped", "album_id", parent.ID, "delivered", len(delivered), "total", len(items), "err", cause)
	return SendMessageResponse{
		Success: false, MessageID: parent.ID, Timestamp: parent.Timestamp,
		Message: fmt.Sprintf("Album to %s is incomplete: %d of %d items delivered. Album id %s; delivered items: %s. Item %d (%s) failed: %v. The remaining %d were not sent and nothing was retracted; delete_message removes what was delivered.",
			recipient, len(delivered), len(items), parent.ID, list, failedAt+1, items[failedAt].name, cause, len(items)-failedAt-1),
	}
}

// albumItemMessage is an image or video message that belongs to the album
// whose AlbumMessage has the key parent. The caption is empty for all but the
// first item. MessageIndex is left unset: it is optional and whatsmeow does
// not say what it means.
func albumItemMessage(item albumItem, caption string, parent *waCommon.MessageKey) *waE2E.Message {
	up := item.upload
	msg := &waE2E.Message{MessageContextInfo: &waE2E.MessageContextInfo{
		MessageAssociation: &waE2E.MessageAssociation{
			AssociationType:  waE2E.MessageAssociation_MEDIA_ALBUM.Enum(),
			ParentMessageKey: parent,
		},
	}}
	var text *string
	if caption != "" {
		text = &caption
	}
	if item.kind == "image" {
		msg.ImageMessage = &waE2E.ImageMessage{
			Caption: text, Mimetype: &item.mime,
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}
	} else {
		msg.VideoMessage = &waE2E.VideoMessage{
			Caption: text, Mimetype: &item.mime,
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength,
		}
	}
	return msg
}

func albumMediaInfo(item albumItem) content.MediaInfo {
	up := item.upload
	return content.MediaInfo{
		Type: item.kind, Filename: item.name, Mimetype: item.mime, URL: up.URL, DirectPath: up.DirectPath,
		MediaKey: up.MediaKey, FileSHA256: up.FileSHA256, FileEncSHA256: up.FileEncSHA256, FileLength: up.FileLength,
	}
}
