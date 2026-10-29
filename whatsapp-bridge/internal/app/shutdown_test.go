package app

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestShutdownStepsAreBounded(t *testing.T) {
	logs := &syncBuffer{}
	a := &app{log: slog.New(slog.NewTextHandler(logs, nil))}
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	a.step(t.Context(), "stuck", 20*time.Millisecond, func(context.Context) { <-release })
	if waited := time.Since(start); waited > time.Second {
		t.Errorf("waited %s for a stuck step", waited)
	}
	if !strings.Contains(logs.String(), `msg="shutdown step did not finish in time" step=stuck`) {
		t.Errorf("log:\n%s", logs.String())
	}
}
