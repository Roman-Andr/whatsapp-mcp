package account

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The accounts file is read on every call: an edit takes effect without a
// restart.
func TestAccountsFileIsReadOnEveryCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "accounts.json")
	write(t, file, `{"work": {"store_dir": "/w", "api_url": "http://w/api/"}}`)
	r := &Registry{StoreDir: dir, AccountsFile: file}
	got, err := r.List()
	if err != nil || len(got) != 2 || got["work"].APIURL != "http://w/api" {
		t.Fatalf("got %v %v", got, err)
	}
	if _, err := r.Resolve("home"); err == nil {
		t.Fatal("home resolved before it was added")
	}
	write(t, file, `{"work": {"store_dir": "/w", "api_url": "http://w/api"}, "home": {"store_dir": "/h", "api_url": "http://h/api", "token": "t"}}`)
	a, err := r.Resolve("home")
	if err != nil || a.StoreDir != "/h" || a.APIToken != "t" {
		t.Fatalf("after the edit: %+v %v", a, err)
	}
}

func TestUnknownAccountIsNamed(t *testing.T) {
	t.Parallel()
	r := &Registry{StoreDir: t.TempDir(), Accounts: `{"work": {"store_dir": "/w", "api_url": "http://w/api"}}`}
	_, err := r.Resolve("home")
	if err == nil || err.Error() != "Unknown WhatsApp account 'home'; configured: default, work" {
		t.Errorf("got %v", err)
	}
	for raw, want := range map[string]string{
		"[1]":                        "WHATSAPP_ACCOUNTS must be a JSON object",
		"{":                          "WHATSAPP_ACCOUNTS is not valid JSON",
		`{"x": {"store_dir": "/x"}}`: "account 'x' needs store_dir and api_url",
	} {
		if _, err := (&Registry{Accounts: raw}).List(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", raw, err)
		}
	}
}

// store/api_token is read on every request: the bridge may write a new one.
func TestTokenFileIsReadOnEveryCall(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, err := (&Registry{StoreDir: dir}).Resolve("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Token(); err == nil {
		t.Error("no token file, no error")
	}
	write(t, filepath.Join(dir, "api_token"), "first\n")
	if tok, _ := a.Token(); tok != "first" {
		t.Errorf("got %q", tok)
	}
	write(t, filepath.Join(dir, "api_token"), "second\n")
	if tok, _ := a.Token(); tok != "second" {
		t.Errorf("after the edit: %q", tok)
	}
	a.APIToken = "configured"
	if tok, _ := a.Token(); tok != "configured" {
		t.Errorf("configured token: %q", tok)
	}
}

// WHATSAPP_BRIDGE_TOKEN belongs to the single-account settings: an omitted
// account uses it, account "default" does not (PLAN.md §1.5 M3).
func TestBridgeTokenOnlyForTheImplicitAccount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, "api_token"), "file\n")
	r := &Registry{StoreDir: dir, BridgeToken: " env "}
	implicit, _ := r.Resolve("")
	if tok, _ := implicit.Token(); tok != "env" {
		t.Errorf("omitted account: %q", tok)
	}
	named, err := r.Resolve("default")
	if err != nil {
		t.Fatal(err)
	}
	if tok, _ := named.Token(); tok != "file" {
		t.Errorf("account default: %q", tok)
	}
}

func TestDefaultAccount(t *testing.T) {
	t.Parallel()
	r := &Registry{StoreDir: t.TempDir(), Default: "work",
		Accounts: `{"work": {"store_dir": "/w", "api_url": "http://w/api"}}`}
	if a, _ := r.Resolve(""); a.Name != "work" || r.DefaultName() != "work" {
		t.Errorf("got %+v", a)
	}
	// Without its own "default" entry the list has none.
	if got, _ := r.List(); got["default"] != nil {
		t.Error("implicit default listed")
	}
	// A default that does not exist falls back silently (M3).
	r.Default = "missing"
	if a, _ := r.Resolve(""); a.Name != "default" {
		t.Errorf("got %+v", a)
	}
}
