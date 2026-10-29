package logging

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"regexp"
	"testing"
	"time"
)

func TestLines(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		log  func(l *slog.Logger)
		want string
	}{
		{"plain message", func(l *slog.Logger) { l.Info("WhatsApp MCP server on http://127.0.0.1:8091/mcp") },
			"WhatsApp MCP server on http://127.0.0.1:8091/mcp\n"},
		{"attributes", func(l *slog.Logger) { l.Info("tool failed", "tool", "send_message", "err", errors.New("no route")) },
			"tool failed tool=send_message err=\"no route\"\n"},
		{"levels other than info", func(l *slog.Logger) { l.Warn("event stream down", "retry_every", 10*time.Second) },
			"WARN event stream down retry_every=10s\n"},
		{"error level", func(l *slog.Logger) { l.Error("database error", "err", "x") }, "ERROR database error err=x\n"},
		{"debug is dropped", func(l *slog.Logger) { l.Debug("noise") }, ""},
		{"with attributes", func(l *slog.Logger) { l.With("account", "work").Info("event stream back") },
			"event stream back account=work\n"},
		{"with a group", func(l *slog.Logger) { l.WithGroup("req").Info("done", "id", 1) }, "done req.id=1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var b bytes.Buffer
			tc.log(New(&b))
			if b.String() != tc.want {
				t.Errorf("got %q, want %q", b.String(), tc.want)
			}
		})
	}
}

func TestTimeStampOnATerminalOnly(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	slog.New(NewHandler(&b, true)).Info("hello")
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d hello\n$`).MatchString(b.String()) {
		t.Errorf("got %q", b.String())
	}
	f, err := os.Create(t.TempDir() + "/log")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f) {
		t.Error("a file is a terminal")
	}
	if isTerminal(&b) {
		t.Error("a buffer is a terminal")
	}
}

func TestDiscard(t *testing.T) {
	t.Parallel()
	if Discard().Enabled(t.Context(), slog.LevelError) {
		t.Error("enabled")
	}
}
