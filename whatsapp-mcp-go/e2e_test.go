package main

// End-to-end runs of the real media tools: whisper.cpp transcription, the
// ffmpeg voice-message conversion and video frames, through the MCP tool
// layer against a fixture store and a fake bridge. They are skipped when
// whisper-cli, its model or ffmpeg is missing (as in CI), and in -short.

import (
	"bytes"
	"image/png"
	"math"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/mediaconv"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/storetest"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/media/audio"
)

func requireTool(t *testing.T, name string) string {
	t.Helper()
	if testing.Short() {
		t.Skip("end-to-end media test")
	}
	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s is not installed", name)
	}
	return path
}

func copyFixture(t *testing.T, src string) string {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), filepath.Base(src))
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dst
}

func TestEndToEndTranscription(t *testing.T) {
	requireTool(t, "whisper-cli")
	home, _ := os.UserHomeDir()
	cache := os.Getenv("WHATSAPP_WHISPER_CACHE")
	if cache == "" {
		cache = filepath.Join(home, ".cache", "whatsapp-mcp", "whisper")
	}
	if _, err := os.Stat(filepath.Join(cache, "ggml-small.bin")); err != nil {
		t.Skip("the small model is not downloaded")
	}
	s := newStore(t)
	s.Config.Whisper.CacheDir = cache
	s.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type, media_seconds, media_detail)
		VALUES ('VJ', '` + group + `', '5', '', '2026-10-27 18:00:00+03:00', 0, 'audio', 11, 'voice')`)
	voice := copyFixture(t, "media/audio/testdata/jfk-voip16k.ogg")
	b := storetest.NewBridge(t, s)
	b.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		w.Write([]byte(`{"success": true, "path": "` + voice + `", "media_type": "audio"}`))
	}
	cs := connectClient(t, s)
	started := time.Now()
	res := callTool(t, cs, "transcribe_voice_message", map[string]any{"message_id": "VJ", "chat_jid": group, "language": "en"})
	got := structured(t, res)["result"].(map[string]any)
	text, _ := got["transcript"].(string)
	t.Logf("transcribed 11 s in %v: %q", time.Since(started), text)
	if got["success"] != true || !strings.HasPrefix(text, "[en] ") || !strings.Contains(strings.ToLower(text), "ask not what your country can do for you") {
		t.Fatalf("got %v", got)
	}
	// Cached: shown by list_messages, and a second call does not run whisper.
	listed := callTool(t, cs, "list_messages", map[string]any{"chat_jid": group, "limit": 1, "include_context": false}).Content[0].(*mcp.TextContent).Text
	assertContains(t, listed, "[audio (voice) 0:11 |", "[voice transcript: "+strings.TrimPrefix(text, "[en] ")+"]")
	started = time.Now()
	again := structured(t, callTool(t, cs, "transcribe_voice_message", map[string]any{"message_id": "VJ", "chat_jid": group}))["result"].(map[string]any)
	if again["transcript"] != strings.TrimPrefix(text, "[en] ") || time.Since(started) > time.Second {
		t.Errorf("not served from the cache: %v", again)
	}
	// A speech-free tone is no transcript, not an error of the server.
	s.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)
		VALUES ('VT', '` + group + `', '5', '', '2026-10-27 18:01:00+03:00', 0, 'audio')`)
	voice = copyFixture(t, "media/audio/testdata/tone-voip16k.ogg")
	tone := structured(t, callTool(t, cs, "transcribe_voice_message", map[string]any{"message_id": "VT", "chat_jid": group}))["result"].(map[string]any)
	if tone["success"] != false || tone["message"] != "No speech recognised" {
		t.Errorf("tone: %v", tone)
	}
}

