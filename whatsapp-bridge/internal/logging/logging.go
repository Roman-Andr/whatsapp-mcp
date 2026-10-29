package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
)

// New returns a text logger at Info level writing to w. Off a terminal the
// time stamp is dropped: under systemd the log goes to journald, which adds
// its own.
func New(w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if !isTerminal(w) {
		opts.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		}
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// WA adapts l for whatsmeow. module names the logger as waLog's does
// ("Client", "Database"; Sub appends "/<name>"). Lines below l's level are
// dropped, so Debugf is silent at Info as with waLog.Stdout(…, "INFO", …).
func WA(l *slog.Logger, module string) waLog.Logger {
	return &waLogger{l: l, module: module}
}

type waLogger struct {
	l      *slog.Logger
	module string
}

func (w *waLogger) log(level slog.Level, format string, args []any) {
	// waLog's calls carry no context, so the record is handled detached.
	ctx := context.Background()
	if !w.l.Enabled(ctx, level) {
		return
	}
	// whatsmeow's messages are printf formats, so the record is built
	// directly rather than through a static-message call.
	r := slog.NewRecord(time.Now(), level, fmt.Sprintf(format, args...), 0)
	r.AddAttrs(slog.String("module", w.module))
	_ = w.l.Handler().Handle(ctx, r)
}

func (w *waLogger) Debugf(msg string, args ...any) { w.log(slog.LevelDebug, msg, args) }
func (w *waLogger) Infof(msg string, args ...any)  { w.log(slog.LevelInfo, msg, args) }
func (w *waLogger) Warnf(msg string, args ...any)  { w.log(slog.LevelWarn, msg, args) }
func (w *waLogger) Errorf(msg string, args ...any) { w.log(slog.LevelError, msg, args) }

func (w *waLogger) Sub(module string) waLog.Logger {
	return &waLogger{l: w.l, module: w.module + "/" + module}
}
