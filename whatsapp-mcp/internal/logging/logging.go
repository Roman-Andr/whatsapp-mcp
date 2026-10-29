package logging

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

// New is the logger writing to w. On a terminal a line starts with the
// time; elsewhere (journald, a file) it does not. Info is the default
// level and is not written; other levels start the line ("WARN ...").
func New(w io.Writer) *slog.Logger {
	return slog.New(NewHandler(w, isTerminal(w)))
}

// Discard is a logger that writes nothing, for tests and tools that log
// nowhere.
func Discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// Handler writes "[time ][LEVEL ]message key=value ..." lines. The
// attributes are formatted by slog's text handler, so quoting follows its
// rules.
type Handler struct {
	out   *output
	stamp bool
	// attrs formats the attributes into out.buf; time, level and message
	// are left out of it and written by Handle.
	attrs slog.Handler
}

// output is the writer and scratch buffer the handlers derived from one
// New share.
type output struct {
	mu  sync.Mutex
	w   io.Writer
	buf bytes.Buffer
}

// NewHandler is the handler writing to w, with time stamps when stamp is
// set.
func NewHandler(w io.Writer, stamp bool) *Handler {
	out := &output{w: w}
	return &Handler{out: out, stamp: stamp, attrs: slog.NewTextHandler(&out.buf, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && (a.Key == slog.TimeKey || a.Key == slog.LevelKey || a.Key == slog.MessageKey) {
				return slog.Attr{}
			}
			return a
		},
	})}
}

// Enabled reports Info and above.
func (h *Handler) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelInfo }

// Handle writes one line.
func (h *Handler) Handle(ctx context.Context, r slog.Record) error {
	h.out.mu.Lock()
	defer h.out.mu.Unlock()
	h.out.buf.Reset()
	if err := h.attrs.Handle(ctx, r); err != nil {
		return err
	}
	attrs := bytes.TrimSpace(h.out.buf.Bytes())
	var line []byte
	if h.stamp && !r.Time.IsZero() {
		line = r.Time.AppendFormat(line, time.DateTime)
		line = append(line, ' ')
	}
	if r.Level != slog.LevelInfo {
		line = append(line, r.Level.String()...)
		line = append(line, ' ')
	}
	line = append(line, r.Message...)
	if len(attrs) > 0 {
		line = append(line, ' ')
		line = append(line, attrs...)
	}
	line = append(line, '\n')
	_, err := h.out.w.Write(line)
	return err
}

// WithAttrs returns a handler that adds attrs to every line.
func (h *Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &Handler{out: h.out, stamp: h.stamp, attrs: h.attrs.WithAttrs(attrs)}
}

// WithGroup returns a handler that puts the following attributes in group.
func (h *Handler) WithGroup(name string) slog.Handler {
	return &Handler{out: h.out, stamp: h.stamp, attrs: h.attrs.WithGroup(name)}
}
