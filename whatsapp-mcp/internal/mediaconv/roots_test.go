package mediaconv

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/storetest"
)

func TestMediaRoots(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"an older bridge publishes none", "", nil},
		{"a list", `["/a", "/b"]`, []string{"/a", "/b"}},
		{"an unreadable list allows nothing", `{nope`, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := storetest.New(t, storetest.Options{})
			if tc.raw != "" {
				f.Exec("INSERT INTO bridge_meta (key, value) VALUES ('media_roots', ?)", tc.raw)
			}
			if got := MediaRoots(t.Context(), f.DB()); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCheckSendable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	inside := filepath.Join(root, "photo.jpg")
	hidden := filepath.Join(root, ".secret", "key.pem")
	outside := filepath.Join(t.TempDir(), "photo.jpg")
	for _, p := range []string{inside, hidden, outside} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(root, "link.jpg")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		roots []string
		path  string
		want  string
	}{
		{"no roots published", nil, outside, ""},
		{"inside a root", []string{root}, inside, ""},
		{"a hidden directory", []string{root}, hidden,
			"refusing to send " + hidden + ": hidden files and directories cannot be sent"},
		{"outside the roots", []string{root}, outside,
			"refusing to send " + outside + ": it is outside the media roots (" + root + "). " +
				"Copy it into one of them, or add its directory to WHATSAPP_MEDIA_ROOTS"},
		{"a link out of a root", []string{root}, link,
			"refusing to send " + link + ": it is outside the media roots (" + root + "). " +
				"Copy it into one of them, or add its directory to WHATSAPP_MEDIA_ROOTS"},
		{"the root itself", []string{root}, root,
			"refusing to send " + root + ": it is outside the media roots (" + root + "). " +
				"Copy it into one of them, or add its directory to WHATSAPP_MEDIA_ROOTS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := storetest.New(t, storetest.Options{})
			if tc.roots != nil {
				f.PublishMediaRoots(tc.roots, "")
			}
			if got := CheckSendable(t.Context(), f.DB(), tc.path); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOutbox(t *testing.T) {
	t.Parallel()
	outbox := t.TempDir()
	cases := []struct {
		name, published, want string
	}{
		{"an older bridge has none", "", ""},
		{"the published directory", outbox, outbox},
		{"a missing directory", filepath.Join(outbox, "missing"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := storetest.New(t, storetest.Options{})
			f.PublishMediaRoots([]string{outbox}, tc.published)
			if got := Outbox(t.Context(), f.DB()); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
