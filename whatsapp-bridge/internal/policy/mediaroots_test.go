package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMediaRootsResolve(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	write := func(path string) string {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	inside := write(filepath.Join(root, "sub", "photo.jpg"))
	secret := write(filepath.Join(outside, "id_rsa"))
	hidden := write(filepath.Join(root, ".git", "config"))
	escape := filepath.Join(root, "escape.jpg")
	if err := os.Symlink(secret, escape); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(root, "alias.jpg")
	if err := os.Symlink(inside, inner); err != nil {
		t.Fatal(err)
	}
	roots, err := NewMediaRoots([]string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	realInside, _ := filepath.EvalSymlinks(inside)

	for _, c := range []struct {
		name, path string
	}{
		{"file inside", inside},
		{"symlink that stays inside the root", inner},
		{"path with ..", filepath.Join(root, "sub", "..", "sub", "photo.jpg")},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got, err := roots.Resolve(c.path); err != nil || got != realInside {
				t.Errorf("Resolve(%s) = %q, %v; want %q", c.path, got, err, realInside)
			}
		})
	}
	for _, c := range []struct {
		name, path, wantErr string
		refused             bool
	}{
		{"file outside", secret, "outside the media roots", true},
		{"symlink out of the root", escape, "outside the media roots", true},
		{".. out of the root", filepath.Join(root, "..", filepath.Base(outside), "id_rsa"), "outside the media roots", true},
		{"hidden directory", hidden, "hidden", true},
		{"missing file", filepath.Join(root, "missing.jpg"), "not found", false},
		{"relative path", "relative/photo.jpg", "must be absolute", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := roots.Resolve(c.path)
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("Resolve(%s): %v, want %q", c.path, err, c.wantErr)
			}
			if errors.Is(err, ErrOutsideMediaRoots) != c.refused {
				t.Errorf("Resolve(%s): refused = %v, want %v", c.path, !c.refused, c.refused)
			}
		})
	}
	t.Run("no roots send nothing", func(t *testing.T) {
		t.Parallel()
		var none *MediaRoots
		if _, err := none.Resolve(inside); !errors.Is(err, ErrOutsideMediaRoots) {
			t.Errorf("a bridge without media roots sent a file: %v", err)
		}
	})
	t.Run("/ is no root", func(t *testing.T) {
		t.Parallel()
		if _, err := NewMediaRoots([]string{"/"}, ""); err == nil {
			t.Error("/ accepted as a media root")
		}
	})
}

func TestMediaRootsDefaults(t *testing.T) {
	// The default roots include the relative store/ directory.
	t.Chdir(t.TempDir())
	if err := os.Mkdir("store", 0700); err != nil {
		t.Fatal(err)
	}
	outbox := filepath.Join(t.TempDir(), "whatsapp-mcp", "outbox")
	roots, err := NewMediaRoots(nil, outbox)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(outbox); err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		t.Fatalf("outbox not created private: %v %v", st, err)
	}
	store, _ := filepath.Abs("store")
	want := []string{resolveDir(store), resolveDir(outbox), resolveDir(os.TempDir())}
	if got := roots.Dirs(); strings.Join(got, ":") != strings.Join(want, ":") {
		t.Errorf("default roots %v, want %v", got, want)
	}
	if roots.Outbox() != resolveDir(outbox) {
		t.Errorf("outbox %q", roots.Outbox())
	}
	home, _ := os.UserHomeDir()
	if _, err := roots.Resolve(filepath.Join(home, ".ssh", "id_rsa")); err == nil {
		t.Error("~/.ssh is sendable by default")
	}
}

func TestResolveMediaRootsExpandsHome(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	got, err := ResolveMediaRoots([]string{"/srv/a", "~/Outgoing"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "/srv/a" || got[1] != filepath.Join(home, "Outgoing") {
		t.Errorf("configured roots %v", got)
	}
}

type metaMap map[string]string

func (m metaMap) SetMeta(_ context.Context, key, value string) error {
	m[key] = value
	return nil
}

func TestMediaRootsPublish(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	roots, err := NewMediaRoots([]string{root}, filepath.Join(root, "outbox"))
	if err != nil {
		t.Fatal(err)
	}
	meta := metaMap{}
	if err := roots.Publish(t.Context(), meta); err != nil {
		t.Fatal(err)
	}
	resolved, _ := filepath.EvalSymlinks(root)
	if meta["media_roots"] != `["`+resolved+`"]` || meta["media_outbox"] != filepath.Join(resolved, "outbox") {
		t.Errorf("published %v", meta)
	}
}
