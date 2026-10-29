package export

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/render"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/storetest"
)

const group = storetest.Group

// exportChat exports with the fixture's current settings, downloading
// media as download_media does.
func exportChat(t *testing.T, f *storetest.Fixture, format, after string, includeMedia bool, out string) *pyfmt.OMap {
	t.Helper()
	d := f.DB()
	a := f.Account()
	bridge := &bridgeapi.Client{BaseURL: a.APIURL, Token: a.Token, HTTP: bridgeapi.NewHTTPClient()}
	download := func(ctx context.Context, messageID, chatJID string) *pyfmt.OMap {
		if hidden := d.CheckReadable(ctx, chatJID); hidden != "" {
			return render.Refused(hidden)
		}
		return bridge.Download(ctx, messageID, chatJID)
	}
	return Chat(t.Context(), d, download, group, format, after, "", includeMedia, out)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExportText(t *testing.T) {
	t.Parallel()
	s := storetest.New(t, storetest.Options{})
	res := exportChat(t, s, "txt", "", false, "")
	path := pyfmt.AsString(res.Get("path"))
	if want := `{"success": true, "path": "` + path + `", "messages": 3, "media": 0}`; pyfmt.Compact(res) != want {
		t.Fatalf("got %s, want %s", pyfmt.Compact(res), want)
	}
	if dir := filepath.Join(s.Dir, "exports"); filepath.Dir(path) != dir {
		t.Errorf("written to %s, not %s", path, dir)
	}
	want := "Snow operations system (" + group + ")\n\n" +
		"[2026-10-27 12:00:00 +03:00] David Bender: The plow route on Main street is wrong\n" +
		"[2026-10-27 12:01:00 +03:00] Me: Fixed it (edited) [👍 David Bender]\n" +
		"[2026-10-27 12:05:00 +03:00] Antonina: <image omitted> see screenshot (deleted by sender)\n"
	if got := readFile(t, path); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestExportJSON(t *testing.T) {
	t.Parallel()
	s := storetest.New(t, storetest.Options{})
	res := exportChat(t, s, "json", "2026-10-27T12:00:30+03:00", false, filepath.Join(t.TempDir(), "out.json"))
	decoded, err := pyfmt.DecodeOrdered([]byte(readFile(t, pyfmt.AsString(res.Get("path")))))
	if err != nil {
		t.Fatal(err)
	}
	var ids []any
	for _, m := range decoded.(*pyfmt.OMap).Get("messages").([]any) {
		ids = append(ids, m.(*pyfmt.OMap).Get("id"))
	}
	if !reflect.DeepEqual(ids, []any{"A2", "A3"}) {
		t.Errorf("json ids %v", ids)
	}
}

func TestExportWithMedia(t *testing.T) {
	t.Parallel()
	s := storetest.New(t, storetest.Options{})
	pic := filepath.Join(t.TempDir(), "pic.jpg")
	if err := os.WriteFile(pic, []byte("\xff\xd8jpeg"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := storetest.NewBridge(t, s)
	b.Answer = func(w http.ResponseWriter, r *http.Request, payload map[string]any) {
		if payload["message_id"] == "A4" {
			w.Write([]byte(`{"success": false, "message": "expired"}`))
			return
		}
		w.Write([]byte(`{"success": true, "path": "` + pic + `"}`))
	}
	s.Exec(`UPDATE messages SET deleted_at = NULL WHERE id = 'A3';
		INSERT INTO messages (id, chat_jid, sender, content, timestamp, is_from_me, media_type)
			VALUES ('A4', '` + group + `', '5', '', '2026-10-27 12:07:00+03:00', 0, 'video')`)
	res := exportChat(t, s, "html", "", true, "")
	path := pyfmt.AsString(res.Get("path"))
	if want := `{"success": true, "path": "` + path + `", "messages": 4, "media": 1, "media_failed": ["A4"]}`; pyfmt.Compact(res) != want {
		t.Fatalf("got %s, want %s", pyfmt.Compact(res), want)
	}
	if filepath.Ext(path) != ".zip" {
		t.Errorf("not a ZIP: %s", path)
	}
	names, page := readZip(t, path)
	if want := []string{"Snow_operations_system.html", "A3_pic.jpg"}; !reflect.DeepEqual(names, want) {
		t.Errorf("zip %v, want %v", names, want)
	}
	if !strings.Contains(page, `<img src="A3_pic.jpg"`) {
		t.Errorf("no image in:\n%s", page)
	}
}

func TestExportRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		format string
		after  string
		setup  func(f *storetest.Fixture)
		want   string
	}{
		{"unknown format", "pdf", "", nil, "format must be one of txt, json, html"},
		{"hidden chat", "txt", "", func(f *storetest.Fixture) { f.PublishReadAllowlist("79966764464@s.whatsapp.net") },
			"Chat " + group + " is not in the read allowlist (WHATSAPP_READ_ALLOWLIST)"},
		{"nothing in range", "txt", "2030-01-01T00:00:00Z", nil, "No messages stored for " + group + " in that range"},
		{"bad date", "txt", "soon", nil, "Invalid date format for 'after': soon. Please use ISO-8601 format."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := storetest.New(t, storetest.Options{})
			if tc.setup != nil {
				tc.setup(s)
			}
			want := pyfmt.Compact(render.Refused(tc.want))
			if got := pyfmt.Compact(exportChat(t, s, tc.format, tc.after, false, "")); got != want {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

// Exports are private whatever the process umask: the file 0600, the
// exports directory 0700, and a file that existed with another mode is
// made 0600 too.
func TestExportModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		media    bool
		existing bool
	}{
		{"text", false, false},
		{"ZIP", true, false},
		{"over a readable file", false, true},
		{"ZIP over a readable file", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := storetest.New(t, storetest.Options{})
			storetest.NewBridge(t, s)
			out := ""
			if tc.existing {
				out = filepath.Join(t.TempDir(), "chat.out")
				if err := os.WriteFile(out, []byte("old"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			res := exportChat(t, s, "txt", "", tc.media, out)
			path := pyfmt.AsString(res.Get("path"))
			st, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if st.Mode().Perm() != 0o600 {
				t.Errorf("file mode %v", st.Mode().Perm())
			}
			if tc.existing {
				return
			}
			dir, err := os.Stat(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			if dir.Mode().Perm() != 0o700 {
				t.Errorf("exports dir mode %v", dir.Mode().Perm())
			}
		})
	}
}

func TestMainCommand(t *testing.T) {
	t.Parallel()
	s := storetest.New(t, storetest.Options{})
	out := filepath.Join(t.TempDir(), "chat.txt")
	cases := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"exports", []string{group, "--out", out}, 0, `{"success": true, "path": "` + out + `", "messages": 3, "media": 0}` + "\n", ""},
		{"flags before the chat", []string{"--format", "pdf", group}, 1, `{"success": false, "message": "format must be one of txt, json, html"}` + "\n", ""},
		{"no chat", nil, 2, "", "usage: whatsapp-mcp export <chat JID> [--format txt|json|html] [--after ISO] [--before ISO] [--media] [--out PATH] [--account NAME]\n"},
		{"two chats", []string{group, group}, 2, "", "usage: whatsapp-mcp export <chat JID> [--format txt|json|html] [--after ISO] [--before ISO] [--media] [--out PATH] [--account NAME]\n"},
		{"unknown account", []string{group, "--account", "work"}, 1, "", "Unknown WhatsApp account 'work'; configured: default\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := Main(t.Context(), s.Config, logging.Discard(), tc.args, &stdout, &stderr)
			if code != tc.code || stdout.String() != tc.stdout || stderr.String() != tc.stderr {
				t.Errorf("got %d %q %q, want %d %q %q", code, stdout.String(), stderr.String(), tc.code, tc.stdout, tc.stderr)
			}
		})
	}
	t.Run("bad flag", func(t *testing.T) {
		t.Parallel()
		var stdout, stderr bytes.Buffer
		if code := Main(t.Context(), s.Config, logging.Discard(), []string{"--bogus"}, &stdout, &stderr); code != 2 {
			t.Errorf("exit %d", code)
		}
	})
}

func readZip(t *testing.T, path string) ([]string, string) {
	t.Helper()
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	var names []string
	page := ""
	for _, f := range z.File {
		names = append(names, f.Name)
		if strings.HasSuffix(f.Name, ".html") {
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(r)
			r.Close()
			page = string(data)
		}
	}
	if slices.Contains(names, "") {
		t.Errorf("empty name in %v", names)
	}
	return names, page
}
