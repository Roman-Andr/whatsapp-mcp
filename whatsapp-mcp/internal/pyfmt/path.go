package pyfmt

import (
	"os"
	"path/filepath"
	"strings"
)

// ExpandUser is os.path.expanduser for "~" and "~/...".
func ExpandUser(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[1:])
		}
	}
	return path
}

// AbsPath is os.path.abspath(os.path.expanduser(path)); path itself when
// it cannot be made absolute.
func AbsPath(path string) string {
	p, err := filepath.Abs(ExpandUser(path))
	if err != nil {
		return path
	}
	return p
}
