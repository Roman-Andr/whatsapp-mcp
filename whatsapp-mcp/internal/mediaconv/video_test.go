package mediaconv

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestFFmpegVideoFrame(t *testing.T) {
	t.Parallel()
	if _, err := lookFFmpeg(); err != nil {
		_, err := (FFmpegVideo{}).FirstFramePNG(t.Context(), "/nonexistent.mp4")
		const want = "Cannot show a video frame: install ffmpeg (there is no ffmpeg binary in PATH)"
		if !errors.Is(err, ErrVideoFrame) || err.Error() != want {
			t.Errorf("got %v, want %s", err, want)
		}
		t.Skip("ffmpeg is not installed")
	}
	broken := filepath.Join(t.TempDir(), "broken.mp4")
	if err := os.WriteFile(broken, []byte("not a video"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := (FFmpegVideo{}).FirstFramePNG(t.Context(), broken)
	const prefix = "ffmpeg could not read a frame from the video: "
	if !errors.Is(err, ErrVideoFrame) || len(err.Error()) <= len(prefix) || err.Error()[:len(prefix)] != prefix {
		t.Errorf("got %v", err)
	}
}
