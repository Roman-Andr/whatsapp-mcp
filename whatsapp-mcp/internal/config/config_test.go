package config

import (
	"errors"
	"strings"
	"testing"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

func loadErr(t *testing.T, vars map[string]string, args ...string) *Error {
	t.Helper()
	_, err := Load(env(vars), append([]string{"whatsapp-mcp"}, args...))
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("got %v", err)
	}
	return e
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()
	c, err := Load(env(nil), []string{"whatsapp-mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport != "stdio" || c.Host != "127.0.0.1" || c.Port != 8091 || c.Token != "" || c.ReadOnly {
		t.Errorf("got %+v", c)
	}
}

func TestFlagsOverrideTheEnvironment(t *testing.T) {
	t.Parallel()
	vars := map[string]string{"WHATSAPP_MCP_TRANSPORT": "http", "WHATSAPP_MCP_HOST": "0.0.0.0", "WHATSAPP_MCP_PORT": "9000",
		"WHATSAPP_MCP_TOKEN": "  0123456789abcdef  ", "WHATSAPP_STORE_DIR": "/s", "WHATSAPP_WHISPER_THREADS": "3"}
	c, err := Load(env(vars), []string{"x"})
	if err != nil || c.Transport != "http" || c.Host != "0.0.0.0" || c.Port != 9000 || c.Token != "0123456789abcdef" ||
		c.StoreDir != "/s" || c.Whisper.Threads != 3 {
		t.Fatalf("got %+v %v", c, err)
	}
	c, err = Load(env(vars), []string{"x", "--transport", "stdio", "--host", "::1", "--port", "1234"})
	if err != nil || c.Transport != "stdio" || c.Host != "::1" || c.Port != 1234 {
		t.Errorf("got %+v %v", c, err)
	}
}

// A bad WHATSAPP_MCP_PORT is fatal before the flags are parsed, so --port
// cannot repair it (PLAN.md §1.5 M4).
func TestBadPortInTheEnvironmentWinsOverTheFlag(t *testing.T) {
	t.Parallel()
	e := loadErr(t, map[string]string{"WHATSAPP_MCP_PORT": "eighty"}, "--port", "8091")
	if e.Code != 1 || !strings.HasPrefix(e.Msg, "WHATSAPP_MCP_PORT: ") || !strings.HasSuffix(e.Msg, "\n") {
		t.Errorf("got %d %q", e.Code, e.Msg)
	}
}

func TestBadTransport(t *testing.T) {
	t.Parallel()
	e := loadErr(t, nil, "--transport", "x")
	if e.Code != 2 || e.Msg != "--transport must be stdio or http, not \"x\"\n" {
		t.Errorf("got %d %q", e.Code, e.Msg)
	}
	if e := loadErr(t, nil, "--nope"); e.Code != 2 || e.Msg != "" {
		t.Errorf("unknown flag: %d %q", e.Code, e.Msg)
	}
	if e := loadErr(t, nil, "-h"); e.Code != 0 {
		t.Errorf("help: %d", e.Code)
	}
}

func TestHTTPNeedsAToken(t *testing.T) {
	t.Parallel()
	e := loadErr(t, map[string]string{"WHATSAPP_MCP_TOKEN": "short"}, "--transport", "http")
	if e.Code != 1 || !strings.HasPrefix(e.Msg, "WHATSAPP_MCP_TOKEN (at least 16 characters) is required for --transport http") {
		t.Errorf("got %d %q", e.Code, e.Msg)
	}
	// stdio does not need one.
	if _, err := Load(env(map[string]string{"WHATSAPP_MCP_TOKEN": "short"}), []string{"x"}); err != nil {
		t.Error(err)
	}
}

func TestReadOnlyValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value string
		want  bool
	}{
		{"1", true}, {"true", true}, {" YES ", true}, {"on", true},
		{"", false}, {"0", false}, {"no", false}, {"enabled", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()
			if got := Env(env(map[string]string{"WHATSAPP_READ_ONLY": tc.value})).ReadOnly; got != tc.want {
				t.Errorf("got %v", got)
			}
		})
	}
}
