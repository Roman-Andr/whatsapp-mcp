package config

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// DefaultAddr is where the REST API listens unless WHATSAPP_BRIDGE_ADDR says
// otherwise: localhost only.
const DefaultAddr = "127.0.0.1:8080"

// MaxDeviceNameLen keeps the device label readable in the phone's device
// list.
const MaxDeviceNameLen = 50

// Config is every setting of the bridge. Field comments name the variable.
type Config struct {
	Addr string // WHATSAPP_BRIDGE_ADDR, DefaultAddr when unset
	// Token is WHATSAPP_BRIDGE_TOKEN as given; Secret resolves it against
	// store/api_token.
	Token string

	PairPhone   string // WHATSAPP_PAIR_PHONE, without spaces and "+"
	DeviceName  string // WHATSAPP_DEVICE_NAME, trimmed
	HistoryDays int    // WHATSAPP_HISTORY_DAYS, 0 for whatsmeow's default

	AutoDownload AutoDownload
	Presence     bool // WHATSAPP_PRESENCE
	RejectCalls  RejectCalls
	// Welcomes maps a group JID to the text sent to newcomers
	// (WHATSAPP_WELCOME_FILE); nil without the file.
	Welcomes map[string]string

	// SendAllowlist and ReadAllowlist are the raw entries of
	// WHATSAPP_SEND_ALLOWLIST / _FILE and WHATSAPP_READ_ALLOWLIST / _FILE,
	// already checked to parse; policy.NewAllowlist builds the lists.
	SendAllowlist []string
	ReadAllowlist []string
	ReadOnly      bool // WHATSAPP_READ_ONLY

	SendRate  int // WHATSAPP_SEND_RATE, per minute; 0 turns the limit off
	SendBurst int // WHATSAPP_SEND_BURST

	// MediaRoots are the entries of WHATSAPP_MEDIA_ROOTS, nil for the
	// defaults; MediaOutbox is $XDG_DATA_HOME/whatsapp-mcp/outbox, shared by
	// every account's bridge. policy.NewMediaRoots builds the roots.
	MediaRoots  []string
	MediaOutbox string

	Webhook Webhook
}

// AutoDownload decides which attachments are saved as soon as they arrive.
type AutoDownload struct {
	Enabled       bool   // WHATSAPP_AUTO_DOWNLOAD: on unless 0/false/no/off
	MaxVideoBytes uint64 // WHATSAPP_AUTO_DOWNLOAD_MAX_VIDEO_MB, default 25 MB
	MaxFileBytes  uint64 // WHATSAPP_AUTO_DOWNLOAD_MAX_MB, default 100 MB
}

// RejectCalls is WHATSAPP_REJECT_CALLS: decline incoming 1:1 calls, and with
// WHATSAPP_REJECT_CALLS_MESSAGE (trimmed) tell the caller why.
type RejectCalls struct {
	Enabled bool
	Message string
}

// Webhook is where events are POSTed. URL is empty when none is set.
type Webhook struct {
	URL string // WHATSAPP_WEBHOOK_URL, an http:// or https:// URL
	// Secret is WHATSAPP_WEBHOOK_SECRET as given; Secret (the function)
	// resolves it against store/webhook_secret.
	Secret string
	// Disabled is WHATSAPP_WEBHOOK_DISABLED with a URL set: the URL is then
	// neither checked nor used.
	Disabled bool
}

