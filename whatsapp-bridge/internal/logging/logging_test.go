package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewDropsTheTimeOffTerminal(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	New(&buf).Info("started", "addr", "127.0.0.1:8080")
	if got := buf.String(); got != "level=INFO msg=started addr=127.0.0.1:8080\n" {
		t.Errorf("got %q", got)
	}
}

func TestWAAdapter(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	l := WA(New(&buf), "Client")
	l.Debugf("hidden %d", 1)
	l.Infof("connected to %s", "web")
	l.Sub("Socket").Warnf("frame %d dropped", 7)
	l.Errorf("failed: %v", "boom")
	want := []string{
		`level=INFO msg="connected to web" module=Client`,
		`level=WARN msg="frame 7 dropped" module=Client/Socket`,
		`level=ERROR msg="failed: boom" module=Client`,
	}
	if got := strings.Split(strings.TrimSpace(buf.String()), "\n"); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWARespectsTheLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	debug := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	WA(debug, "Database").Debugf("query %s", "x")
	if !strings.Contains(buf.String(), `msg="query x"`) {
		t.Errorf("debug line missing at debug level: %q", buf.String())
	}
}
