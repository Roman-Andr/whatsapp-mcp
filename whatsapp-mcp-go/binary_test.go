package main

// The built binary as systemd and MCP clients run it: SIGTERM with open
// calls, stdin closing, the token rule, plain log lines, and the
// install-service target.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	binaryOnce sync.Once
	binaryPath string
	errBinary  error
)

// builtBinary builds the server once per test run.
func builtBinary(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds the binary")
	}
	binaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "whatsapp-mcp-go-test-")
		if err != nil {
			errBinary = err
			return
		}
		binaryPath = filepath.Join(dir, "whatsapp-mcp-go")
		out, err := exec.Command("go", "build", "-tags", "sqlite_fts5", "-o", binaryPath, ".").CombinedOutput()
		if err != nil {
			errBinary = fmt.Errorf("%w: %s", err, out)
		}
	})
	if errBinary != nil {
		t.Fatal(errBinary)
	}
	return binaryPath
}

// process is the binary running with stderr collected.
type process struct {
	cmd    *exec.Cmd
	mu     sync.Mutex
	stderr []string
}

func (p *process) log() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.stderr, "\n")
}

func startBinary(t *testing.T, env []string, args ...string) *process {
	t.Helper()
	cmd := exec.Command(builtBinary(t), args...)
	cmd.Env = append(os.Environ(), env...)
	stderr, _ := cmd.StderrPipe()
	p := &process{cmd: cmd}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			p.mu.Lock()
			p.stderr = append(p.stderr, sc.Text())
			p.mu.Unlock()
		}
	}()
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	return p
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestBinaryStopsQuicklyOnSIGTERMWithOpenCalls(t *testing.T) {
	s := newStore(t)
	b := newStreamBridge(t, "")
	port := freePort(t)
	p := startBinary(t, []string{"WHATSAPP_STORE_DIR=" + s.Dir, "WHATSAPP_API_BASE_URL=" + b.url,
		"WHATSAPP_MCP_TOKEN=test-token-0123456789abcdef"}, "--transport", "http", "--port", fmt.Sprint(port))
	url := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	waitUntil(t, 10*time.Second, "the server", func() bool {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
		}
		return err == nil
	})
	// The event pump holds one stream for the whole life of the server.
	waitUntil(t, 5*time.Second, "the event pump", func() bool { return b.open.Load() == 1 })
	svc := &httpService{url: url}
	cs := svc.session(t)
	go cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "wait_for_events", Arguments: map[string]any{"timeout_seconds": 600}})
	waitUntil(t, 5*time.Second, "the waiting call's stream", func() bool { return b.open.Load() == 2 })
	if res := callTool(t, svc.session(t), "list_chats", nil); res.IsError {
		t.Fatalf("list_chats: %+v", res)
	}

	started := time.Now()
	p.cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan error, 1)
	go func() { done <- p.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit: %v\n%s", err, p.log())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("still running 5 s after SIGTERM\n%s", p.log())
	}
	if d := time.Since(started); d > 3*time.Second {
		t.Errorf("stopping took %v", d)
	} else {
		t.Logf("stopped %v after SIGTERM", d)
	}
	waitUntil(t, 3*time.Second, "every bridge stream to close", func() bool { return b.open.Load() == 0 })
	logged := p.log()
	if strings.Contains(logged, "panic") || strings.Contains(logged, "goroutine ") {
		t.Errorf("log:\n%s", logged)
	}
	// Plain lines for journald, which adds its own time stamps: the
	// start-up line is the slog message alone.
	first, _, _ := strings.Cut(logged, "\n")
	if want := fmt.Sprintf("WhatsApp MCP server on http://127.0.0.1:%d/mcp", port); first != want {
		t.Errorf("first log line is %q, not %q:\n%s", first, want, logged)
	}
	if regexp.MustCompile(`(?m)(^\d{4}[/-]\d\d[/-]\d\d |\btime=)`).MatchString(logged) {
		t.Errorf("time stamps in the log:\n%s", logged)
	}
}

