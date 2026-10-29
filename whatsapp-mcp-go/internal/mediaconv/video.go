package mediaconv

// The first frame of a video, for view_media.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const frameTimeout = 60 * time.Second

// ErrVideoFrame means no frame could be taken from the video; the error
// wrapping it says why, in words for the client.
var ErrVideoFrame = errors.New("no video frame")

// frameError is an ErrVideoFrame with the text the client sees.
type frameError struct{ msg string }

func (e *frameError) Error() string { return e.msg }
func (e *frameError) Unwrap() error { return ErrVideoFrame }

// FFmpegVideo takes the frame with the ffmpeg binary. Go has no video
// decoder of its own, so without ffmpeg the error says to install it.
type FFmpegVideo struct{}

// FirstFramePNG takes the first frame of the video at path.
func (FFmpegVideo) FirstFramePNG(ctx context.Context, path string) ([]byte, error) {
	if _, err := lookFFmpeg(); err != nil {
		return nil, &frameError{"Cannot show a video frame: install ffmpeg (there is no ffmpeg binary in PATH)"}
	}
	ctx, cancel := context.WithTimeout(ctx, frameTimeout)
	defer cancel()
	//nolint:gosec // G204: ffmpeg with our own flags; the path is an argument, not a shell
	cmd := exec.CommandContext(ctx, "ffmpeg", "-nostdin", "-loglevel", "error", "-i", path,
		"-frames:v", "1", "-f", "image2pipe", "-vcodec", "png", "-")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, &frameError{fmt.Sprintf("ffmpeg took longer than %d s to read the video", int(frameTimeout.Seconds()))}
	}
	if err != nil || stdout.Len() == 0 {
		detail := strings.TrimSpace(stderr.String())
		if r := []rune(detail); len(r) > 300 {
			detail = string(r[len(r)-300:])
		}
		if detail == "" {
			detail = "no output"
		}
		return nil, &frameError{"ffmpeg could not read a frame from the video: " + detail}
	}
	return stdout.Bytes(), nil
}

func lookFFmpeg() (string, error) { return exec.LookPath("ffmpeg") }
