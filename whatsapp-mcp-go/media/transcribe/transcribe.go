// Package transcribe turns voice notes into text locally with whisper.cpp.
//
// whisper.cpp runs as its whisper-cli program, one process per message, rather
// than through its cgo bindings:
//   - transcription adds no cgo and no C++ toolchain to the MCP server's build
//     (its only cgo is SQLite's), and it is optional: without whisper-cli
//     only this feature reports an error;
//   - a ggml abort or out-of-memory on a bad file or a too-large model kills
//     the child, not the server holding the MCP session;
//   - the bindings in whisper.cpp/bindings/go trail the C API and need the
//     static library built separately anyway, so they add a build step without
//     removing one;
//   - the cost, loading the model per call, is small: the file is mmapped and
//     stays in the page cache, about 0.3 s for "small" against seconds of
//     decoding.
//
// The audio is decoded in Go (package audio) and handed over as a 16 kHz WAV,
// so whisper-cli needs no ffmpeg. Models and the Silero VAD model are fetched
// on first use into a cache directory, as faster-whisper does with the Hugging
// Face cache.
package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/media/audio"
)

// DefaultModel is the model used when WHATSAPP_WHISPER_MODEL is unset.
const DefaultModel = "small"

// ErrNoSpeech is returned when whisper found nothing to transcribe (silence,
// music, a tone).
var ErrNoSpeech = errors.New("no speech recognised")

// Config says where whisper-cli and the models are. The zero value of a field
// means its default; the server fills it from WHATSAPP_WHISPER_MODEL,
// WHATSAPP_WHISPER_CLI, WHATSAPP_WHISPER_CACHE and WHATSAPP_WHISPER_THREADS
// (internal/config).
type Config struct {
	// Model is a model name ("tiny", "base", "small", "medium", "large-v3",
	// "turbo", "small.en", "small-q5_1", ...) fetched into CacheDir, or a path
	// to a ggml .bin file. Default DefaultModel.
	Model string
	// CLI is the whisper-cli executable, a path or a name looked up in PATH.
	// Default "whisper-cli".
	CLI string
	// CacheDir holds downloaded models. Default <user cache dir>/whatsapp-mcp/whisper.
	CacheDir string
	// Threads for whisper-cli. Default min(NumCPU, 8): whisper.cpp scales
	// poorly past that on CPU.
	Threads int
	// NoVAD turns off voice activity detection. faster-whisper is called with
	// vad_filter=True, so VAD is on by default: it drops silence before
	// decoding, which is faster and stops whisper inventing words in pauses.
	NoVAD bool
	// ModelURL is the download URL template for models, with %s for the file
	// name. Default the whisper.cpp model repository on Hugging Face.
	ModelURL string
	// VADModelURL is the URL of the Silero VAD model.
	VADModelURL string
}

const (
	defaultModelURL    = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/%s"
	defaultVADModelURL = "https://huggingface.co/ggml-org/whisper-vad/resolve/main/ggml-silero-v5.1.2.bin"
)

func (c Config) withDefaults() Config {
	if c.Model == "" {
		c.Model = DefaultModel
	}
	if c.CLI == "" {
		c.CLI = "whisper-cli"
	}
	if c.CacheDir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		c.CacheDir = filepath.Join(base, "whatsapp-mcp", "whisper")
	}
	if c.Threads <= 0 {
		c.Threads = min(runtime.NumCPU(), 8)
	}
	if c.ModelURL == "" {
		c.ModelURL = defaultModelURL
	}
	if c.VADModelURL == "" {
		c.VADModelURL = defaultVADModelURL
	}
	return c
}

// Result is a transcript and the language it is in: the one asked for, or
// the one whisper detected.
type Result struct {
	Text     string
	Language string
}

// Transcriber runs whisper-cli. It is safe for concurrent use; transcriptions
// run one at a time, since each already uses all the threads it is given.
type Transcriber struct {
	cfg  Config
	mu   sync.Mutex // serialises runs and downloads
	cli  string
	vad  string
	path string
}

// New returns a Transcriber; nothing is checked or downloaded until first use.
func New(cfg Config) *Transcriber {
	return &Transcriber{cfg: cfg.withDefaults()}
}

var languageRe = regexp.MustCompile(`^[a-z]{2,3}$`)

// TranscribeFile transcribes an audio file. Ogg/Opus (every WhatsApp voice
// note) is decoded here; anything else is passed to whisper-cli as is, which
// reads WAV, MP3 and FLAC itself. language is an ISO 639-1 code such as "en",
// or "" to detect it.
func (t *Transcriber) TranscribeFile(ctx context.Context, path, language string) (Result, error) {
	pcm, err := audio.DecodeFile(path)
	if errors.Is(err, audio.ErrNotOggOpus) {
		return t.run(ctx, path, language)
	}
	if err != nil {
		return Result{}, err
	}
	return t.TranscribePCM(ctx, pcm, language)
}

// TranscribePCM transcribes 16 kHz mono PCM.
func (t *Transcriber) TranscribePCM(ctx context.Context, pcm []int16, language string) (Result, error) {
	f, err := os.CreateTemp("", "whatsapp-voice-*.wav")
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(audio.WAV(pcm))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Result{}, err
	}
	return t.run(ctx, f.Name(), language)
}

// Prepare finds whisper-cli and downloads the models if they are missing, so a
// server can do it at start instead of on the first voice note. It returns the
// model path.
func (t *Transcriber) Prepare(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.prepare(ctx); err != nil {
		return "", err
	}
	return t.path, nil
}

