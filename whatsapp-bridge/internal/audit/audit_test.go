package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPreviewShortensByRunes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in string
		n        int
		want     string
	}{
		{"cut with an ellipsis", "привет мир", 6, "приве…"},
		{"short text kept", "short", 10, "short"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := Preview(c.in, c.n); got != c.want {
				t.Errorf("Preview = %q, want %q", got, c.want)
			}
		})
	}
}

func TestLogAppendsPrivateJSONLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "audit.log")
	l, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	l.Record(Entry{Endpoint: "send", Chat: "c@s.whatsapp.net", Text: strings.Repeat("x", 300), Success: true, Result: "ok"})
	l.Record(Entry{Endpoint: "react", Time: time.Unix(1, 0)})
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("mode %v %v", st, err)
	}
	data, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines", len(lines))
	}
	var first Entry
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Time.IsZero() || len([]rune(first.Text)) != previewRunes || first.Endpoint != "send" {
		t.Errorf("entry %+v", first)
	}
}

func TestNilLogRecordsNothing(t *testing.T) {
	t.Parallel()
	var l *Log
	l.Record(Entry{Endpoint: "send"})
	if err := l.Close(); err != nil {
		t.Error(err)
	}
}
