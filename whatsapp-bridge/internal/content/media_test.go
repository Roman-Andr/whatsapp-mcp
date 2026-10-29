package content

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
)

func TestSafeFileNameContainsPaths(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"report.pdf", "report.pdf"},
		{"../../etc/passwd", "passwd"},
		{`..\..\windows\win.ini`, "win.ini"},
		{"..", "file"},
		{"", "file"},
		{"a:b.txt", "a_b.txt"},
		{"line\nbreak.txt", "line_break.txt"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := SafeFileName(c.in); got != c.want {
				t.Errorf("SafeFileName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMIMEType(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"/x/a.PDF", "application/pdf"},
		{"/x/a.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"/x/a.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"/x/a.jpg", "image/jpeg"},
		{"/x/a.ogg", "audio/ogg; codecs=opus"},
		{"/x/a", "application/octet-stream"},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			t.Parallel()
			if got := MIMEType(c.in); got != c.want {
				t.Errorf("MIMEType(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestMediaFileNamesFollowMimeAndTime(t *testing.T) {
	t.Parallel()
	ts := time.Date(2026, 10, 27, 9, 8, 7, 0, time.UTC)
	t.Run("png image", func(t *testing.T) {
		t.Parallel()
		png := Media(&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: new("image/png"), DirectPath: new("/v/p")}}, ts)
		if png.Filename != "image_20261027_090807.png" || png.DirectPath != "/v/p" {
			t.Errorf("got %+v", png)
		}
	})
	t.Run("sticker", func(t *testing.T) {
		t.Parallel()
		if sticker := Media(&waE2E.Message{StickerMessage: &waE2E.StickerMessage{}}, ts); sticker.Type != "sticker" {
			t.Errorf("sticker type = %q", sticker.Type)
		}
	})
}

// An enveloped image must keep its attachment, not just its caption.
func TestMediaUnwraps(t *testing.T) {
	t.Parallel()
	msg := &waE2E.Message{ViewOnceMessageV2: &waE2E.FutureProofMessage{
		Message: &waE2E.Message{ImageMessage: &waE2E.ImageMessage{
			Caption:    new("x"),
			FileLength: new(uint64(213903)),
		}},
	}}

	media := Media(msg, time.Time{})
	if media.Type != "image" {
		t.Errorf("media.Type = %q, want %q", media.Type, "image")
	}
	if media.FileLength != 213903 {
		t.Errorf("media.FileLength = %d, want 213903", media.FileLength)
	}
}

func TestMediaMetadata(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		msg  *waE2E.Message
		want MediaInfo // only Type, Detail, Seconds, Width and Height are compared
	}{
		{"video note", &waE2E.Message{PtvMessage: &waE2E.VideoMessage{Seconds: new(uint32(15)), Width: new(uint32(480)), Height: new(uint32(480))}},
			MediaInfo{Type: "video", Detail: "video note", Seconds: 15, Width: 480, Height: 480}},
		{"voice message", &waE2E.Message{AudioMessage: &waE2E.AudioMessage{PTT: new(true), Seconds: new(uint32(42))}},
			MediaInfo{Type: "audio", Detail: "voice", Seconds: 42}},
		{"image", &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Width: new(uint32(1280)), Height: new(uint32(720))}},
			MediaInfo{Type: "image", Width: 1280, Height: 720}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := Media(c.msg, time.Now())
			if got.Type != c.want.Type || got.Detail != c.want.Detail || got.Seconds != c.want.Seconds ||
				got.Width != c.want.Width || got.Height != c.want.Height {
				t.Errorf("got %+v", got)
			}
		})
	}
}
