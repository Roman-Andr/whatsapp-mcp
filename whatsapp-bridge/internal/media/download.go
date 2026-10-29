package media

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waMmsRetry"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"go.mau.fi/whatsmeow/util/cbcutil"
	"go.mau.fi/whatsmeow/util/hkdfutil"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/content"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// DefaultHost serves media downloads.
const DefaultHost = "https://mmg.whatsapp.net"

// DefaultRoot is the bridge's store directory, relative to the bridge
// directory; downloads go to subdirectories of it.
const DefaultRoot = "store"

// ReuploadTimeout is how long a download waits for the phone to answer a
// re-upload request. The phone answers within seconds when it is online; a
// longer wait only holds the caller.
const ReuploadTimeout = 45 * time.Second

// maxPlaintextDownload bounds an attachment fetched without whatsmeow
// (WhatsApp's own limit for documents is 2 GB, but nothing the bridge
// re-verifies is that large).
const maxPlaintextDownload = 200 << 20

// mediaKeyExpansion is the length of the HKDF output an attachment's media
// key expands to: IV (16), cipher key (32), MAC key (32), and 32 unused.
const mediaKeyExpansion = 112

// macLength is the length of the truncated HMAC at the end of an encrypted
// attachment.
const macLength = 10

// Service downloads attachments. The zero value of the unexported state is
// ready to use; fill in the exported fields.
type Service struct {
	Client wa.Client
	Store  *store.Store
	// Host serves media downloads that bypass whatsmeow; empty means
	// DefaultHost. Tests point it at a fake CDN.
	Host string
	// Root is the store directory media is saved under; empty means
	// DefaultRoot. Tests use a temporary directory.
	Root string
	// Log receives progress and failures; nil discards them.
	Log *slog.Logger
	// Author is the full JID of a stored message's author, which a re-upload
	// request names. The bridge sets it (it knows the account's own JIDs and
	// asks groups for their addressing mode).
	Author func(ctx context.Context, ref *store.MessageRef) (types.JID, error)

	retries     retryWaiters
	downloading keyedMutex
}

// File is a downloaded attachment.
type File struct {
	Name      string // file name under the chat's directory
	Path      string // absolute
	MediaType string
	Mimetype  string
}

// Downloader is a stored attachment as whatsmeow downloads it: it
// implements whatsmeow.DownloadableMessage.
type Downloader struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     whatsmeow.MediaType
}

// GetDirectPath implements whatsmeow.DownloadableMessage.
func (d *Downloader) GetDirectPath() string { return d.DirectPath }

// GetURL implements whatsmeow.DownloadableMessage.
func (d *Downloader) GetURL() string { return d.URL }

// GetMediaKey implements whatsmeow.DownloadableMessage.
func (d *Downloader) GetMediaKey() []byte { return d.MediaKey }

// GetFileLength implements whatsmeow.DownloadableMessage.
func (d *Downloader) GetFileLength() uint64 { return d.FileLength }

// GetFileSHA256 implements whatsmeow.DownloadableMessage.
func (d *Downloader) GetFileSHA256() []byte { return d.FileSHA256 }

// GetFileEncSHA256 implements whatsmeow.DownloadableMessage.
func (d *Downloader) GetFileEncSHA256() []byte { return d.FileEncSHA256 }

// GetMediaType implements whatsmeow.DownloadableMessage.
func (d *Downloader) GetMediaType() whatsmeow.MediaType { return d.MediaType }

// retryKey names the message a re-upload request is about. Message ids are
// only unique within a chat, so the chat is part of the key.
type retryKey struct {
	chat string // non-AD JID
	id   types.MessageID
}

// retryWaiters routes MediaRetry events to the download waiting for them.
type retryWaiters struct {
	mu      sync.Mutex
	waiting map[retryKey]chan *events.MediaRetry
}

func (w *retryWaiters) register(k retryKey) chan *events.MediaRetry {
	ch := make(chan *events.MediaRetry, 1)
	w.mu.Lock()
	if w.waiting == nil {
		w.waiting = make(map[retryKey]chan *events.MediaRetry)
	}
	w.waiting[k] = ch
	w.mu.Unlock()
	return ch
}

func (w *retryWaiters) unregister(k retryKey) {
	w.mu.Lock()
	delete(w.waiting, k)
	w.mu.Unlock()
}

func (w *retryWaiters) deliver(evt *events.MediaRetry) {
	w.mu.Lock()
	ch, ok := w.waiting[retryKey{chat: evt.ChatID.ToNonAD().String(), id: evt.MessageID}]
	w.mu.Unlock()
	if ok {
		select {
		case ch <- evt:
		default:
		}
	}
}

// HandleRetry hands the phone's answer to a re-upload request to the
// download waiting for it.
func (s *Service) HandleRetry(evt *events.MediaRetry) {
	s.retries.deliver(evt)
}

