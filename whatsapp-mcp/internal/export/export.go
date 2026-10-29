package export

// Export a chat's history to a file: plain text, JSON or HTML, optionally
// with its media in a ZIP archive.
//
//	whatsapp-mcp export <chat JID> [--format txt|json|html] [--after ISO] [--before ISO]
//	                       [--media] [--out PATH] [--account NAME]
//
// The export_chat tool does the same. Exports go to store/exports/ (mode
// 0600) unless a path is given; the read allowlist applies.

import (
	"archive/zip"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/account"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/render"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
)

var exportFormats = []string{"txt", "json", "html"}

func exportLine(m *store.Message, mediaName string) string {
	ts := "?"
	if m.Timestamp != nil {
		ts = m.Timestamp.Display()
	}
	text := m.Content
	if m.MediaType != "" {
		attached := fmt.Sprintf("<%s omitted>", m.MediaType)
		if mediaName != "" {
			attached = fmt.Sprintf("<%s: %s>", m.MediaType, mediaName)
		}
		text = strings.TrimSpace(attached + " " + text)
	}
	if m.DeletedAt != nil {
		text += " (deleted by sender)"
	} else if m.EditedAt != nil {
		text += " (edited)"
	}
	if len(m.Reactions) > 0 {
		parts := make([]string, len(m.Reactions))
		for i, r := range m.Reactions {
			who := store.Deref(r.SenderName)
			if who == "" {
				who = store.UserPart(r.SenderJID)
			}
			parts[i] = r.Emoji + " " + who
		}
		text += " [" + strings.Join(parts, ", ") + "]"
	}
	return fmt.Sprintf("[%s] %s: %s", ts, store.Deref(m.SenderName), text)
}

func isoOrNil(t *pyfmt.Time) any {
	if t == nil {
		return nil
	}
	return t.ISOFormat("T")
}

func votesMap(v *store.Votes) any {
	if v == nil {
		return nil
	}
	m := pyfmt.NewOMap()
	for _, k := range v.Keys {
		m.Set(k, v.Who[k])
	}
	return m
}

// exportDict is every field of the message, as dataclasses.asdict gives it.
func exportDict(m *store.Message, mediaName string) *pyfmt.OMap {
	reactions := []any{}
	for _, r := range m.Reactions {
		reactions = append(reactions, pyfmt.NewOMap("emoji", r.Emoji, "sender_jid", r.SenderJID, "sender_name", pyfmt.Ptr(r.SenderName)))
	}
	mentions := m.Mentions
	if mentions == nil {
		mentions = []any{}
	}
	var mediaType, transcript any
	if m.MediaType != "" {
		mediaType = m.MediaType
	}
	if m.Transcript != "" {
		transcript = m.Transcript
	}
	d := pyfmt.NewOMap("timestamp", isoOrNil(m.Timestamp), "sender", m.Sender, "content", m.Content, "is_from_me", m.IsFromMe,
		"chat_jid", m.ChatJID, "id", m.ID, "chat_name", pyfmt.Ptr(m.ChatName), "media_type", mediaType,
		"sender_jid", pyfmt.Ptr(m.SenderJID), "sender_name", pyfmt.Ptr(m.SenderName), "filename", pyfmt.Ptr(m.Filename),
		"quoted_id", pyfmt.Ptr(m.QuotedID), "quoted_sender", pyfmt.Ptr(m.QuotedSender), "quoted_text", pyfmt.Ptr(m.QuotedText),
		"mentions", mentions, "edited_at", isoOrNil(m.EditedAt), "deleted_at", isoOrNil(m.DeletedAt),
		"reactions", reactions, "media_width", pyfmt.Ptr(m.MediaWidth), "media_height", pyfmt.Ptr(m.MediaHeight),
		"media_seconds", pyfmt.Ptr(m.MediaSeconds), "media_detail", pyfmt.Ptr(m.MediaDetail), "starred", m.Starred,
		"transcript", transcript, "poll_votes", votesMap(m.PollVotes), "event_responses", votesMap(m.EventResponses))
	if mediaName != "" {
		d.Set("media_file", mediaName)
	}
	return d
}

// htmlEscape is Python's html.escape (quotes included).
var htmlEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;").Replace