func (t *Transcriber) prepare(ctx context.Context) error {
	if t.cli == "" {
		cli, err := exec.LookPath(t.cfg.CLI)
		if err != nil {
			return fmt.Errorf("whisper-cli not found (%w): build it with whatsapp-mcp-go/media/transcribe/build-whisper.sh and set WHATSAPP_WHISPER_CLI", err)
		}
		t.cli = cli
	}
	if t.path == "" {
		path, err := t.modelPath(ctx)
		if err != nil {
			return err
		}
		t.path = path
	}
	if !t.cfg.NoVAD && t.vad == "" {
		vad := filepath.Join(t.cfg.CacheDir, filepath.Base(t.cfg.VADModelURL))
		if err := download(ctx, t.cfg.VADModelURL, vad); err != nil {
			return fmt.Errorf("VAD model: %w", err)
		}
		t.vad = vad
	}
	return nil
}

// modelPath resolves Config.Model to a file, downloading a named model.
func (t *Transcriber) modelPath(ctx context.Context) (string, error) {
	m := t.cfg.Model
	if strings.ContainsRune(m, os.PathSeparator) || strings.HasSuffix(m, ".bin") {
		if _, err := os.Stat(m); err != nil {
			return "", fmt.Errorf("whisper model: %w", err)
		}
		return m, nil
	}
	file, err := ModelFile(m)
	if err != nil {
		return "", err
	}
	path := filepath.Join(t.cfg.CacheDir, file)
	if err := download(ctx, fmt.Sprintf(t.cfg.ModelURL, file), path); err != nil {
		return "", fmt.Errorf("whisper model %q: %w", m, err)
	}
	return path, nil
}

var modelNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ModelFile maps a model name to its ggml file name. faster-whisper's aliases
// ("large", "turbo", "large-v3-turbo") map to the same weights; its distil-*
// models have no ggml conversion and are refused.
func ModelFile(name string) (string, error) {
	switch name {
	case "large":
		name = "large-v3"
	case "turbo":
		name = "large-v3-turbo"
	}
	if !modelNameRe.MatchString(name) {
		return "", fmt.Errorf("bad whisper model name %q", name)
	}
	if strings.HasPrefix(name, "distil-") {
		return "", fmt.Errorf("whisper model %q exists only for faster-whisper; use e.g. small, medium, large-v3 or turbo", name)
	}
	return "ggml-" + name + ".bin", nil
}

// cliOutput is the part of whisper-cli's --output-json file we read.
type cliOutput struct {
	Result struct {
		Language string `json:"language"`
	} `json:"result"`
	Transcription []struct {
		Text string `json:"text"`
	} `json:"transcription"`
}

func (t *Transcriber) run(ctx context.Context, wav, language string) (Result, error) {
	if language == "" || language == "auto" {
		language = "auto"
	} else if language = strings.ToLower(strings.TrimSpace(language)); !languageRe.MatchString(language) {
		return Result{}, fmt.Errorf("bad language %q: want an ISO 639-1 code such as en", language)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.prepare(ctx); err != nil {
		return Result{}, err
	}
	dir, err := os.MkdirTemp("", "whatsapp-whisper-")
	if err != nil {
		return Result{}, err
	}
	defer os.RemoveAll(dir)
	out := filepath.Join(dir, "out")
	args := []string{
		"--model", t.path, "--file", wav, "--language", language,
		"--threads", strconv.Itoa(t.cfg.Threads), "--no-prints", "--no-timestamps",
		"--output-json", "--output-file", out,
	}
	if t.vad != "" {
		args = append(args, "--vad", "--vad-model", t.vad)
	}
	cmd := exec.CommandContext(ctx, t.cli, args...) //nolint:gosec // G204: the configured whisper-cli with our own flags, no shell
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("whisper-cli: %w: %s", err, lastLines(stderr.String(), 5))
	}
	data, err := os.ReadFile(out + ".json") //nolint:gosec // G304: the output file in our own temp dir
	if err != nil {
		return Result{}, fmt.Errorf("whisper-cli wrote no transcript: %w: %s", err, lastLines(stderr.String(), 5))
	}
	return parseOutput(data, language)
}

func parseOutput(data []byte, language string) (Result, error) {
	var o cliOutput
	if err := json.Unmarshal(data, &o); err != nil {
		return Result{}, fmt.Errorf("whisper-cli output: %w", err)
	}
	parts := make([]string, 0, len(o.Transcription))
	for _, s := range o.Transcription {
		if s := strings.TrimSpace(s.Text); s != "" {
			parts = append(parts, s)
		}
	}
	text := strings.Join(parts, " ")
	if text == "" {
		return Result{}, ErrNoSpeech
	}
	if o.Result.Language != "" {
		language = o.Result.Language
	}
	return Result{Text: text, Language: language}, nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.Join(lines[max(len(lines)-n, 0):], " | ")
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.@-]`)

// TranscriptPath is where a message's transcript is cached:
// <dir>/<chat>_<id>.txt with unsafe characters replaced; existing caches use
// this name, so keep it stable. dir is the
// account's store/transcripts.
func TranscriptPath(dir, chatJID, messageID string) string {
	return filepath.Join(dir, unsafeName.ReplaceAllString(chatJID+"_"+messageID, "_")+".txt")
}

// ReadCached returns a cached transcript, or "" when there is none.
func ReadCached(dir, chatJID, messageID string) string {
	data, err := os.ReadFile(TranscriptPath(dir, chatJID, messageID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteCached stores a transcript, creating dir private to the user.
func WriteCached(dir, chatJID, messageID, text string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(TranscriptPath(dir, chatJID, messageID), []byte(text+"\n"), 0o600)
}
