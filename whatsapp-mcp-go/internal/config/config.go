package config

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
)

// Config is every setting of the server.
type Config struct {
	// Transport is "stdio" or "http" (--transport, WHATSAPP_MCP_TRANSPORT).
	Transport string
	// Host and Port are where the HTTP transport listens.
	Host string
	Port int
	// Token is WHATSAPP_MCP_TOKEN, trimmed; HTTP clients send it.
	Token string
	// Version is the server version reported to clients (set by main).
	Version string

	// ReadOnly is WHATSAPP_READ_ONLY: tools that send or change something
	// are hidden.
	ReadOnly bool

	// StoreDir is WHATSAPP_STORE_DIR ("": the bridge's store next to the
	// binary).
	StoreDir string
	// APIBaseURL is WHATSAPP_API_BASE_URL ("": http://localhost:8080/api).
	APIBaseURL string
	// BridgeToken is WHATSAPP_BRIDGE_TOKEN, raw.
	BridgeToken string
	// Accounts, AccountsFile and DefaultAccount are WHATSAPP_ACCOUNTS,
	// WHATSAPP_ACCOUNTS_FILE and WHATSAPP_DEFAULT_ACCOUNT, raw: their
	// readers trim them where they always did.
	Accounts       string
	AccountsFile   string
	DefaultAccount string
	// ReadAllowlist and ReadAllowlistFile are WHATSAPP_READ_ALLOWLIST and
	// WHATSAPP_READ_ALLOWLIST_FILE, raw.
	ReadAllowlist     string
	ReadAllowlistFile string

	// Whisper are the transcription settings.
	Whisper Whisper
}

// Whisper are the WHATSAPP_WHISPER_* settings of voice transcription.
type Whisper struct {
	Model    string
	CLI      string
	CacheDir string
	Threads  int
}

// Error is a start-up failure: main prints Msg (a line, or nothing) on
// stderr and exits with Code.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Env reads the settings the export subcommand and the server share: all
// but the transport, host, port and token.
func Env(getenv func(string) string) *Config {
	threads, _ := strconv.Atoi(getenv("WHATSAPP_WHISPER_THREADS"))
	return &Config{
		ReadOnly:          readOnly(getenv("WHATSAPP_READ_ONLY")),
		StoreDir:          getenv("WHATSAPP_STORE_DIR"),
		APIBaseURL:        getenv("WHATSAPP_API_BASE_URL"),
		BridgeToken:       getenv("WHATSAPP_BRIDGE_TOKEN"),
		Accounts:          getenv("WHATSAPP_ACCOUNTS"),
		AccountsFile:      getenv("WHATSAPP_ACCOUNTS_FILE"),
		DefaultAccount:    getenv("WHATSAPP_DEFAULT_ACCOUNT"),
		ReadAllowlist:     getenv("WHATSAPP_READ_ALLOWLIST"),
		ReadAllowlistFile: getenv("WHATSAPP_READ_ALLOWLIST_FILE"),
		Whisper: Whisper{
			Model:    getenv("WHATSAPP_WHISPER_MODEL"),
			CLI:      getenv("WHATSAPP_WHISPER_CLI"),
			CacheDir: getenv("WHATSAPP_WHISPER_CACHE"),
			Threads:  threads,
		},
	}
}

// readOnly: WHATSAPP_READ_ONLY hides every tool that sends or changes
// something, so an agent reading chats that may contain prompt injections
// has nothing to act with. The bridge refuses the same calls when it has
// the variable too.
func readOnly(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func envOr(getenv func(string) string, name, fallback string) string {
	if v := getenv(name); v != "" {
		return v
	}
	return fallback
}

// Load reads the server's settings: the environment, then the flags of
// args (os.Args, the program name first). The checks run in the order the
// server always made them, so the first failure wins: a bad
// WHATSAPP_MCP_PORT before the flags are parsed (so --port cannot repair
// it), then the transport, then the HTTP token.
func Load(getenv func(string) string, args []string) (*Config, error) {
	c := Env(getenv)
	name := ""
	if len(args) > 0 {
		name, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	transport := fs.String("transport", envOr(getenv, "WHATSAPP_MCP_TRANSPORT", "stdio"), "stdio or http")
	host := fs.String("host", envOr(getenv, "WHATSAPP_MCP_HOST", "127.0.0.1"), "address to listen on (http)")
	defaultPort, err := strconv.Atoi(envOr(getenv, "WHATSAPP_MCP_PORT", "8091"))
	if err != nil {
		return nil, &Error{Code: 1, Msg: fmt.Sprintf("WHATSAPP_MCP_PORT: %v", err) + "\n"}
	}
	port := fs.Int("port", defaultPort, "port to listen on (http)")
	// The flag set has printed the problem and the usage already; the
	// codes are flag.ExitOnError's.
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil, &Error{Code: 0}
		}
		return nil, &Error{Code: 2}
	}
	c.Transport, c.Host, c.Port = *transport, *host, *port
	if c.Transport != "stdio" && c.Transport != "http" {
		return nil, &Error{Code: 2, Msg: fmt.Sprintf("--transport must be stdio or http, not %q\n", c.Transport)}
	}
	if c.Transport == "http" {
		c.Token = strings.TrimSpace(getenv("WHATSAPP_MCP_TOKEN"))
		if len(c.Token) < 16 {
			return nil, &Error{Code: 1, Msg: "WHATSAPP_MCP_TOKEN (at least 16 characters) is required for --transport http; " +
				"clients send it as 'Authorization: Bearer <token>'" + "\n"}
		}
	}
	return c, nil
}
