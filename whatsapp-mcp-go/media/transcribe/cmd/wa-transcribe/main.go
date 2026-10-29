// Command wa-transcribe transcribes audio files the way the MCP server does,
// for trying a model or timing it: wa-transcribe [-l en] file.ogg...
// It reads the same WHATSAPP_WHISPER_* variables as the server.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/media/audio"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/media/transcribe"
)

func main() {
	language := flag.String("l", "", "language code; empty to detect")
	flag.Parse()
	w := config.Env(os.Getenv).Whisper
	t := transcribe.New(transcribe.Config{Model: w.Model, CLI: w.CLI, CacheDir: w.CacheDir, Threads: w.Threads})
	ctx := context.Background()
	start := time.Now()
	model, err := t.Prepare(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "model %s ready in %.1fs\n", model, time.Since(start).Seconds())
	failed := false
	for _, path := range flag.Args() {
		start := time.Now()
		res, err := t.TranscribeFile(ctx, path, *language)
		took := time.Since(start).Seconds()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
			continue
		}
		rtf := ""
		if pcm, err := audio.DecodeFile(path); err == nil {
			length := float64(len(pcm)) / audio.SampleRate
			rtf = fmt.Sprintf(", %.1fs audio, RTF %.3f", length, took/length)
		}
		fmt.Printf("%s: [%s] %s\n", path, res.Language, res.Text)
		fmt.Fprintf(os.Stderr, "%s: %.2fs%s\n", path, took, rtf)
	}
	if failed {
		os.Exit(1)
	}
}