func exportHTML(chatName string, messages []*store.Message, mediaFiles map[string]string) string {
	rows := make([]string, len(messages))
	for i, m := range messages {
		name := mediaFiles[m.ID]
		body := strings.ReplaceAll(htmlEscape(m.Content), "\n", "<br>")
		switch {
		case name != "" && (m.MediaType == "image" || m.MediaType == "sticker"):
			body = fmt.Sprintf(`<img src="%s" alt=""><br>`, htmlEscape(name)) + body
		case name != "":
			body = fmt.Sprintf(`<a href="%s">%s: %s</a><br>`, htmlEscape(name), htmlEscape(m.MediaType), htmlEscape(name)) + body
		case m.MediaType != "":
			body = fmt.Sprintf("<em>%s omitted</em><br>", htmlEscape(m.MediaType)) + body
		}
		cls := "them"
		if m.IsFromMe {
			cls = "me"
		}
		ts := ""
		if m.Timestamp != nil {
			ts = m.Timestamp.Display()
		}
		rows[i] = fmt.Sprintf(`<div class="msg %s"><div class="meta">%s · %s</div>%s</div>`, cls, htmlEscape(store.Deref(m.SenderName)), ts, body)
	}
	title := htmlEscape(chatName)
	return `<!doctype html><html><head><meta charset="utf-8"><title>` + title + `</title>
<style>body{font-family:sans-serif;max-width:760px;margin:auto;background:#efeae2}
.msg{background:#fff;border-radius:8px;padding:6px 10px;margin:6px 0;max-width:80%}
.me{background:#d9fdd3;margin-left:auto}.meta{font-size:12px;color:#667}img{max-width:100%}</style>
</head><body><h1>` + title + `</h1>
` + strings.Join(rows, "\n") + `
</body></html>`
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) //nolint:gosec // G304: a media file the bridge downloaded for this export
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: the export file or a copy in our work dir
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// Download fetches a message's media through the bridge, as download_media
// does: {success, path, ...} or a refusal.
type Download func(ctx context.Context, messageID, chatJID string) *pyfmt.OMap

// unsafeName matches what may not appear in an export's file name.
var unsafeName = regexp.MustCompile(`[^A-Za-z0-9_.@-]`)

// Chat writes the chat to a file and returns {success, path, messages, media}.
func Chat(ctx context.Context, d *store.DB, download Download, chatJID, format, after, before string, includeMedia bool, out string) *pyfmt.OMap {
	if !slices.Contains(exportFormats, format) {
		return render.Refused("format must be one of " + strings.Join(exportFormats, ", "))
	}
	messages, err := d.ExportMessages(ctx, chatJID, after, before)
	if err != nil {
		return render.Refused(err.Error())
	}
	if len(messages) == 0 {
		return render.Refused(fmt.Sprintf("No messages stored for %s in that range", chatJID))
	}
	chatName := chatJID
	if chat := d.GetChat(ctx, chatJID, false); chat != nil && store.Deref(chat.Name) != "" {
		chatName = *chat.Name
	}

	work, err := os.MkdirTemp("", "whatsapp-export-")
	if err != nil {
		return render.Refused(err.Error())
	}
	defer os.RemoveAll(work)
	mediaFiles := map[string]string{}
	failed := []any{}
	if includeMedia {
		if mediaFiles, failed, err = downloadMedia(ctx, download, messages, chatJID, work); err != nil {
			return render.Refused(err.Error())
		}
	}

	stem := []rune(unsafeName.ReplaceAllString(chatName, "_"))
	if len(stem) > 60 {
		stem = stem[:60]
	}
	stemText := string(stem)
	if stemText == "" {
		stemText = "chat"
	}
	bodyName := stemText + "." + format
	body := formatBody(format, chatJID, chatName, messages, mediaFiles)
	if err := os.WriteFile(filepath.Join(work, bodyName), []byte(body), 0o600); err != nil {
		return render.Refused(err.Error())
	}

	if out == "" {
		dir := filepath.Join(d.Dir(), "exports")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return render.Refused(err.Error())
		}
		ext := format
		if includeMedia {
			ext = "zip"
		}
		out = filepath.Join(dir, fmt.Sprintf("%s_%s.%s", stemText, time.Now().Format("20060102_150405"), ext))
	}
	out = pyfmt.AbsPath(out)
	// Both create the file 0600; the Chmod covers a file that existed
	// already, which keeps its mode when truncated.
	if includeMedia {
		err = writeZip(out, work, bodyName, mediaFiles)
	} else {
		err = copyFile(filepath.Join(work, bodyName), out)
	}
	if err == nil {
		err = os.Chmod(out, 0o600)
	}
	if err != nil {
		return render.Refused(err.Error())
	}
	result := pyfmt.NewOMap("success", true, "path", out, "messages", len(messages), "media", len(mediaFiles))
	if len(failed) > 0 {
		result.Set("media_failed", failed)
	}
	return result
}

