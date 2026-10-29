package audit

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"
	"unicode/utf8"
)

// previewRunes is how much of a sent text an entry keeps.
const previewRunes = 200

// Entry is one line of store/audit.log.
type Entry struct {
	Time      time.Time `json:"time"`
	Endpoint  string    `json:"endpoint"`
	Chat      string    `json:"chat,omitempty"`
	MessageID string    `json:"message_id,omitempty"` // message created by the call (sent, reaction, edit, ...)
	TargetID  string    `json:"target_id,omitempty"`  // message the call acted on (reacted to, edited, ...)
	Text      string    `json:"text,omitempty"`       // preview of what was sent
	Success   bool      `json:"success"`
	Result    string    `json:"result"`
}

// Log appends one JSON line per state-changing API call.
type Log struct {
	mu   sync.Mutex
	file *os.File
	log  *slog.Logger
}

// Open opens (or creates, mode 0600) the log at path for appending. Write
// errors are reported to logger, which may be nil.
func Open(path string, logger *slog.Logger) (*Log, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0600) //nolint:gosec // the path is the bridge's own store/audit.log
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log: %w", err)
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Log{file: f, log: logger}, nil
}

// Record writes an entry. A nil log (tests) records nothing; a write error is
// logged but never fails the call that was already made.
func (l *Log) Record(e Entry) {
	if l == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Text = Preview(e.Text, previewRunes)
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, err := l.file.Write(append(line, '\n')); err != nil {
		l.log.Warn("failed to write audit log", "err", err)
	}
}

// Close closes the file. Closing a nil log does nothing.
func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	return l.file.Close()
}

// Preview shortens text to at most n runes.
func Preview(text string, n int) string {
	if utf8.RuneCountInString(text) <= n {
		return text
	}
	runes := []rune(text)
	return string(runes[:n-1]) + "…"
}