func TestBinaryExitsWhenStdinCloses(t *testing.T) {
	s := newStore(t)
	b := newStreamBridge(t, "")
	cmd := exec.Command(builtBinary(t))
	cmd.Env = append(os.Environ(), "WHATSAPP_STORE_DIR="+s.Dir, "WHATSAPP_API_BASE_URL="+b.url)
	stdin, _ := cmd.StdinPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	waitUntil(t, 5*time.Second, "the event pump", func() bool { return b.open.Load() == 1 })
	started := time.Now()
	stdin.Close()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("exit: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("still running after stdin closed")
	}
	if d := time.Since(started); d > 3*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestBinaryRequiresAToken(t *testing.T) {
	newStore(t)
	cmd := exec.Command(builtBinary(t), "--transport", "http", "--port", fmt.Sprint(freePort(t)))
	cmd.Env = append(os.Environ(), "WHATSAPP_MCP_TOKEN=short")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(string(out), "WHATSAPP_MCP_TOKEN (at least 16 characters) is required") {
		t.Errorf("got %v: %s", err, out)
	}
}

func TestInstallServiceTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("runs make")
	}
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make is not installed")
	}
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "bin", "whatsapp-mcp-go")
	os.MkdirAll(filepath.Dir(bin), 0o700)
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700)
	units, config := filepath.Join(tmp, "units"), filepath.Join(tmp, "config")
	install := func(binary string) (string, error) {
		out, err := exec.Command("make", "-s", "install-service", "BIN="+binary, "SERVICE_DIR="+units,
			"CONFIG_DIR="+config, "SYSTEMCTL=true").CombinedOutput()
		return string(out), err
	}
	if out, err := install(filepath.Join(tmp, "missing")); err == nil || !strings.Contains(out, "run 'make build' first") {
		t.Errorf("installed without a binary: %v %s", err, out)
	}
	out, err := install(bin)
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	unit, _ := os.ReadFile(filepath.Join(units, "whatsapp-mcp.service"))
	cwd, _ := os.Getwd()
	for _, line := range []string{"ExecStart=" + bin + " --transport http", "EnvironmentFile=" + config + "/env",
		"WorkingDirectory=" + cwd, "Environment=PATH=%h/.local/bin:", "KillSignal=SIGTERM"} {
		assertContains(t, string(unit), line)
	}
	if body := strings.SplitN(string(unit), "[Unit]", 2)[1]; strings.Contains(body, "@") {
		t.Errorf("placeholder left in the unit:\n%s", unit)
	}
	env := filepath.Join(config, "env")
	st, _ := os.Stat(env)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("env file mode %v", st.Mode())
	}
	data, _ := os.ReadFile(env)
	token := strings.TrimPrefix(strings.TrimSpace(string(data)), "WHATSAPP_MCP_TOKEN=")
	if !regexp.MustCompile(`^[0-9a-f]{48}$`).MatchString(token) || strings.Contains(out, token) {
		t.Errorf("token %q (printed: %v)", token, strings.Contains(out, token))
	}
	assertContains(t, out, "claude mcp add -s user --transport http whatsapp http://127.0.0.1:8091/mcp")
	// A second install keeps the token clients are already configured with,
	// and so does an existing env file with other settings.
	if _, err := install(bin); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(env); string(again) != string(data) {
		t.Errorf("token replaced: %s", again)
	}
	os.WriteFile(env, []byte("WHATSAPP_MCP_TOKEN=kept-token-existing-0123\nWHATSAPP_MCP_PORT=8091\n"), 0o644)
	if _, err := install(bin); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(env); !strings.HasPrefix(string(again), "WHATSAPP_MCP_TOKEN=kept-token-existing-0123\n") {
		t.Errorf("env file replaced: %s", again)
	}
	if st, _ := os.Stat(env); st.Mode().Perm() != 0o600 {
		t.Errorf("env file mode %v", st.Mode())
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if binaryPath != "" {
		os.RemoveAll(filepath.Dir(binaryPath))
	}
	os.Exit(code)
}
