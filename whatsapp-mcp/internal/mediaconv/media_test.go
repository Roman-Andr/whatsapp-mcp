package mediaconv

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/media/transcribe"
)

// defaultKit is the real toolkit; whisper is never run here.
var defaultKit = Default(transcribe.Config{CLI: "/nonexistent/whisper-cli"})

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, x%h, color.NRGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func writeFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestImageForModel(t *testing.T) {
	t.Parallel()
	t.Run("passes small images through", func(t *testing.T) {
		t.Parallel()
		data := encodePNG(t, 64, 32)
		out, mime, err := defaultKit.ImageForModel(writeFile(t, "small.png", data))
		if err != nil || mime != "image/png" || !bytes.Equal(out, data) {
			t.Errorf("got %s %v", mime, err)
		}
	})
	t.Run("shrinks large images", func(t *testing.T) {
		t.Parallel()
		out, mime, err := defaultKit.Image.ForModel(encodePNG(t, 3000, 1000))
		if err != nil || mime != "image/jpeg" {
			t.Fatalf("got %s %v", mime, err)
		}
		img, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if b := img.Bounds(); b.Dx() != 1568 || b.Dy() != 523 {
			t.Errorf("size %v", b)
		}
	})
	t.Run("refuses what is not an image", func(t *testing.T) {
		t.Parallel()
		if _, _, err := defaultKit.Image.ForModel([]byte("not an image")); err == nil {
			t.Error("garbage accepted")
		}
	})
	t.Run("reports a missing file", func(t *testing.T) {
		t.Parallel()
		if _, _, err := defaultKit.ImageForModel(filepath.Join(t.TempDir(), "missing.png")); !os.IsNotExist(err) {
			t.Errorf("got %v", err)
		}
	})
}

func TestStickerIsA512WebP(t *testing.T) {
	t.Parallel()
	outbox := t.TempDir()
	out, err := defaultKit.Sticker.ToSticker(writeFile(t, "logo.png", encodePNG(t, 800, 400)), outbox)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != outbox {
		t.Errorf("written to %s, not the outbox", out)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) < 12 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		t.Fatalf("not a WebP: %q", data[:min(12, len(data))])
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width != 512 || cfg.Height != 512 {
		t.Errorf("got %+v %v", cfg, err)
	}
	if _, err := defaultKit.Sticker.ToSticker(filepath.Join(t.TempDir(), "missing.png"), ""); !os.IsNotExist(err) {
		t.Errorf("missing file: %v", err)
	}
	if _, err := defaultKit.Sticker.ToSticker(writeFile(t, "x.png", []byte("nope")), ""); err == nil {
		t.Error("garbage accepted")
	}
}

func TestToOpusOgg(t *testing.T) {
	t.Parallel()
	t.Run("missing file", func(t *testing.T) {
		t.Parallel()
		missing := filepath.Join(t.TempDir(), "missing.mp3")
		_, err := FFmpegAudio{}.ToOpusOgg(t.Context(), missing, "")
		if want := "Input file not found: " + missing; err == nil || err.Error() != want {
			t.Errorf("got %v, want %s", err, want)
		}
	})
	t.Run("unreadable audio", func(t *testing.T) {
		t.Parallel()
		if _, err := lookFFmpeg(); err != nil {
			t.Skip("ffmpeg is not installed")
		}
		outbox := t.TempDir()
		_, err := FFmpegAudio{}.ToOpusOgg(t.Context(), writeFile(t, "x.mp3", []byte("not audio")), outbox)
		const prefix = "Failed to convert audio. You likely need to install ffmpeg exit status "
		if err == nil || len(err.Error()) < len(prefix) || err.Error()[:len(prefix)] != prefix {
			t.Errorf("got %v", err)
		}
		if left, _ := os.ReadDir(outbox); len(left) != 0 {
			t.Errorf("left behind %v", left)
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()
		if _, err := lookFFmpeg(); err != nil {
			t.Skip("ffmpeg is not installed")
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := (FFmpegAudio{}).ToOpusOgg(ctx, writeFile(t, "x.mp3", []byte("x")), t.TempDir()); err == nil {
			t.Error("converted with a cancelled context")
		}
	})
}

func TestWhisperWithoutCLI(t *testing.T) {
	t.Parallel()
	_, _, err := defaultKit.Transcriber.Transcribe(t.Context(), writeFile(t, "v.wav", []byte("RIFF")), "")
	if err == nil {
		t.Error("transcribed without whisper-cli")
	}
}
