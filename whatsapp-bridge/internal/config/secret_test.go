package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecretGeneratesAndReuses(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "api_token")
	first, err := Secret("", path, nil)
	if err != nil || len(first) != 64 {
		t.Fatalf("secret %q, err %v", first, err)
	}
	t.Run("file is private", func(t *testing.T) {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != 0600 {
			t.Errorf("mode %v %v", st, err)
		}
	})
	t.Run("kept across starts", func(t *testing.T) {
		if second, _ := Secret("   ", path, nil); second != first {
			t.Error("secret regenerated")
		}
	})
	t.Run("configured value wins", func(t *testing.T) {
		if got, _ := Secret("  from-env  ", path, nil); got != "from-env" {
			t.Errorf("got %q", got)
		}
	})
}

func TestSecretWriteError(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing", "webhook_secret")
	if _, err := Secret("", path, nil); err == nil || err.Error() != "failed to write "+path+": open "+path+": no such file or directory" {
		t.Errorf("error %v", err)
	}
}
