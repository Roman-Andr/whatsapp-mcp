package mediaconv

// Media conversions the tools need: image scaling for the model, image ->
// WebP sticker, audio -> Ogg/Opus voice message, and voice transcription.
//
// Each is an interface, implemented by the packages under media/ (pure Go,
// whisper.cpp's CLI for transcription) except voice-message encoding, which
// uses ffmpeg. Tests build a Toolkit of fakes.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/media/modelimage"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/media/sticker"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/media/transcribe"
)

// ImageScaler makes an image small enough to hand to the model.
type ImageScaler interface {
	// ForModel returns the image bytes and their MIME type.
	ForModel(data []byte) ([]byte, string, error)
}

// StickerEncoder converts an image to a 512x512 WebP sticker with
// transparent padding, in a new file in dir ("": the temp directory) that
// the caller removes.
type StickerEncoder interface {
	ToSticker(path, dir string) (string, error)
}

// AudioConverter converts audio to Ogg/Opus for a voice message, in a new
// file in dir ("": the temp directory) that the caller removes.
type AudioConverter interface {
	ToOpusOgg(ctx context.Context, path, dir string) (string, error)
}

// VideoFramer takes the first frame of a video as PNG.
type VideoFramer interface {
	FirstFramePNG(ctx context.Context, path string) ([]byte, error)
}

// Transcriber turns a voice message into text locally.
type Transcriber interface {
	// Transcribe returns the text and the detected (or given) language.
	Transcribe(ctx context.Context, path, language string) (text, detected string, err error)
}

// Toolkit is the set of conversions the tools use. Tests build one with
// fakes.
type Toolkit struct {
	Image       ImageScaler
	Sticker     StickerEncoder
	Audio       AudioConverter
	Transcriber Transcriber
	Video       VideoFramer
}

// Default is the real toolkit; whisper runs with the given settings.
func Default(whisper transcribe.Config) Toolkit {
	return Toolkit{
		Image:       modelImage{},
		Sticker:     webpSticker{},
		Audio:       FFmpegAudio{},
		Transcriber: NewWhisper(whisper),
		Video:       FFmpegVideo{},
	}
}

// modelImage passes small JPEG and PNG images through and re-encodes
// everything else as JPEG (quality 85), scaled to fit 1568 pixels.
type modelImage struct{}

func (modelImage) ForModel(data []byte) ([]byte, string, error) {
	img, err := modelimage.FromBytes(data)
	if err != nil {
		return nil, "", err
	}
	return img.Data, img.MIMEType(), nil
}

// webpSticker encodes with libwebp transpiled to Go.
type webpSticker struct{}

func (webpSticker) ToSticker(path, dir string) (string, error) {
	in, err := os.Open(path) //nolint:gosec // G304: the file the caller sends, checked against the media roots before
	if err != nil {
		return "", err
	}
	defer in.Close()
	data, err := sticker.Convert(in)
	if err != nil {
		return "", err
	}
	return writeTemp(dir, "sticker-*.webp", data)
}

// writeTemp writes data to a new file in dir ("": the temp directory).
func writeTemp(dir, pattern string, data []byte) (string, error) {
	out, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", err
	}
	_, err = out.Write(data)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// whisper runs whisper.cpp's whisper-cli (WHATSAPP_WHISPER_MODEL, default
// "small"); the model is downloaded on first use.
type whisper struct {
	cfg  transcribe.Config
	once sync.Once
	t    *transcribe.Transcriber
}

// NewWhisper is the whisper.cpp transcriber with cfg.
func NewWhisper(cfg transcribe.Config) Transcriber { return &whisper{cfg: cfg} }

func (w *whisper) Transcribe(ctx context.Context, path, language string) (string, string, error) {
	w.once.Do(func() { w.t = transcribe.New(w.cfg) })
	res, err := w.t.TranscribeFile(ctx, path, language)
	if errors.Is(err, transcribe.ErrNoSpeech) {
		return "", res.Language, nil
	}
	return res.Text, res.Language, err
}

func tempFile(dir, suffix string) (string, error) {
	f, err := os.CreateTemp(dir, "whatsapp-*"+suffix)
	if err != nil {
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

// runFFmpeg runs ffmpeg until it ends or ctx does; there is no timeout of
// its own, as a long recording takes as long as it takes.
func runFFmpeg(ctx context.Context, args ...string) error {
	if _, err := lookFFmpeg(); err != nil {
		return errors.New("ffmpeg is not installed")
	}
	out, err := exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput() //nolint:gosec // G204: ffmpeg with our own flags; the paths are arguments, not a shell
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// FFmpegAudio converts to a 32 kbit/s Opus voice message with ffmpeg.
type FFmpegAudio struct{}

// ToOpusOgg converts path into a new file in dir.
func (FFmpegAudio) ToOpusOgg(ctx context.Context, path, dir string) (string, error) {
	if !IsFile(path) {
		//nolint:staticcheck // the message clients already see, kept verbatim
		return "", fmt.Errorf("Input file not found: %s", path)
	}
	out, err := tempFile(dir, ".ogg")
	if err != nil {
		return "", err
	}
	err = runFFmpeg(ctx, "-i", path, "-c:a", "libopus", "-b:a", "32k", "-ar", "24000",
		"-application", "voip", "-vbr", "on", "-compression_level", "10", "-frame_duration", "60", "-y", out)
	if err != nil {
		os.Remove(out)
		//nolint:staticcheck // the message clients already see, kept verbatim
		return "", fmt.Errorf("Failed to convert audio. You likely need to install ffmpeg %w", err)
	}
	return out, nil
}

// ImageForModel reads an image file and makes it small enough for the model.
func (k Toolkit) ImageForModel(path string) ([]byte, string, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, "", err
	}
	return k.Image.ForModel(data)
}

// IsFile says whether path is a regular file.
func IsFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