// FileName is the name a message's attachment is saved under. Both parts
// are cleaned separately: cleaning the joined name would let a sender-chosen
// file name like "x/<other id>_a.pdf" drop the prefix and take the place of
// another message's file.
func FileName(messageID, name string) string {
	return content.SafeFileName(messageID) + "_" + content.SafeFileName(name)
}

// Dir is where downloaded media of a chat is kept, under DefaultRoot.
func Dir(chatJID string) string {
	return dirIn(DefaultRoot, chatJID)
}

func dirIn(root, chatJID string) string {
	return filepath.Join(root, content.SafeFileName(strings.ReplaceAll(chatJID, ":", "_")))
}

func (s *Service) root() string {
	if s.Root == "" {
		return DefaultRoot
	}
	return s.Root
}

func (s *Service) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

// Download saves a message's attachment under store/<chat>/ and returns
// its absolute path. Files already downloaded are returned as they are.
func (s *Service) Download(ctx context.Context, messageID, chatJID string) (*File, error) {
	media, err := s.Store.Media(ctx, messageID, chatJID)
	if err != nil {
		return nil, fmt.Errorf("failed to find message: %w", err)
	}
	if media.Type == "" {
		return nil, fmt.Errorf("not a media message")
	}
	// The automatic download of a new message and a download_media call can
	// ask for the same file at once.
	unlock := s.downloading.Lock(chatJID + "/" + messageID)
	defer unlock()

	// Names in the DB are time based and collide when several files arrive in
	// the same second; the id prefix makes them unique per message. The name
	// itself may come from another user, so it is reduced to a bare file name.
	filename := FileName(messageID, media.Filename)
	dir := dirIn(s.root(), chatJID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create chat directory: %w", err)
	}
	localPath := filepath.Join(dir, filename)
	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	result := &File{Name: filename, Path: absPath, MediaType: media.Type, Mimetype: media.Mimetype}
	if _, err := os.Stat(localPath); err == nil {
		return result, nil
	}

	if len(media.MediaKey) == 0 || len(media.FileSHA256) == 0 || len(media.FileEncSHA256) == 0 || media.FileLength == 0 {
		return nil, fmt.Errorf("incomplete media information for download")
	}
	waType, err := whatsmeowMediaType(media.Type)
	if err != nil {
		return nil, err
	}
	directPath := media.DirectPath
	if directPath == "" {
		directPath = extractDirectPathFromURL(media.URL)
	}
	downloader := &Downloader{
		URL: media.URL, DirectPath: directPath, MediaKey: media.MediaKey, FileLength: media.FileLength,
		FileSHA256: media.FileSHA256, FileEncSHA256: media.FileEncSHA256, MediaType: waType,
	}

	log := s.log().With("message_id", messageID, "chat", chatJID)
	log.Info("downloading media")
	data, err := s.Client.Download(ctx, downloader)
	if err != nil && isExpiredMediaError(err) {
		// The CDN forgets media after a few weeks. The phone still has it and
		// re-uploads it on request, answering with a fresh direct path.
		log.Info("media expired on the CDN, asking the phone to re-upload it", "err", err)
		newPath, retryErr := s.requestReupload(ctx, messageID, chatJID, media)
		if retryErr != nil {
			return nil, fmt.Errorf("failed to download media: %w; re-upload request failed: %w", err, retryErr)
		}
		downloader.DirectPath = newPath
		if err := s.Store.UpdateDirectPath(ctx, messageID, chatJID, newPath); err != nil {
			log.Warn("failed to store refreshed media path", "err", err)
		}
		data, err = s.Client.Download(ctx, downloader)
	}
	if errors.Is(err, whatsmeow.ErrInvalidMediaEncSHA256) {
		// A file the phone re-uploaded can differ from the original upload in
		// its ciphertext hash while being encrypted with the same key.
		log.Info("media has a different ciphertext hash, verifying MAC and plaintext instead")
		data, err = s.downloadVerifyingPlaintext(ctx, downloader.DirectPath, media.MediaKey, media.FileSHA256, waType)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to download media: %w", err)
	}

	// Written under a temporary name and renamed, so a crash or a full disk
	// never leaves a truncated file that later calls would return as cached.
	tmpPath := localPath + ".part"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to save media file: %w", err)
	}
	if err := os.Rename(tmpPath, localPath); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("failed to save media file: %w", err)
	}
	log.Info("media downloaded", "type", media.Type, "path", absPath, "bytes", len(data))
	return result, nil
}

