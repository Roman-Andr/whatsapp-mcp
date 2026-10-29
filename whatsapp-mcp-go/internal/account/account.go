package account

// Accounts
//
// One bridge serves one WhatsApp account. To use several, run a bridge per
// account (each with its own directory and port) and list them in
// WHATSAPP_ACCOUNTS (JSON) or WHATSAPP_ACCOUNTS_FILE:
//   {"work": {"store_dir": "/path/to/work/store", "api_url": "http://localhost:8081/api"}, ...}
// Every tool then takes `account`; the single-account settings are the
// account "default" unless the list names its own default
// (WHATSAPP_DEFAULT_ACCOUNT).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
)

// Account is one bridge: its store directory and REST API.
type Account struct {
	Name     string
	StoreDir string
	APIURL   string
	// APIToken is the token of the account's entry in WHATSAPP_ACCOUNTS;
	// "" reads store/api_token.
	APIToken string
	// implicit marks the single-account settings used when no account was
	// named; only they fall back to WHATSAPP_BRIDGE_TOKEN.
	implicit bool
	// envToken is WHATSAPP_BRIDGE_TOKEN.
	envToken string
}

// MessagesDB is the bridge's message store.
func (a *Account) MessagesDB() string { return filepath.Join(a.StoreDir, "messages.db") }

// WhatsappDB is whatsmeow's store: contacts and the LID map.
func (a *Account) WhatsappDB() string { return filepath.Join(a.StoreDir, "whatsapp.db") }

// Token is the bridge API token: the configured one, else (for the
// single-account settings) WHATSAPP_BRIDGE_TOKEN, else store/api_token,
// read on every call so a new token is picked up without a restart.
func (a *Account) Token() (string, error) {
	if a.APIToken != "" {
		return a.APIToken, nil
	}
	// The environment token belongs to the single-account setup.
	if a.implicit {
		if t := strings.TrimSpace(a.envToken); t != "" {
			return t, nil
		}
	}
	data, err := os.ReadFile(filepath.Join(a.StoreDir, "api_token"))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// Registry resolves account names. Its fields are the settings of
// internal/config; the account list file is read on every call.
type Registry struct {
	// StoreDir and APIURL are the single-account settings ("": defaults).
	StoreDir, APIURL string
	// BridgeToken is WHATSAPP_BRIDGE_TOKEN.
	BridgeToken string
	// Accounts and AccountsFile are WHATSAPP_ACCOUNTS and _FILE.
	Accounts, AccountsFile string
	// Default is WHATSAPP_DEFAULT_ACCOUNT.
	Default string
}

// defaultStoreDir is WHATSAPP_STORE_DIR, else the bridge's store next to
// this program (whatsapp-mcp-go/../whatsapp-bridge/store).
func (r *Registry) defaultStoreDir() string {
	if r.StoreDir != "" {
		return r.StoreDir
	}
	base := "."
	if exe, err := os.Executable(); err == nil {
		base = filepath.Dir(exe)
	}
	return filepath.Join(base, "..", "whatsapp-bridge", "store")
}

func (r *Registry) defaultAPIURL() string {
	if r.APIURL != "" {
		return r.APIURL
	}
	return "http://localhost:8080/api"
}

func (r *Registry) implicitAccount() *Account {
	return &Account{Name: "default", StoreDir: r.defaultStoreDir(), APIURL: r.defaultAPIURL(), implicit: true, envToken: r.BridgeToken}
}

// List reads WHATSAPP_ACCOUNTS / WHATSAPP_ACCOUNTS_FILE, plus "default"
// from the single-account settings when the list lacks it.
func (r *Registry) List() (map[string]*Account, error) {
	raw := strings.TrimSpace(r.Accounts)
	path := strings.TrimSpace(r.AccountsFile)
	if raw == "" && path != "" {
		data, err := os.ReadFile(path) //nolint:gosec // G304: the file WHATSAPP_ACCOUNTS_FILE names, read as configured
		if err != nil {
			return nil, err
		}
		raw = string(data)
	}
	accounts := map[string]*Account{}
	if raw != "" {
		var data map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &data); err != nil {
			var anyValue any
			if json.Unmarshal([]byte(raw), &anyValue) == nil {
				return nil, fmt.Errorf("WHATSAPP_ACCOUNTS must be a JSON object of name -> {store_dir, api_url}")
			}
			return nil, fmt.Errorf("WHATSAPP_ACCOUNTS is not valid JSON: %w", err)
		}
		for name, rawCfg := range data {
			var cfg map[string]any
			if json.Unmarshal(rawCfg, &cfg) != nil || cfg == nil {
				return nil, fmt.Errorf("account %s needs store_dir and api_url", pyfmt.Repr(name))
			}
			store, _ := cfg["store_dir"].(string)
			api, _ := cfg["api_url"].(string)
			if store == "" || api == "" {
				return nil, fmt.Errorf("account %s needs store_dir and api_url", pyfmt.Repr(name))
			}
			token, _ := cfg["token"].(string)
			accounts[name] = &Account{Name: name, StoreDir: pyfmt.ExpandUser(store), APIURL: strings.TrimRight(api, "/"), APIToken: token}
		}
	}
	if _, ok := accounts["default"]; !ok && r.Default == "" {
		a := r.implicitAccount()
		a.implicit = false
		accounts["default"] = a
	}
	return accounts, nil
}

// DefaultName is the name of the default account.
func (r *Registry) DefaultName() string {
	if name := strings.TrimSpace(r.Default); name != "" {
		return name
	}
	return "default"
}

// Resolve is the account a call runs against: the one named, or the
// default (WHATSAPP_DEFAULT_ACCOUNT, else the single-account settings).
func (r *Registry) Resolve(name string) (*Account, error) {
	if name == "" {
		if r.Default != "" {
			if accounts, err := r.List(); err == nil {
				if a := accounts[r.DefaultName()]; a != nil {
					return a, nil
				}
			}
		}
		return r.implicitAccount(), nil
	}
	accounts, err := r.List()
	if err != nil {
		return nil, err
	}
	a, ok := accounts[name]
	if !ok {
		//nolint:staticcheck // the message clients already see, kept verbatim
		return nil, fmt.Errorf("Unknown WhatsApp account %s; configured: %s", pyfmt.Repr(name), strings.Join(SortedNames(accounts), ", "))
	}
	return a, nil
}

// SortedNames are the names of accounts in order.
func SortedNames(accounts map[string]*Account) []string {
	names := make([]string, 0, len(accounts))
	for n := range accounts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
