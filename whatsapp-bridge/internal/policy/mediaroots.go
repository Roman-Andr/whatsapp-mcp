package policy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideMediaRoots marks a path refused by the media roots (HTTP 403).
// The refusal's own text says why; match it with errors.Is.
var ErrOutsideMediaRoots = errors.New("outside the media roots")

// refusal is a path refused by the media roots. Its text is the response.
type refusal struct{ msg string }

func (e *refusal) Error() string        { return e.msg }
func (e *refusal) Is(target error) bool { return target == ErrOutsideMediaRoots }

// MetaWriter stores a bridge_meta value; *store.Store is one.
type MetaWriter interface {
	SetMeta(ctx context.Context, key, value string) error
}

// MediaRoots are the directories files may be sent from. Without them any
// file the bridge's user can read (~/.ssh, a password store, other projects)
// is one prompt-injected send_file away from leaving the machine.
type MediaRoots struct {
	dirs []string // absolute, cleaned, symlinks resolved
	// outbox is the default directory for files staged for sending; the MCP
	// server writes converted stickers and voice messages there.
	outbox string
}

// NewMediaRoots creates outbox (mode 0700) when it is set and resolves
// entries. Nil entries mean the defaults: the bridge's store/ (downloaded
// media), the outbox and the system temp directory, where the MCP server's
// helpers convert files. "~/" is expanded; other relative paths are taken
// relative to the bridge directory.
func NewMediaRoots(entries []string, outbox string) (*MediaRoots, error) {
	if entries == nil {
		entries = []string{"store", outbox, os.TempDir()}
	}
	if outbox != "" {
		if err := os.MkdirAll(outbox, 0700); err != nil {
			return nil, fmt.Errorf("failed to create media outbox %s: %w", outbox, err)
		}
	}
	dirs, err := ResolveMediaRoots(entries)
	if err != nil {
		return nil, err
	}
	r := &MediaRoots{dirs: dirs}
	if outbox != "" {
		r.outbox = resolveDir(outbox)
	}
	return r, nil
}

// ResolveMediaRoots returns the effective directories of entries, or why
// one cannot be a root. It changes nothing, so configuration loading uses
// it to validate WHATSAPP_MEDIA_ROOTS.
func ResolveMediaRoots(entries []string) ([]string, error) {
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e == "~" || strings.HasPrefix(e, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("WHATSAPP_MEDIA_ROOTS: cannot expand %s: %w", e, err)
			}
			e = filepath.Join(home, strings.TrimPrefix(e, "~"))
		}
		abs, err := filepath.Abs(e)
		if err != nil {
			return nil, fmt.Errorf("WHATSAPP_MEDIA_ROOTS: %w", err)
		}
		if abs == string(filepath.Separator) {
			return nil, errors.New("WHATSAPP_MEDIA_ROOTS: / would allow sending every file")
		}
		dirs = append(dirs, resolveDir(abs))
	}
	return dirs, nil
}

// resolveDir resolves symlinks of a directory that exists; one that does not
// (yet) is kept as given, and nothing can be resolved into it until it does.
func resolveDir(dir string) string {
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return filepath.Clean(dir)
}

// Dirs returns the effective roots.
func (r *MediaRoots) Dirs() []string {
	if r == nil {
		return nil
	}
	return r.dirs
}

// Outbox returns the resolved outbox directory, "" when there is none.
func (r *MediaRoots) Outbox() string {
	if r == nil {
		return ""
	}
	return r.outbox
}

// Resolve returns the real path of a file that may be sent, or why not. The
// file is then read from the returned path, so a symlink swapped in after
// the check is not followed out of the root. A refusal by the roots matches
// ErrOutsideMediaRoots.
func (r *MediaRoots) Resolve(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("media path must be absolute: %s", path)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("media file not found: %s", path)
		}
		// The capitalised text is the API response.
		return "", fmt.Errorf("Error reading media file: %w", err)
	}
	if r != nil {
		for _, dir := range r.dirs {
			rel, err := filepath.Rel(dir, resolved)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			// Hidden files and directories below a root (a .env, a .git
			// checkout in the outbox) hold configuration, not media.
			if hasHiddenPart(rel) {
				return "", &refusal{fmt.Sprintf("refusing to send %s: hidden files and directories cannot be sent", path)}
			}
			return resolved, nil
		}
	}
	return "", &refusal{fmt.Sprintf(
		"refusing to send %s: it is outside the media roots (%s). Copy it into one of them, or add its directory to WHATSAPP_MEDIA_ROOTS",
		path, strings.Join(r.Dirs(), ":"))}
}

func hasHiddenPart(rel string) bool {
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if strings.HasPrefix(part, ".") {
			return true
		}
	}
	return false
}

// Publish records the roots in messages.db, where the MCP server checks a
// path before converting it and finds the outbox to write conversions to.
func (r *MediaRoots) Publish(ctx context.Context, w MetaWriter) error {
	dirs, err := json.Marshal(r.Dirs())
	if err != nil {
		return err
	}
	if err := w.SetMeta(ctx, "media_roots", string(dirs)); err != nil {
		return err
	}
	return w.SetMeta(ctx, "media_outbox", r.Outbox())
}