// downloadVerifyingPlaintext downloads an encrypted attachment without
// checking its ciphertext hash. The MAC proves the file was encrypted with
// the message's media key and the plaintext hash proves it is the original
// content, so the integrity guarantee is the same.
func (s *Service) downloadVerifyingPlaintext(ctx context.Context, directPath string, mediaKey, fileSHA256 []byte, mediaType whatsmeow.MediaType) ([]byte, error) {
	if len(fileSHA256) != sha256.Size {
		return nil, fmt.Errorf("no plaintext hash to verify against")
	}
	expanded := hkdfutil.SHA256(mediaKey, nil, []byte(mediaType), mediaKeyExpansion)
	iv, cipherKey, macKey := expanded[:16], expanded[16:48], expanded[48:80]

	sep := "?"
	if strings.Contains(directPath, "?") {
		sep = "&"
	}
	host := s.Host
	if host == "" {
		host = DefaultHost
	}
	url := host + directPath + sep + "mms-type=" + mmsTypeFor(mediaType) + "&__wa-mms="
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", "https://web.whatsapp.com")
	req.Header.Set("Referer", "https://web.whatsapp.com/")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status code %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPlaintextDownload))
	if err != nil {
		return nil, err
	}
	if len(body) <= macLength {
		return nil, fmt.Errorf("downloaded file too short")
	}
	ciphertext, mac := body[:len(body)-macLength], body[len(body)-macLength:]
	h := hmac.New(sha256.New, macKey)
	h.Write(iv)
	h.Write(ciphertext)
	if !hmac.Equal(h.Sum(nil)[:macLength], mac) {
		return nil, fmt.Errorf("media MAC mismatch (%d bytes from %s): the file was not encrypted with this message's key", len(body), resp.Request.URL.Host)
	}
	data, err := cbcutil.Decrypt(cipherKey, iv, ciphertext)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt file: %w", err)
	}
	if sum := sha256.Sum256(data); !bytes.Equal(sum[:], fileSHA256) {
		return nil, fmt.Errorf("plaintext hash mismatch")
	}
	return data, nil
}

func mmsTypeFor(mediaType whatsmeow.MediaType) string {
	switch mediaType {
	case whatsmeow.MediaImage:
		return "image"
	case whatsmeow.MediaVideo:
		return "video"
	case whatsmeow.MediaAudio:
		return "audio"
	}
	return "document"
}

func whatsmeowMediaType(mediaType string) (whatsmeow.MediaType, error) {
	switch mediaType {
	case "image", "sticker":
		return whatsmeow.MediaImage, nil
	case "video":
		return whatsmeow.MediaVideo, nil
	case "audio":
		return whatsmeow.MediaAudio, nil
	case "document":
		return whatsmeow.MediaDocument, nil
	}
	return "", fmt.Errorf("unsupported media type: %s", mediaType)
}

func isExpiredMediaError(err error) bool {
	return errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith403) ||
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith404) ||
		errors.Is(err, whatsmeow.ErrMediaDownloadFailedWith410)
}

// requestReupload sends a media retry receipt and waits for the phone's
// answer, returning the new direct path.
func (s *Service) requestReupload(ctx context.Context, messageID, chatJID string, media *store.Media) (string, error) {
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return "", err
	}
	ref := &store.MessageRef{ID: messageID, ChatJID: chatJID, Sender: media.Sender, SenderJID: media.SenderJID, IsFromMe: media.IsFromMe}
	sender, err := s.Author(ctx, ref)
	if err != nil {
		return "", err
	}
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: media.IsFromMe, IsGroup: chat.Server == types.GroupServer},
		ID:            messageID,
	}

	k := retryKey{chat: chat.ToNonAD().String(), id: messageID}
	answer := s.retries.register(k)
	defer s.retries.unregister(k)
	if err := s.Client.SendMediaRetryReceipt(ctx, info, media.MediaKey); err != nil {
		return "", fmt.Errorf("failed to send retry receipt: %w", err)
	}

	select {
	case evt := <-answer:
		notif, err := whatsmeow.DecryptMediaRetryNotification(evt, media.MediaKey)
		if err != nil {
			return "", err
		}
		if notif.GetResult() != waMmsRetry.MediaRetryNotification_SUCCESS || notif.GetDirectPath() == "" {
			return "", fmt.Errorf("phone answered %s", notif.GetResult())
		}
		return notif.GetDirectPath(), nil
	case <-time.After(ReuploadTimeout):
		return "", fmt.Errorf("no answer from the phone within %s (is it online?)", ReuploadTimeout)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Extract direct path from a WhatsApp media URL. Used for rows stored before
// the direct_path column existed.
func extractDirectPathFromURL(url string) string {
	// Example URL: https://mmg.whatsapp.net/v/t62.7118-24/13812002_698058036224062_3424455886509161511_n.enc?ccb=11-4&oh=...
	parts := strings.SplitN(url, ".net/", 2)
	if len(parts) < 2 {
		return url // Return original URL if parsing fails
	}
	// Keep the query string: it carries the CDN authorization (oh/oe/ccb), and
	// whatsmeow appends its own parameters with "&". Stripping it makes the CDN
	// answer 403 to every download.
	return "/" + parts[1]
}
