package mediaconv

// Media roots: the bridge sends files only from the directories in its
// WHATSAPP_MEDIA_ROOTS and publishes them, with its outbox, in bridge_meta.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/store"
)

// MediaRoots are the directories the bridge sends files from; nil for an
// older bridge that publishes none. An unreadable list allows nothing.
func MediaRoots(ctx context.Context, d *store.DB) []string {
	raw := d.Meta(ctx, "media_roots")
	if raw == "" {
		return nil
	}
	var list []any
	if json.Unmarshal([]byte(raw), &list) != nil {
		return []string{}
	}
	roots := make([]string, len(list))
	for i, d := range list {
		roots[i] = pyfmt.Str(d)
	}
	return roots
}

// CheckSendable says why the bridge would refuse to send path, "" if it
// would not.
//
// The bridge has the final say; this check runs before a file is
// converted, because a sticker or voice message made from a file outside
// the roots lands in the outbox and would pass the bridge's check.
func CheckSendable(ctx context.Context, d *store.DB, path string) string {
	roots := MediaRoots(ctx, d)
	if roots == nil {
		return ""
	}
	resolvedPath := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		resolvedPath = resolved
	}
	resolvedPath = pyfmt.AbsPath(resolvedPath)
	for _, root := range roots {
		rel, err := filepath.Rel(pyfmt.AbsPath(root), resolvedPath)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
			if strings.HasPrefix(part, ".") {
				return fmt.Sprintf("refusing to send %s: hidden files and directories cannot be sent", path)
			}
		}
		return ""
	}
	return fmt.Sprintf("refusing to send %s: it is outside the media roots (%s). "+
		"Copy it into one of them, or add its directory to WHATSAPP_MEDIA_ROOTS", path, strings.Join(roots, string(os.PathListSeparator)))
}

// Outbox is where converted files go: the bridge's outbox, which is always
// a media root, or "" (the temp directory) for an older bridge.
func Outbox(ctx context.Context, d *store.DB) string {
	dir := d.Meta(ctx, "media_outbox")
	if st, err := os.Stat(dir); dir != "" && err == nil && st.IsDir() {
		return dir
	}
	return ""
}