// downloadMedia downloads the media of messages into work: the file name
// of each message's media there, and the ids of those that failed.
func downloadMedia(ctx context.Context, download Download, messages []*store.Message, chatJID, work string) (map[string]string, []any, error) {
	files := map[string]string{}
	failed := []any{}
	for _, m := range messages {
		if m.MediaType == "" || m.DeletedAt != nil {
			continue
		}
		result := download(ctx, m.ID, chatJID)
		if !pyfmt.Truthy(result.Get("success")) {
			failed = append(failed, m.ID)
			continue
		}
		name := m.ID + "_" + filepath.Base(pyfmt.AsString(result.Get("path")))
		if err := copyFile(pyfmt.AsString(result.Get("path")), filepath.Join(work, name)); err != nil {
			return nil, nil, err
		}
		files[m.ID] = name
	}
	return files, failed, nil
}

// formatBody is the export file in format: txt, json or html.
func formatBody(format, chatJID, chatName string, messages []*store.Message, mediaFiles map[string]string) string {
	switch format {
	case "txt":
		lines := make([]string, len(messages))
		for i, m := range messages {
			lines[i] = exportLine(m, mediaFiles[m.ID])
		}
		return fmt.Sprintf("%s (%s)\n\n", chatName, chatJID) + strings.Join(lines, "\n") + "\n"
	case "json":
		list := make([]any, len(messages))
		for i, m := range messages {
			list[i] = exportDict(m, mediaFiles[m.ID])
		}
		now := pyfmt.NewTime(time.Now(), true)
		return pyfmt.Indent(pyfmt.NewOMap("chat_jid", chatJID, "chat_name", chatName, "exported_at", now.ISOFormat("T"), "messages", list), " ")
	}
	return exportHTML(chatName, messages, mediaFiles)
}

func writeZip(out, work, bodyName string, mediaFiles map[string]string) error {
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) //nolint:gosec // G304: the export file the caller chose
	if err != nil {
		return err
	}
	z := zip.NewWriter(f)
	names := slices.Sorted(maps.Values(mediaFiles))
	for _, name := range append([]string{bodyName}, names...) {
		w, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			f.Close()
			return err
		}
		src, err := os.Open(filepath.Join(work, name)) //nolint:gosec // G304: a file of our own work dir
		if err != nil {
			f.Close()
			return err
		}
		_, err = io.Copy(w, src)
		src.Close()
		if err != nil {
			f.Close()
			return err
		}
	}
	if err := z.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// Main is the `export` subcommand: args are what follows "export". It
// prints the result on stdout and returns the exit code.
//
// Database errors are logged to log.
func Main(ctx context.Context, cfg *config.Config, log *slog.Logger, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	fs.SetOutput(stderr)
	format := fs.String("format", "txt", "txt, json or html")
	after := fs.String("after", "", "only messages after this ISO-8601 time")
	before := fs.String("before", "", "only messages before this ISO-8601 time")
	withMedia := fs.Bool("media", false, "download media and write a ZIP archive")
	out := fs.String("out", "", "output path (default store/exports/...)")
	accountName := fs.String("account", "", "account name (see WHATSAPP_ACCOUNTS)")
	// Flags may come before or after the chat JID.
	var positional []string
	for len(args) > 0 {
		if err := fs.Parse(args); err != nil {
			return 2
		}
		args = fs.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	if len(positional) != 1 {
		fmt.Fprintln(stderr, "usage: whatsapp-mcp export <chat JID> [--format txt|json|html] [--after ISO] [--before ISO] [--media] [--out PATH] [--account NAME]")
		return 2
	}
	registry := &account.Registry{StoreDir: cfg.StoreDir, APIURL: cfg.APIBaseURL, BridgeToken: cfg.BridgeToken,
		Accounts: cfg.Accounts, AccountsFile: cfg.AccountsFile, Default: cfg.DefaultAccount}
	a, err := registry.Resolve(*accountName)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	pool := store.NewPool(log)
	defer pool.Close()
	d := store.NewDB(pool, a.StoreDir, store.Scope{List: cfg.ReadAllowlist, File: cfg.ReadAllowlistFile})
	bridge := &bridgeapi.Client{BaseURL: a.APIURL, Token: a.Token, HTTP: bridgeapi.NewHTTPClient()}
	result := Chat(ctx, d, func(ctx context.Context, messageID, chatJID string) *pyfmt.OMap {
		if hidden := d.CheckReadable(ctx, chatJID); hidden != "" {
			return render.Refused(hidden)
		}
		return bridge.Download(ctx, messageID, chatJID)
	}, positional[0], *format, *after, *before, *withMedia, *out)
	fmt.Fprintln(stdout, pyfmt.Compact(result))
	if pyfmt.Truthy(result.Get("success")) {
		return 0
	}
	return 1
}
