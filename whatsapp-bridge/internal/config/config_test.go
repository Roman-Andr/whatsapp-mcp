package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// env is a fake environment. HOME-less tests set XDG_DATA_HOME so the
// media outbox can be located.
func env(vars map[string]string) func(string) string {
	return func(name string) string {
		if name == "XDG_DATA_HOME" {
			if v, ok := vars[name]; ok {
				return v
			}
			return "/data"
		}
		return vars[name]
	}
}

func mustLoad(t *testing.T, vars map[string]string) *Config {
	t.Helper()
	c, err := Load(env(vars))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func writeFile(t *testing.T, name, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()
	c := mustLoad(t, nil)
	if c.Addr != DefaultAddr || c.SendRate != 10 || c.SendBurst != 3 || c.HistoryDays != 0 {
		t.Errorf("defaults %+v", c)
	}
	if !c.AutoDownload.Enabled || c.AutoDownload.MaxVideoBytes != 25<<20 || c.AutoDownload.MaxFileBytes != 100<<20 {
		t.Errorf("auto-download defaults %+v", c.AutoDownload)
	}
	if c.MediaRoots != nil || c.MediaOutbox != "/data/whatsapp-mcp/outbox" {
		t.Errorf("media roots %q outbox %q", c.MediaRoots, c.MediaOutbox)
	}
	if c.Webhook != (Webhook{}) || c.RejectCalls != (RejectCalls{}) || c.Welcomes != nil {
		t.Errorf("optional features on by default: %+v", c)
	}
}

// Three dialects, kept as they always were: envBool's 1/true/yes/on,
// auto-download's "on unless 0/false/no/off", and false for anything else.
func TestBoolDialects(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value              string
		readOnly, download bool
	}{
		{"", false, true},
		{"1", true, true},
		{" YES ", true, true},
		{"on", true, true},
		{"0", false, false},
		{"off", false, false},
		{"enabled", false, true},
		{"y", false, true},
	}
	for _, c := range cases {
		t.Run(c.value, func(t *testing.T) {
			t.Parallel()
			got := mustLoad(t, map[string]string{"WHATSAPP_READ_ONLY": c.value, "WHATSAPP_AUTO_DOWNLOAD": c.value})
			if got.ReadOnly != c.readOnly || got.AutoDownload.Enabled != c.download {
				t.Errorf("read-only %v, auto-download %v", got.ReadOnly, got.AutoDownload.Enabled)
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing")
	cases := []struct {
		name string
		vars map[string]string
		want string
	}{
		{"rate not a number", map[string]string{"WHATSAPP_SEND_RATE": "lots"}, `WHATSAPP_SEND_RATE must be a non-negative whole number, got "lots"`},
		{"negative burst", map[string]string{"WHATSAPP_SEND_BURST": "-1"}, `WHATSAPP_SEND_BURST must be a non-negative whole number, got "-1"`},
		{"history days", map[string]string{"WHATSAPP_HISTORY_DAYS": "forever"}, `WHATSAPP_HISTORY_DAYS must be a non-negative whole number, got "forever"`},
		{"device name too long", map[string]string{"WHATSAPP_DEVICE_NAME": strings.Repeat("x", MaxDeviceNameLen+1)}, "WHATSAPP_DEVICE_NAME is longer than 50 characters"},
		{"allowlist entry", map[string]string{"WHATSAPP_SEND_ALLOWLIST": "John"}, `invalid send allowlist entry: "John": recipient "John" is neither a JID nor a phone number`},
		{"allowlist file", map[string]string{"WHATSAPP_READ_ALLOWLIST_FILE": missing}, "failed to read read allowlist: open " + missing + ": no such file or directory"},
		{"auto-download size", map[string]string{"WHATSAPP_AUTO_DOWNLOAD_MAX_MB": "lots"}, `invalid WHATSAPP_AUTO_DOWNLOAD_MAX_MB="lots": want a number of megabytes`},
		{"media roots empty", map[string]string{"WHATSAPP_MEDIA_ROOTS": " : "}, "WHATSAPP_MEDIA_ROOTS lists no directory"},
		{"media root /", map[string]string{"WHATSAPP_MEDIA_ROOTS": "/"}, "WHATSAPP_MEDIA_ROOTS: / would allow sending every file"},
		{"webhook scheme", map[string]string{"WHATSAPP_WEBHOOK_URL": "file:///etc/passwd"}, `WHATSAPP_WEBHOOK_URL must be an http:// or https:// URL, got "file:///etc/passwd"`},
		// The first failing setting wins, in the order the bridge always checked them.
		{"history before allowlist", map[string]string{"WHATSAPP_HISTORY_DAYS": "x", "WHATSAPP_SEND_ALLOWLIST": "John"}, `WHATSAPP_HISTORY_DAYS must be a non-negative whole number, got "x"`},
		{"allowlist before rate", map[string]string{"WHATSAPP_SEND_RATE": "x", "WHATSAPP_SEND_ALLOWLIST": "John"}, "invalid send allowlist entry"},
		{"media roots before webhook", map[string]string{"WHATSAPP_MEDIA_ROOTS": "/", "WHATSAPP_WEBHOOK_URL": "ftp://x"}, "WHATSAPP_MEDIA_ROOTS: /"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := Load(env(c.vars))
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}

func TestAllowlistsFromEnvAndFile(t *testing.T) {
	t.Parallel()
	path := writeFile(t, "allow.txt", "# owner\n79966764464  # message yourself\n\n120363431597928274@g.us\n")
	c := mustLoad(t, map[string]string{
		"WHATSAPP_SEND_ALLOWLIST":      "111, 222;333\t444",
		"WHATSAPP_SEND_ALLOWLIST_FILE": path,
		"WHATSAPP_READ_ALLOWLIST":      "555",
	})
	want := "111,222,333,444,79966764464,120363431597928274@g.us"
	if got := strings.Join(c.SendAllowlist, ","); got != want {
		t.Errorf("send entries %s, want %s", got, want)
	}
	if got := strings.Join(c.ReadAllowlist, ","); got != "555" {
		t.Errorf("read entries %s", got)
	}
}

func TestWelcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, data, want string
	}{
		{"group", `{"120@g.us": "Welcome {mention}! Events are on Tuesdays."}`, ""},
		{"direct chat", `{"79966764464@s.whatsapp.net": "hi"}`, `WHATSAPP_WELCOME_FILE: "79966764464@s.whatsapp.net" is not a group JID (…@g.us)`},
		{"empty text", `{"120@g.us": " "}`, "WHATSAPP_WELCOME_FILE: empty text for 120@g.us"},
		{"not an object", `[]`, "WHATSAPP_WELCOME_FILE is not a JSON object of group JID to text: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := Load(env(map[string]string{"WHATSAPP_WELCOME_FILE": writeFile(t, "welcome.json", c.data)}))
			if c.want == "" {
				if err != nil || got.Welcomes["120@g.us"] == "" {
					t.Errorf("%v %v", got, err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), c.want) {
				t.Errorf("error %v, want %q", err, c.want)
			}
		})
	}
}

func TestAutoDownloadLimits(t *testing.T) {
	t.Parallel()
	c := mustLoad(t, map[string]string{"WHATSAPP_AUTO_DOWNLOAD_MAX_VIDEO_MB": "1.5", "WHATSAPP_AUTO_DOWNLOAD_MAX_MB": "0"})
	if c.AutoDownload.MaxVideoBytes != 1572864 || c.AutoDownload.MaxFileBytes != 0 {
		t.Errorf("%+v", c.AutoDownload)
	}
}

func TestMediaRootsAndOutbox(t *testing.T) {
	t.Parallel()
	c := mustLoad(t, map[string]string{"WHATSAPP_MEDIA_ROOTS": "/srv/a: /srv/b ::", "XDG_DATA_HOME": "/xdg"})
	if strings.Join(c.MediaRoots, ",") != "/srv/a,/srv/b" || c.MediaOutbox != "/xdg/whatsapp-mcp/outbox" {
		t.Errorf("roots %q outbox %q", c.MediaRoots, c.MediaOutbox)
	}
}

func TestWebhook(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		vars map[string]string
		want Webhook
	}{
		{"no URL", map[string]string{"WHATSAPP_WEBHOOK_SECRET": "unused"}, Webhook{}},
		{"URL and secret", map[string]string{"WHATSAPP_WEBHOOK_URL": "http://127.0.0.1:9/hook", "WHATSAPP_WEBHOOK_SECRET": " s "}, Webhook{URL: "http://127.0.0.1:9/hook", Secret: " s "}},
		{"disabled skips the URL check", map[string]string{"WHATSAPP_WEBHOOK_URL": "ftp://x", "WHATSAPP_WEBHOOK_DISABLED": "1"}, Webhook{URL: "ftp://x", Disabled: true}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := mustLoad(t, c.vars).Webhook; got != c.want {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestPairingAndCalls(t *testing.T) {
	t.Parallel()
	c := mustLoad(t, map[string]string{
		"WHATSAPP_PAIR_PHONE":           " +79990000001 ",
		"WHATSAPP_DEVICE_NAME":          "  Snow ops bridge ",
		"WHATSAPP_HISTORY_DAYS":         "3650",
		"WHATSAPP_REJECT_CALLS":         "1",
		"WHATSAPP_REJECT_CALLS_MESSAGE": " I can't take calls, please write. ",
		"WHATSAPP_BRIDGE_ADDR":          "127.0.0.1:8081",
	})
	if c.PairPhone != "79990000001" || c.DeviceName != "Snow ops bridge" || c.HistoryDays != 3650 || c.Addr != "127.0.0.1:8081" {
		t.Errorf("%+v", c)
	}
	if c.RejectCalls != (RejectCalls{Enabled: true, Message: "I can't take calls, please write."}) {
		t.Errorf("reject calls %+v", c.RejectCalls)
	}
	t.Run("message ignored without rejecting", func(t *testing.T) {
		t.Parallel()
		if got := mustLoad(t, map[string]string{"WHATSAPP_REJECT_CALLS_MESSAGE": "x"}).RejectCalls; got != (RejectCalls{}) {
			t.Errorf("%+v", got)
		}
	})
}