// Load reads the configuration through getenv (os.Getenv in production).
func Load(getenv func(string) string) (*Config, error) {
	l := loader{getenv: getenv}
	c := &Config{
		Addr:      getenv("WHATSAPP_BRIDGE_ADDR"),
		Token:     getenv("WHATSAPP_BRIDGE_TOKEN"),
		PairPhone: strings.TrimPrefix(strings.TrimSpace(getenv("WHATSAPP_PAIR_PHONE")), "+"),
		ReadOnly:  l.bool("WHATSAPP_READ_ONLY"),
		Presence:  l.bool("WHATSAPP_PRESENCE"),
		RejectCalls: RejectCalls{
			Enabled: l.bool("WHATSAPP_REJECT_CALLS"),
		},
	}
	if c.Addr == "" {
		c.Addr = DefaultAddr
	}
	if c.RejectCalls.Enabled {
		c.RejectCalls.Message = strings.TrimSpace(getenv("WHATSAPP_REJECT_CALLS_MESSAGE"))
	}
	// The order is the order the bridge has always checked them in, so the
	// first error stays the same.
	steps := []func(*Config) error{
		l.history, l.deviceName, l.sendAllowlist, l.readAllowlist, l.rateLimit,
		l.welcomes, l.autoDownload, l.mediaRoots, l.webhook,
	}
	for _, step := range steps {
		if err := step(c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

type loader struct {
	getenv func(string) string
}

// bool reads a yes/no variable: 1/true/yes/on, anything else is false.
func (l loader) bool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(l.getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// int reads a non-negative whole number, def when unset.
func (l loader) int(name string, def int) (int, error) {
	value := strings.TrimSpace(l.getenv(name))
	if value == "" {
		return def, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative whole number, got %q", name, value)
	}
	return n, nil
}

// history is WHATSAPP_HISTORY_DAYS: how many days of history the phone sends
// at pairing.
func (l loader) history(c *Config) error {
	days, err := l.int("WHATSAPP_HISTORY_DAYS", 0)
	c.HistoryDays = days
	return err
}

// deviceName is WHATSAPP_DEVICE_NAME, the name shown under Linked devices.
func (l loader) deviceName(c *Config) error {
	name := strings.TrimSpace(l.getenv("WHATSAPP_DEVICE_NAME"))
	if utf8.RuneCountInString(name) > MaxDeviceNameLen {
		return fmt.Errorf("WHATSAPP_DEVICE_NAME is longer than %d characters", MaxDeviceNameLen)
	}
	c.DeviceName = name
	return nil
}

// sendAllowlist reads WHATSAPP_SEND_ALLOWLIST (JIDs or phone numbers,
// separated by commas or whitespace) and WHATSAPP_SEND_ALLOWLIST_FILE (one
// per line, "#" starts a comment).
func (l loader) sendAllowlist(c *Config) (err error) {
	c.SendAllowlist, err = l.allowlist("WHATSAPP_SEND_ALLOWLIST", "send allowlist")
	return err
}

// readAllowlist reads WHATSAPP_READ_ALLOWLIST and
// WHATSAPP_READ_ALLOWLIST_FILE, in the same format.
func (l loader) readAllowlist(c *Config) (err error) {
	c.ReadAllowlist, err = l.allowlist("WHATSAPP_READ_ALLOWLIST", "read allowlist")
	return err
}

// allowlist reads the list from env and the file named by env_FILE. An
// entry that cannot be parsed is an error: a typo must not silently turn the
// restriction off.
func (l loader) allowlist(env, label string) ([]string, error) {
	var entries []string
	entries = append(entries, strings.FieldsFunc(l.getenv(env), func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})...)
	if path := l.getenv(env + "_FILE"); path != "" {
		lines, err := readListFile(path)
		if err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", label, err)
		}
		entries = append(entries, lines...)
	}
	if _, err := policy.NewAllowlist(entries); err != nil {
		return nil, fmt.Errorf("invalid %s entry: %w", label, err)
	}
	return entries, nil
}

// readListFile returns the non-empty lines of path with "#" comments cut.
func readListFile(path string) ([]string, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the operator's own setting
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line, _, _ := strings.Cut(scanner.Text(), "#")
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

// rateLimit reads WHATSAPP_SEND_RATE (per minute, default 10, 0 turns the
// limit off) and WHATSAPP_SEND_BURST (default 3).
func (l loader) rateLimit(c *Config) error {
	var err error
	if c.SendRate, err = l.int("WHATSAPP_SEND_RATE", 10); err != nil {
		return err
	}
	c.SendBurst, err = l.int("WHATSAPP_SEND_BURST", 3)
	return err
}

// welcomes reads WHATSAPP_WELCOME_FILE: a JSON object mapping a group JID to
// the text sent when someone joins it. "{name}" becomes the newcomer's name
// and "{mention}" an @mention of them.
func (l loader) welcomes(c *Config) error {
	path := l.getenv("WHATSAPP_WELCOME_FILE")
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // the path is the operator's own setting
	if err != nil {
		return fmt.Errorf("failed to read WHATSAPP_WELCOME_FILE: %w", err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("WHATSAPP_WELCOME_FILE is not a JSON object of group JID to text: %w", err)
	}
	out := make(map[string]string, len(raw))
	for chat, text := range raw {
		group, err := jid.ParseGroup(chat)
		if err != nil {
			return fmt.Errorf("WHATSAPP_WELCOME_FILE: %w", err)
		}
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("WHATSAPP_WELCOME_FILE: empty text for %s", chat)
		}
		out[group.String()] = text
	}
	c.Welcomes = out
	return nil
}

// autoDownload reads WHATSAPP_AUTO_DOWNLOAD (on unless 0/false/no/off; any
// other value, valid or not, leaves it on), WHATSAPP_AUTO_DOWNLOAD_MAX_VIDEO_MB
// (default 25) and WHATSAPP_AUTO_DOWNLOAD_MAX_MB (default 100).
func (l loader) autoDownload(c *Config) error {
	c.AutoDownload = AutoDownload{Enabled: true, MaxVideoBytes: 25 << 20, MaxFileBytes: 100 << 20}
	switch strings.ToLower(strings.TrimSpace(l.getenv("WHATSAPP_AUTO_DOWNLOAD"))) {
	case "0", "false", "no", "off":
		c.AutoDownload.Enabled = false
	}
	for _, v := range []struct {
		name string
		dst  *uint64
	}{
		{"WHATSAPP_AUTO_DOWNLOAD_MAX_VIDEO_MB", &c.AutoDownload.MaxVideoBytes},
		{"WHATSAPP_AUTO_DOWNLOAD_MAX_MB", &c.AutoDownload.MaxFileBytes},
	} {
		raw := strings.TrimSpace(l.getenv(v.name))
		if raw == "" {
			continue
		}
		mb, err := strconv.ParseFloat(raw, 64)
		if err != nil || mb < 0 {
			return fmt.Errorf("invalid %s=%q: want a number of megabytes", v.name, raw)
		}
		*v.dst = uint64(mb * (1 << 20))
	}
	return nil
}

// mediaRoots reads WHATSAPP_MEDIA_ROOTS, a list of directories separated by
// ":" (like PATH), and locates the outbox. The entries are only checked
// here; policy.NewMediaRoots creates the outbox and resolves them.
func (l loader) mediaRoots(c *Config) error {
	outbox, err := l.outbox()
	if err != nil {
		return fmt.Errorf("cannot locate the media outbox: %w", err)
	}
	c.MediaOutbox = outbox
	raw := strings.TrimSpace(l.getenv("WHATSAPP_MEDIA_ROOTS"))
	if raw == "" {
		return nil
	}
	var entries []string
	for _, e := range filepath.SplitList(raw) {
		if e = strings.TrimSpace(e); e != "" {
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return errors.New("WHATSAPP_MEDIA_ROOTS lists no directory")
	}
	if _, err := policy.ResolveMediaRoots(entries); err != nil {
		return err
	}
	c.MediaRoots = entries
	return nil
}

// outbox is $XDG_DATA_HOME/whatsapp-mcp/outbox, shared by every account's
// bridge.
func (l loader) outbox() (string, error) {
	data := l.getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "whatsapp-mcp", "outbox"), nil
}

// webhook reads WHATSAPP_WEBHOOK_URL, WHATSAPP_WEBHOOK_DISABLED and
// WHATSAPP_WEBHOOK_SECRET.
func (l loader) webhook(c *Config) error {
	raw := l.getenv("WHATSAPP_WEBHOOK_URL")
	if raw == "" {
		return nil
	}
	if l.bool("WHATSAPP_WEBHOOK_DISABLED") {
		c.Webhook = Webhook{URL: raw, Disabled: true}
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("WHATSAPP_WEBHOOK_URL must be an http:// or https:// URL, got %q", raw)
	}
	c.Webhook = Webhook{URL: raw, Secret: l.getenv("WHATSAPP_WEBHOOK_SECRET")}
	return nil
}