// sineWAV is a second of 440 Hz as a 16 kHz WAV file.
func sineWAV(t *testing.T) string {
	t.Helper()
	pcm := make([]int16, audio.SampleRate)
	for i := range pcm {
		pcm[i] = int16(8000 * math.Sin(2*math.Pi*440*float64(i)/audio.SampleRate))
	}
	path := filepath.Join(t.TempDir(), "tone.wav")
	if err := os.WriteFile(path, audio.WAV(pcm), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestEndToEndVoiceMessageConversion(t *testing.T) {
	requireTool(t, "ffmpeg")
	s := newStore(t)
	outbox := t.TempDir()
	src := filepath.Join(outbox, "note.wav")
	os.Rename(sineWAV(t), src)
	s.PublishMediaRoots([]string{outbox}, outbox)
	var mu sync.Mutex
	var sent []string
	b := storetest.NewBridge(t, s)
	b.Answer = func(w http.ResponseWriter, r *http.Request, payload map[string]any) {
		// Look at the file while the request is open: it is removed after.
		path, _ := payload["media_path"].(string)
		data, _ := os.ReadFile(path)
		ok := bytes.HasPrefix(data, []byte("OggS")) && bytes.Contains(data, []byte("OpusHead"))
		mu.Lock()
		sent = append(sent, path)
		mu.Unlock()
		if !ok {
			w.Write([]byte(`{"success": false, "message": "not Ogg/Opus"}`))
			return
		}
		w.Write([]byte(`{"success": true, "message": "ok", "message_id": "NEW"}`))
	}
	cs := connectClient(t, s)
	got := structured(t, callTool(t, cs, "send_audio_message", map[string]any{"recipient": group, "media_path": src}))
	if got["success"] != true || got["message_id"] != "NEW" || len(sent) != 1 {
		t.Fatalf("got %v, sent %v", got, sent)
	}
	if filepath.Dir(sent[0]) != outbox || !strings.HasSuffix(sent[0], ".ogg") {
		t.Errorf("converted to %s, not into the outbox", sent[0])
	}
	if _, err := os.Stat(sent[0]); err == nil {
		t.Error("the converted file was left behind")
	}
	// An unreadable input is reported, not sent.
	broken := filepath.Join(outbox, "broken.mp3")
	os.WriteFile(broken, []byte("not audio"), 0o600)
	got = structured(t, callTool(t, cs, "send_audio_message", map[string]any{"recipient": group, "media_path": broken}))
	if got["success"] != false || !strings.HasPrefix(got["message"].(string), "Error converting file to opus ogg: ") || len(sent) != 1 {
		t.Errorf("got %v", got)
	}
}

func TestEndToEndVideoFrame(t *testing.T) {
	ffmpeg := requireTool(t, "ffmpeg")
	s := newStore(t)
	clip := filepath.Join(t.TempDir(), "clip.mp4")
	if out, err := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "color=c=red:s=2400x1600:d=0.3",
		"-pix_fmt", "yuv420p", clip).CombinedOutput(); err != nil {
		t.Fatalf("making a clip: %v %s", err, out)
	}
	s.Exec(`INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)
		VALUES ('VV', '` + group + `', '5', '', '2026-10-27 18:00:00+03:00', 0, 'video')`)
	b := storetest.NewBridge(t, s)
	b.Answer = func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		w.Write([]byte(`{"success": true, "path": "` + clip + `"}`))
	}
	frame, err := (mediaconv.FFmpegVideo{}).FirstFramePNG(t.Context(), clip)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(frame))
	if err != nil || img.Bounds().Dx() != 2400 {
		t.Fatalf("frame %v %v", img.Bounds(), err)
	}
	if r, g, bl, _ := img.At(1200, 800).RGBA(); r>>8 < 200 || g>>8 > 60 || bl>>8 > 60 {
		t.Errorf("not red: %d %d %d", r>>8, g>>8, bl>>8)
	}
	res := callTool(t, connectClient(t, s), "view_media", map[string]any{"message_id": "VV", "chat_jid": group})
	shown, ok := res.Content[0].(*mcp.ImageContent)
	if !ok || shown.MIMEType != "image/jpeg" || len(shown.Data) > 1_000_000 {
		t.Fatalf("got %+v", res.Content[0])
	}
	broken := filepath.Join(t.TempDir(), "broken.mp4")
	os.WriteFile(broken, []byte("not a video"), 0o600)
	if _, err := (mediaconv.FFmpegVideo{}).FirstFramePNG(t.Context(), broken); err == nil || !strings.Contains(err.Error(), "ffmpeg could not read a frame") {
		t.Errorf("got %v", err)
	}
}
