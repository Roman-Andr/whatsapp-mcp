package content

import (
	"mime"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

// MediaInfo describes the attachment of a message.
type MediaInfo struct {
	Type          string // image, video, audio, document or sticker; "" for none
	Filename      string
	Mimetype      string
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    uint64

	Width, Height uint32 // images and videos
	Seconds       uint32 // audio and video length
	// Detail says what kind of image, video or audio it is: "voice" (a
	// voice message), "video note" (round video), "gif", "animated".
	Detail string
}

// Media returns the attachment of a message. ts names the file;
// downloads prefix the message id, so names only need to be readable.
func Media(msg *waE2E.Message, ts time.Time) MediaInfo {
	// Same envelopes as in Text: a document sent with a caption
	// arrives as DocumentWithCaptionMessage, and without unwrapping it would be
	// stored as a message with no attachment at all.
	msg = Unwrap(msg)
	if msg == nil {
		return MediaInfo{}
	}
	if ts.IsZero() {
		ts = time.Now()
	}
	stamp := ts.Format("20060102_150405")

	if img := msg.GetImageMessage(); img != nil {
		return MediaInfo{
			Type: "image", Filename: "image_" + stamp + Extension(img.GetMimetype(), ".jpg"),
			Mimetype: img.GetMimetype(), URL: img.GetURL(), DirectPath: img.GetDirectPath(),
			MediaKey: img.GetMediaKey(), FileSHA256: img.GetFileSHA256(), FileEncSHA256: img.GetFileEncSHA256(),
			FileLength: img.GetFileLength(), Width: img.GetWidth(), Height: img.GetHeight(),
		}
	}
	vid, detail := msg.GetVideoMessage(), ""
	if vid == nil && msg.GetPtvMessage() != nil {
		vid, detail = msg.GetPtvMessage(), "video note"
	}
	if vid != nil {
		if vid.GetGifPlayback() {
			detail = "gif"
		}
		return MediaInfo{
			Type: "video", Filename: "video_" + stamp + Extension(vid.GetMimetype(), ".mp4"),
			Mimetype: vid.GetMimetype(), URL: vid.GetURL(), DirectPath: vid.GetDirectPath(),
			MediaKey: vid.GetMediaKey(), FileSHA256: vid.GetFileSHA256(), FileEncSHA256: vid.GetFileEncSHA256(),
			FileLength: vid.GetFileLength(), Width: vid.GetWidth(), Height: vid.GetHeight(), Seconds: vid.GetSeconds(),
			Detail: detail,
		}
	}
	if aud := msg.GetAudioMessage(); aud != nil {
		return MediaInfo{
			Type: "audio", Filename: "audio_" + stamp + ".ogg",
			Mimetype: aud.GetMimetype(), URL: aud.GetURL(), DirectPath: aud.GetDirectPath(),
			MediaKey: aud.GetMediaKey(), FileSHA256: aud.GetFileSHA256(), FileEncSHA256: aud.GetFileEncSHA256(),
			FileLength: aud.GetFileLength(), Seconds: aud.GetSeconds(), Detail: map[bool]string{true: "voice"}[aud.GetPTT()],
		}
	}
	if doc := msg.GetDocumentMessage(); doc != nil {
		filename := doc.GetFileName()
		if filename == "" {
			filename = "document_" + stamp + Extension(doc.GetMimetype(), "")
		}
		return MediaInfo{
			Type: "document", Filename: filename,
			Mimetype: doc.GetMimetype(), URL: doc.GetURL(), DirectPath: doc.GetDirectPath(),
			MediaKey: doc.GetMediaKey(), FileSHA256: doc.GetFileSHA256(), FileEncSHA256: doc.GetFileEncSHA256(),
			FileLength: doc.GetFileLength(),
		}
	}
	if st := msg.GetStickerMessage(); st != nil {
		return MediaInfo{
			Type: "sticker", Filename: "sticker_" + stamp + ".webp",
			Mimetype: st.GetMimetype(), URL: st.GetURL(), DirectPath: st.GetDirectPath(),
			MediaKey: st.GetMediaKey(), FileSHA256: st.GetFileSHA256(), FileEncSHA256: st.GetFileEncSHA256(),
			FileLength: st.GetFileLength(), Width: st.GetWidth(), Height: st.GetHeight(),
			Detail: map[bool]string{true: "animated"}[st.GetIsAnimated()],
		}
	}
	return MediaInfo{}
}

// Extension picks a file extension for a MIME type.
func Extension(mimetype, fallback string) string {
	base := strings.TrimSpace(strings.SplitN(mimetype, ";", 2)[0])
	switch base {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "application/pdf":
		return ".pdf"
	}
	if exts, err := mime.ExtensionsByType(base); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return fallback
}

// MIMEType picks the MIME type of a file being sent from its extension.
// Documents sent as application/octet-stream arrive as unnamed .bin files
// without a preview, so the common office and PDF types are spelled out
// instead of relying on the host's mime database.
func MIMEType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	known := map[string]string{
		".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
		".webp": "image/webp", ".ogg": "audio/ogg; codecs=opus", ".mp3": "audio/mpeg", ".m4a": "audio/mp4",
		".mp4": "video/mp4", ".avi": "video/avi", ".mov": "video/quicktime",
		".pdf": "application/pdf", ".txt": "text/plain", ".csv": "text/csv", ".json": "application/json",
		".zip":  "application/zip",
		".doc":  "application/msword",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".xls":  "application/vnd.ms-excel",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".ppt":  "application/vnd.ms-powerpoint",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	}
	if t, ok := known[ext]; ok {
		return t
	}
	if t := mime.TypeByExtension(ext); t != "" {
		return t
	}
	return "application/octet-stream"
}

// SafeFileName strips directories and separators from a name that came from
// another WhatsApp user (a document's file name), so a download cannot be
// written outside its chat directory.
func SafeFileName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.Map(func(r rune) rune {
		if r < 32 || r == '/' || r == ':' {
			return '_'
		}
		return r
	}, name)
	if name == "." || name == ".." || name == "" || name == "/" {
		return "file"
	}
	return name
}
