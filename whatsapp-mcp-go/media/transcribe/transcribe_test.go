package transcribe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const jfk = "../audio/testdata/jfk-voip16k.ogg"

// fakeCLI writes a whisper-cli stand-in that records its arguments in
// args.txt and writes out as the JSON transcript.
func fakeCLI(t *testing.T, out string) (cli, argsFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args.txt")
	jsonFile := filepath.Join(dir, "out.json")
	if err := os.WriteFile(jsonFile, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	cli = filepath.Join(dir, "whisper-cli")
	script := `#!/bin/sh
printf '%s\n' "$@" > '` + argsFile + `'
while [ $# -gt 0 ]; do
  case "$1" in
    --output-file) shift; cp '` + jsonFile + `' "$1.json" ;;
    --file) shift; [ -s "$1" ] || { echo "no input" >&2; exit 3; } ;;
  esac
  shift
done
`
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return cli, argsFile
}

// localModels returns a config whose model and VAD model are already in the
// cache, so nothing is downloaded.
func localModels(t *testing.T, cli string) Config {
	t.Helper()
	cache := t.TempDir()
	for _, f := range []string{"ggml-small.bin", "ggml-silero-v5.1.2.bin"} {
		if err := os.WriteFile(filepath.Join(cache, f), []byte("model"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Config{CLI: cli, CacheDir: cache, Threads: 3}
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func argAfter(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func TestTranscribeFileOgg(t *testing.T) {
	cli, argsFile := fakeCLI(t, `{"result":{"language":"en"},"transcription":[
		{"text":" And so, my fellow Americans,"},{"text":"  "},{"text":" ask not.\n"}]}`)
	cfg := localModels(t, cli)
	res, err := New(cfg).TranscribeFile(context.Background(), jfk, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "And so, my fellow Americans, ask not." || res.Language != "en" {
		t.Fatalf("got %+v", res)
	}
	args := readArgs(t, argsFile)
	if got := argAfter(args, "--language"); got != "auto" {
		t.Errorf("--language %q, want auto", got)
	}
	if got := argAfter(args, "--model"); got != filepath.Join(cfg.CacheDir, "ggml-small.bin") {
		t.Errorf("--model %q", got)
	}
	if got := argAfter(args, "--vad-model"); got != filepath.Join(cfg.CacheDir, "ggml-silero-v5.1.2.bin") {
		t.Errorf("--vad-model %q", got)
	}
	if got := argAfter(args, "--threads"); got != "3" {
		t.Errorf("--threads %q", got)
	}
	// Ogg is decoded here and handed over as a temporary WAV, removed after.
	wav := argAfter(args, "--file")
	if !strings.HasSuffix(wav, ".wav") {
		t.Errorf("--file %q is not the decoded WAV", wav)
	}
	if _, err := os.Stat(wav); !os.IsNotExist(err) {
		t.Errorf("temporary WAV %s left behind", wav)
	}
}

func TestTranscribeGivenLanguageAndNoVAD(t *testing.T) {
	cli, argsFile := fakeCLI(t, `{"result":{"language":"de"},"transcription":[{"text":"Hallo"}]}`)
	cfg := localModels(t, cli)
	cfg.NoVAD = true
	res, err := New(cfg).TranscribeFile(context.Background(), jfk, " DE ")
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "de" {
		t.Fatalf("language %q", res.Language)
	}
	args := readArgs(t, argsFile)
	if got := argAfter(args, "--language"); got != "de" {
		t.Errorf("--language %q", got)
	}
	for _, a := range args {
		if a == "--vad" {
			t.Error("--vad passed with NoVAD")
		}
	}
}

func TestTranscribeNonOggGoesStraightToCLI(t *testing.T) {
	cli, argsFile := fakeCLI(t, `{"transcription":[{"text":"hi"}]}`)
	mp3 := filepath.Join(t.TempDir(), "note.mp3")
	os.WriteFile(mp3, []byte("ID3 not really"), 0o644)
	res, err := New(localModels(t, cli)).TranscribeFile(context.Background(), mp3, "en")
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "hi" || res.Language != "en" {
		t.Fatalf("got %+v", res)
	}
	if got := argAfter(readArgs(t, argsFile), "--file"); got != mp3 {
		t.Fatalf("--file %q, want %q", got, mp3)
	}
}

func TestTranscribeNoSpeech(t *testing.T) {
	cli, _ := fakeCLI(t, `{"result":{"language":"en"},"transcription":[]}`)
	_, err := New(localModels(t, cli)).TranscribeFile(context.Background(), jfk, "")
	if !errors.Is(err, ErrNoSpeech) {
		t.Fatalf("err = %v, want ErrNoSpeech", err)
	}
}

func TestTranscribeErrors(t *testing.T) {
	cli, _ := fakeCLI(t, `{}`)
	tr := New(localModels(t, cli))
	if _, err := tr.TranscribeFile(context.Background(), jfk, "--help"); err == nil || !strings.Contains(err.Error(), "bad language") {
		t.Errorf("bad language: %v", err)
	}
	if _, err := tr.TranscribeFile(context.Background(), "/nonexistent.ogg", ""); err == nil {
		t.Error("missing file accepted")
	}
	missing := New(Config{CLI: "/nonexistent/whisper-cli", CacheDir: t.TempDir()})
	if _, err := missing.TranscribeFile(context.Background(), jfk, ""); err == nil || !strings.Contains(err.Error(), "build-whisper.sh") {
		t.Errorf("missing CLI: %v", err)
	}
	failing := filepath.Join(t.TempDir(), "whisper-cli")
	os.WriteFile(failing, []byte("#!/bin/sh\necho 'error: failed to load model' >&2\nexit 1\n"), 0o755)
	if _, err := New(localModels(t, failing)).TranscribeFile(context.Background(), jfk, ""); err == nil || !strings.Contains(err.Error(), "failed to load model") {
		t.Errorf("CLI failure: %v", err)
	}
}

func TestModelFile(t *testing.T) {
	for name, want := range map[string]string{
		"small": "ggml-small.bin", "large": "ggml-large-v3.bin", "turbo": "ggml-large-v3-turbo.bin",
		"small.en": "ggml-small.en.bin", "small-q8_0": "ggml-small-q8_0.bin",
	} {
		if got, err := ModelFile(name); err != nil || got != want {
			t.Errorf("ModelFile(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, bad := range []string{"", "../x", "Small", "distil-large-v3"} {
		if _, err := ModelFile(bad); err == nil {
			t.Errorf("ModelFile(%q) accepted", bad)
		}
	}
}

func TestModelPath(t *testing.T) {
	cli, argsFile := fakeCLI(t, `{"transcription":[{"text":"x"}]}`)
	cfg := localModels(t, cli)
	cfg.Model = filepath.Join(t.TempDir(), "custom.bin")
	os.WriteFile(cfg.Model, []byte("m"), 0o644)
	if _, err := New(cfg).TranscribeFile(context.Background(), jfk, ""); err != nil {
		t.Fatal(err)
	}
	if got := argAfter(readArgs(t, argsFile), "--model"); got != cfg.Model {
		t.Fatalf("--model %q, want %q", got, cfg.Model)
	}
	cfg.Model = "/nonexistent/model.bin"
	if _, err := New(cfg).Prepare(context.Background()); err == nil {
		t.Fatal("missing model file accepted")
	}
}

func TestPrepareDownloadsModels(t *testing.T) {
	model, vad := []byte("fake ggml weights"), []byte("fake vad")
	sum := sha256.Sum256(model)
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/resolve/ggml-tiny.bin": // Hugging Face style: hash on the redirect
			w.Header().Set("X-Linked-Etag", `"`+hex.EncodeToString(sum[:])+`"`)
			http.Redirect(w, r, "/blob/tiny", http.StatusFound)
		case "/blob/tiny":
			w.Write(model)
		case "/vad.bin":
			w.Write(vad)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cli, _ := fakeCLI(t, `{}`)
	cfg := Config{CLI: cli, CacheDir: filepath.Join(t.TempDir(), "cache"), Model: "tiny",
		ModelURL: srv.URL + "/resolve/%s", VADModelURL: srv.URL + "/vad.bin"}
	path, err := New(cfg).Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(model) {
		t.Fatalf("model file holds %q", got)
	}
	if got, _ := os.ReadFile(filepath.Join(cfg.CacheDir, "vad.bin")); string(got) != string(vad) {
		t.Fatalf("VAD file holds %q", got)
	}
	// Second use: from the cache, no requests.
	before := hits.Load()
	if _, err := New(cfg).Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != before {
		t.Fatal("cached models downloaded again")
	}

	cfg.Model = "base"
	if _, err := New(cfg).Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("missing model: %v", err)
	}
	entries, _ := os.ReadDir(cfg.CacheDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part-") || e.Name() == "ggml-base.bin" {
			t.Errorf("failed download left %s", e.Name())
		}
	}
}

func TestDownloadRejectsWrongHash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Linked-Etag", `"`+strings.Repeat("0", 64)+`"`)
		w.Write([]byte("corrupted"))
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "m.bin")
	if err := download(context.Background(), srv.URL, path); err == nil || !strings.Contains(err.Error(), "sha256") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("corrupted model kept")
	}
}

func TestTranscriptCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "transcripts")
	// The cache file name existing transcript caches use.
	want := filepath.Join(dir, "123@s.whatsapp.net_3EB0_A_B.txt")
	if got := TranscriptPath(dir, "123@s.whatsapp.net", "3EB0/A B"); got != want {
		t.Fatalf("TranscriptPath = %q, want %q", got, want)
	}
	if got := ReadCached(dir, "c@g.us", "m1"); got != "" {
		t.Fatalf("empty cache returned %q", got)
	}
	if err := WriteCached(dir, "c@g.us", "m1", "hello there"); err != nil {
		t.Fatal(err)
	}
	if got := ReadCached(dir, "c@g.us", "m1"); got != "hello there" {
		t.Fatalf("ReadCached = %q", got)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode().Perm())
	}
}

// realTranscriber returns a Transcriber on a real whisper-cli, or skips: set
// WHATSAPP_WHISPER_TEST_CLI (and WHATSAPP_WHISPER_CACHE to reuse downloaded
// models) to run the integration tests and benchmarks.
func realTranscriber(tb testing.TB) *Transcriber {
	tb.Helper()
	cli := os.Getenv("WHATSAPP_WHISPER_TEST_CLI")
	if cli == "" {
		tb.Skip("WHATSAPP_WHISPER_TEST_CLI not set")
	}
	cfg := Config{CLI: cli, Model: os.Getenv("WHATSAPP_WHISPER_MODEL"), CacheDir: os.Getenv("WHATSAPP_WHISPER_CACHE")}
	t := New(cfg)
	if _, err := t.Prepare(context.Background()); err != nil {
		tb.Fatal(err)
	}
	return t
}

func TestIntegrationSpeech(t *testing.T) {
	tr := realTranscriber(t)
	res, err := tr.TranscribeFile(context.Background(), jfk, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Language != "en" || !strings.Contains(strings.ToLower(res.Text), "ask not what your country can do for you") {
		t.Fatalf("got %+v", res)
	}
}

func TestIntegrationToneIsNoSpeech(t *testing.T) {
	tr := realTranscriber(t)
	if res, err := tr.TranscribeFile(context.Background(), "../audio/testdata/tone-voip16k.ogg", ""); !errors.Is(err, ErrNoSpeech) {
		t.Fatalf("got %+v, %v; want ErrNoSpeech", res, err)
	}
}

// BenchmarkTranscribe times an 11 s voice note end to end (decode, model load,
// whisper); the real-time factor is ns/op / 11e9.
func BenchmarkTranscribe(b *testing.B) {
	tr := realTranscriber(b)
	for b.Loop() {
		if _, err := tr.TranscribeFile(context.Background(), jfk, "en"); err != nil {
			b.Fatal(err)
		}
	}
}
