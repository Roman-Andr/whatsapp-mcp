package main

// Golden transcripts of the built binary: the MCP server's whole external
// surface (tools, their output, resources, notifications, the HTTP
// transport and the command line), driven through stdin/stdout, HTTP and
// environment variables only. Nothing here calls into package main, so the
// goldens stay valid however the code behind main.go is arranged.
//
// The request scripts are the parity harness's (parity/requests*.json,
// removed in 57465cd), restored verbatim under testdata/golden/requests.
// Regenerate the goldens with
//
//	go test -tags sqlite_fts5 -run Golden . -update
//
// and review them by reading: they are the contract later changes keep.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

// goldenTimeout bounds each scenario.
const goldenTimeout = 60 * time.Second

// goldenEnv is one scenario's directories, fixture and fake bridge.
type goldenEnv struct {
	t      *testing.T
	root   string // every temp dir of the scenario is below it
	store  string
	image  string
	bridge *goldenBridge
	extra  []string
	// replace are scenario-specific literal replacements (the HTTP port).
	replace [][2]string
}

// newGoldenEnv copies a fresh fixture into a temp dir and starts the fake
// bridge; setup runs between the two, as in parity.sh.
func newGoldenEnv(t *testing.T, configure func(b *goldenBridge), setup func(e *goldenEnv)) *goldenEnv {
	t.Helper()
	e := &goldenEnv{t: t, root: t.TempDir()}
	e.store = filepath.Join(e.root, "store")
	e.image = filepath.Join(e.root, "pic.png")
	for _, d := range []string{"home", "cache", "data", "tmp", "path"} {
		if err := os.MkdirAll(filepath.Join(e.root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	buildGoldenStore(t, e.store)
	writeGoldenPNG(t, e.image)
	if setup != nil {
		setup(e)
	}
	e.bridge = newGoldenBridge(t, e.image, configure)
	return e
}

func (e *goldenEnv) apiURL() string { return e.bridge.srv.URL + "/api" }

// environ is the child's whole environment: nothing of this process's. A
// variable in extra overrides the default (exec keeps the last one).
func (e *goldenEnv) environ() []string {
	env := []string{
		"HOME=" + filepath.Join(e.root, "home"),
		"XDG_CACHE_HOME=" + filepath.Join(e.root, "cache"),
		"XDG_DATA_HOME=" + filepath.Join(e.root, "data"),
		"TMPDIR=" + filepath.Join(e.root, "tmp"),
		// An empty directory: no ffmpeg, no whisper-cli.
		"PATH=" + filepath.Join(e.root, "path"),
		"TZ=Europe/Moscow",
		"LANG=C.UTF-8",
		"WHATSAPP_STORE_DIR=" + e.store,
		"WHATSAPP_API_BASE_URL=" + e.apiURL(),
		"WHATSAPP_WHISPER_CLI=/nonexistent/whisper-cli",
	}
	return append(env, e.extra...)
}

// normalize replaces what differs between runs. Nothing else is touched.
func (e *goldenEnv) normalize(s string) string {
	// The scenario's store, then its other temp dirs, and the directory of
	// the built binary (--help prints its path).
	s = strings.ReplaceAll(s, e.store, "<STORE>")
	s = strings.ReplaceAll(s, e.root, "<TMP>")
	if binaryPath != "" {
		s = strings.ReplaceAll(s, filepath.Dir(binaryPath), "<TMP>")
	}
	// The fake bridge listens on a random port.
	s = strings.ReplaceAll(s, strings.TrimPrefix(e.bridge.srv.URL, "http://"), "<BRIDGE>")
	// Scenario-specific literals: the HTTP transport's random port.
	for _, r := range e.replace {
		s = strings.ReplaceAll(s, r[0], r[1])
	}
	// Export file names carry the time of the export.
	s = stampRE.ReplaceAllString(s, "_<STAMP>.")
	// So does the JSON export's exported_at.
	s = exportedAtRE.ReplaceAllString(s, "${1}<NOW>")
	// Converted stickers and voice notes get a random name in the outbox.
	s = outboxRE.ReplaceAllString(s, "<OUTBOX>/<RANDOM>.${1}")
	// How long wait_for_events waited depends on scheduling.
	s = waitedRE.ReplaceAllString(s, "${1}<N>")
	// The status row S1 sits two hours before the start of the run.
	for _, form := range statusTimeForms() {
		s = strings.ReplaceAll(s, form, "<NOW-2h>")
	}
	return s
}

var (
	stampRE      = regexp.MustCompile(`_\d{8}_\d{6}\.`)
	exportedAtRE = regexp.MustCompile(`(exported_at\\*":\s*\\*")[^"\\]+`)
	outboxRE     = regexp.MustCompile(`<STORE>/outbox/(?:sticker|whatsapp)-\d+\.(webp|ogg)`)
	waitedRE     = regexp.MustCompile(`(waited_seconds\\*":\s*)\d+`)
)

// statusTimeForms are the renderings of S1's time in the server's output.
func statusTimeForms() []string {
	st := goldenStatusTime()
	moscow, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		moscow = time.FixedZone("MSK", 3*3600)
	}
	var forms []string
	for _, t := range []time.Time{st, st.In(moscow)} {
		for _, layout := range []string{"2006-01-02 15:04:05-07:00", "2006-01-02T15:04:05-07:00", "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04"} {
			forms = append(forms, t.Format(layout))
		}
	}
	return forms
}

// child is the binary serving MCP over stdio.
type child struct {
	t        *testing.T
	e        *goldenEnv
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	lines    chan []byte
	deadline time.Time
	stderr   *syncBuffer
	// notifications are the messages without an id received so far.
	notifications [][]byte
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

// rpcMessage is a JSON-RPC message from the server. Result and Error keep
// the bytes as received: their key order is part of the contract.
type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func startChild(t *testing.T, e *goldenEnv) *child {
	t.Helper()
	cmd := exec.Command(builtBinary(t), "--transport", "stdio")
	cmd.Env = e.environ()
	cmd.Dir = e.root
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c := &child{t: t, e: e, cmd: cmd, stdin: stdin, lines: make(chan []byte, 64),
		deadline: time.Now().Add(goldenTimeout), stderr: &syncBuffer{}}
	cmd.Stderr = c.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		defer close(c.lines)
		r := bufio.NewReader(stdout)
		for {
			line, err := r.ReadBytes('\n')
			if len(bytes.TrimSpace(line)) > 0 {
				c.lines <- bytes.TrimRight(line, "\r\n")
			}
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Logf("server stderr:\n%s", c.stderr.String())
		}
	})
	return c
}

func (c *child) send(msg string) {
	c.t.Helper()
	if _, err := io.WriteString(c.stdin, msg+"\n"); err != nil {
		c.t.Fatalf("writing to the server: %v\n%s", err, c.stderr.String())
	}
}

// await returns the answer to request id, keeping notifications.
func (c *child) await(id int) rpcMessage {
	c.t.Helper()
	want := strconv.Itoa(id)
	timer := time.NewTimer(time.Until(c.deadline))
	defer timer.Stop()
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				c.t.Fatalf("server exited before answering request %d\n%s", id, c.stderr.String())
			}
			var msg rpcMessage
			if err := json.Unmarshal(line, &msg); err != nil {
				c.t.Fatalf("not JSON from the server: %v: %s", err, line)
			}
			if msg.ID == nil {
				c.notifications = append(c.notifications, line)
				continue
			}
			if string(msg.ID) == want {
				return msg
			}
		case <-timer.C:
			c.t.Fatalf("no answer to request %d within the scenario's %v\n%s", id, goldenTimeout, c.stderr.String())
		}
	}
}

// collect gathers notifications until d has passed.
func (c *child) collect(d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case line, ok := <-c.lines:
			if !ok {
				return
			}
			c.notifications = append(c.notifications, line)
		case <-timer.C:
			return
		}
	}
}

// initialize runs the handshake and returns the raw initialize result.
func (c *child) initialize() json.RawMessage {
	c.t.Helper()
	c.send(`{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"golden","version":"0"}}}`)
	msg := c.await(0)
	if msg.Result == nil {
		c.t.Fatalf("initialize failed: %s", msg.Error)
	}
	c.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	return msg.Result
}

// call sends request id and returns its result or error as received.
func (c *child) call(id int, method string, params json.RawMessage) json.RawMessage {
	c.t.Helper()
	c.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params))
	msg := c.await(id)
	if msg.Result != nil {
		return msg.Result
	}
	return msg.Error
}

// stop closes stdin, on which the server exits.
func (c *child) stop() {
	c.t.Helper()
	c.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- c.cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			c.t.Errorf("server exit: %v\n%s", err, c.stderr.String())
		}
	case <-time.After(10 * time.Second):
		c.t.Errorf("server still running 10 s after stdin closed\n%s", c.stderr.String())
	}
}

// scriptRequest is one [method, params] of a request script.
type scriptRequest struct {
	method string
	params json.RawMessage // as in the script, compacted
}

func loadScript(t *testing.T, name string) []scriptRequest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "golden", "requests", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var raw [][]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	out := make([]scriptRequest, len(raw))
	for i, r := range raw {
		if len(r) != 2 {
			t.Fatalf("%s request %d: %s", name, i+1, data)
		}
		var method string
		if err := json.Unmarshal(r[0], &method); err != nil {
			t.Fatal(err)
		}
		var params bytes.Buffer
		if err := json.Compact(&params, r[1]); err != nil {
			t.Fatal(err)
		}
		out[i] = scriptRequest{method, params.Bytes()}
	}
	return out
}

// transcriptLine is one request's JSONL line: the request as scripted, the
// server's answer byte for byte, what the bridge received while it ran,
// and the content of the file an export wrote.
func (e *goldenEnv) transcriptLine(req scriptRequest, response json.RawMessage, calls []bridgeCall) string {
	e.t.Helper()
	method, _ := marshalGolden(req.method)
	bridge, err := marshalGolden(calls)
	if err != nil {
		e.t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(`{"request":[` + string(method) + `,` + string(req.params) + `],"response":`)
	b.Write(response)
	b.WriteString(`,"bridge":` + string(bridge))
	if content, ok := exportedFile(response); ok {
		raw, _ := marshalGolden(content)
		b.WriteString(`,"exported_file":` + string(raw))
	}
	b.WriteString("}")
	return e.normalize(b.String())
}

// exportedFile is the content of a text file an export_chat call wrote
// (parity/drive.py's attach_export).
func exportedFile(response json.RawMessage) (string, bool) {
	var r struct {
		StructuredContent struct {
			Result json.RawMessage `json:"result"`
		} `json:"structuredContent"`
	}
	if json.Unmarshal(response, &r) != nil {
		return "", false
	}
	var result struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(r.StructuredContent.Result, &result) != nil || result.Path == "" {
		return "", false
	}
	if !strings.HasSuffix(result.Path, ".txt") && !strings.HasSuffix(result.Path, ".json") && !strings.HasSuffix(result.Path, ".html") {
		return "", false
	}
	data, err := os.ReadFile(result.Path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// runScript runs a request script and checks <name>.jsonl and
// <name>.initialize.json.
func runScript(t *testing.T, name string, script []scriptRequest, e *goldenEnv) {
	t.Helper()
	c := startChild(t, e)
	initResult := c.initialize()
	e.bridge.take() // the handshake asks the bridge nothing; start clean
	var lines []string
	for i, req := range script {
		params := json.RawMessage(strings.ReplaceAll(string(req.params), "@STORE@", e.store))
		response := c.call(i+1, req.method, params)
		lines = append(lines, e.transcriptLine(req, response, e.bridge.take()))
	}
	c.stop()
	checkGolden(t, name+".initialize.json", e.normalize(string(initResult))+"\n")
	checkGolden(t, name+".jsonl", strings.Join(lines, "\n")+"\n")
}

func TestGolden(t *testing.T) {
	builtBinary(t) // skipped with -short
	accounts := func(e *goldenEnv) {
		work := filepath.Join(e.root, "work")
		buildGoldenWorkStore(e.t, work)
		e.extra = append(e.extra, `WHATSAPP_ACCOUNTS={"work": {"store_dir": "`+work+`", "api_url": "`+e.apiURL()+`/"}}`)
	}
	parity := []struct {
		name  string
		setup func(e *goldenEnv)
		env   func(e *goldenEnv)
	}{
		{name: "main"},
		{name: "allowlist", setup: func(e *goldenEnv) {
			goldenExec(e.t, filepath.Join(e.store, "messages.db"),
				`INSERT OR REPLACE INTO bridge_meta (key, value) VALUES ('read_allowlist', '["18046727669@s.whatsapp.net"]')`)
		}},
		{name: "accounts", env: accounts},
		{name: "readonly", env: func(e *goldenEnv) { e.extra = append(e.extra, "WHATSAPP_READ_ONLY=1") }},
		{name: "mediaroots", setup: func(e *goldenEnv) { setupGoldenMediaRoots(e.t, e.store, e.image) }},
		// Beyond parity: nothing listens on port 1, so every bridge call
		// fails the same way on Linux; the error texts stay literal.
		{name: "bridge-down", env: func(e *goldenEnv) {
			e.extra = append(e.extra, "WHATSAPP_API_BASE_URL=http://127.0.0.1:1/api")
		}},
	}
	for _, sc := range parity {
		t.Run(sc.name, func(t *testing.T) {
			e := newGoldenEnv(t, nil, sc.setup)
			if sc.env != nil {
				sc.env(e)
			}
			runScript(t, sc.name, loadScript(t, sc.name), e)
		})
	}

	t.Run("accounts-readonly", func(t *testing.T) {
		e := newGoldenEnv(t, nil, nil)
		accounts(e)
		e.extra = append(e.extra, "WHATSAPP_READ_ONLY=1")
		runScript(t, "accounts-readonly", []scriptRequest{{"tools/list", json.RawMessage(`{}`)}}, e)
	})

	t.Run("notifications", testGoldenNotifications)
	t.Run("http", testGoldenHTTP)
	t.Run("cli", testGoldenCLI)
}

// testGoldenNotifications subscribes to two resources, lets the bridge send
// a message, a session and a presence event, and records the
// resource-updated URIs the client hears within five seconds.
func testGoldenNotifications(t *testing.T) {
	release := make(chan struct{})
	e := newGoldenEnv(t, func(b *goldenBridge) {
		b.events = []string{
			`{"type": "message", "chat_jid": "` + goldenGroup + `", "id": "N1", "content": "hello"}`,
			`{"type": "session", "state": "logged_out"}`,
			`{"type": "presence", "chat_jid": "` + goldenGroup + `", "jid": "18046727669@s.whatsapp.net", "available": true}`,
		}
		// Held back until the client has subscribed, and only sent once.
		b.release = release
		b.hold = true
	}, nil)
	c := startChild(t, e)
	initResult := c.initialize()
	e.bridge.take()
	var lines []string
	for i, uri := range []string{"whatsapp://chats", "whatsapp://chat/" + goldenGroup} {
		req := scriptRequest{"resources/subscribe", json.RawMessage(`{"uri":"` + uri + `"}`)}
		lines = append(lines, e.transcriptLine(req, c.call(i+1, req.method, req.params), e.bridge.take()))
	}
	c.notifications = nil
	close(release)
	c.collect(5 * time.Second)
	uris := []string{}
	for _, line := range c.notifications {
		var msg rpcMessage
		json.Unmarshal(line, &msg)
		if msg.Method != "notifications/resources/updated" {
			continue
		}
		var p struct {
			URI string `json:"uri"`
		}
		json.Unmarshal(msg.Params, &p)
		uris = append(uris, p.URI)
	}
	sort.Strings(uris)
	raw, _ := marshalGolden(uris)
	lines = append(lines, `{"notifications/resources/updated":`+string(raw)+`}`)
	c.stop()
	checkGolden(t, "notifications.initialize.json", e.normalize(string(initResult))+"\n")
	checkGolden(t, "notifications.jsonl", e.normalize(strings.Join(lines, "\n"))+"\n")
}

// testGoldenHTTP pins the HTTP transport: the start-up line, the 401 for a
// missing or wrong token, a 404, and the handshake and tools/list over
// streamable HTTP, which must list exactly what stdio lists.
func testGoldenHTTP(t *testing.T) {
	const token = "golden-token-0123456789"
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()
	e := newGoldenEnv(t, nil, nil)

	// The stdio tools/list to compare with.
	c := startChild(t, e)
	c.initialize()
	stdioTools := c.call(1, "tools/list", json.RawMessage(`{}`))
	c.stop()

	port := freePort(t)
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	e.replace = append(e.replace, [2]string{addr, "127.0.0.1:<PORT>"})
	e.extra = append(e.extra, "WHATSAPP_MCP_TOKEN="+token)
	cmd := exec.CommandContext(ctx, builtBinary(t), "--transport", "http", "--port", strconv.Itoa(port))
	cmd.Env = e.environ()
	cmd.Dir = e.root
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	logged := &syncBuffer{}
	firstLine := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stderr)
		first := true
		for sc.Scan() {
			if first {
				firstLine <- sc.Text()
				first = false
			}
			fmt.Fprintln(logged, sc.Text())
		}
		if first {
			close(firstLine)
		}
	}()
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
		if t.Failed() {
			t.Logf("server stderr:\n%s", logged.String())
		}
	})
	var lines []string
	record := func(check string, v any) {
		raw, err := marshalGolden(v)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, e.normalize(`{"check":`+strconv.Quote(check)+`,"result":`+string(raw)+`}`))
	}
	select {
	case line, ok := <-firstLine:
		if !ok {
			t.Fatalf("server exited without a log line")
		}
		record("first stderr line", line)
	case <-time.After(10 * time.Second):
		t.Fatal("no start-up line within 10 s")
	}

	client := &http.Client{Transport: &http.Transport{Proxy: nil}}
	url := "http://" + addr
	do := func(method, path, auth string, body string, headers map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, url+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp, data
	}
	type unauthorized struct {
		Status          int    `json:"status"`
		ContentType     string `json:"content_type"`
		WWWAuthenticate string `json:"www_authenticate"`
		Body            string `json:"body"`
	}
	initialize := `{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"golden","version":"0"}}}`
	mcpHeaders := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	for _, check := range []struct{ name, auth string }{
		{"POST /mcp without a token", ""},
		{"POST /mcp with a wrong token", "Bearer wrong-token-0123456789"},
	} {
		resp, body := do(http.MethodPost, "/mcp", check.auth, initialize, mcpHeaders)
		record(check.name, unauthorized{resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("WWW-Authenticate"), string(body)})
	}
	resp, body := do(http.MethodGet, "/nope", "Bearer "+token, "", nil)
	record("GET /nope", struct {
		Status int    `json:"status"`
		Body   string `json:"body"`
	}{resp.StatusCode, string(body)})

	// Streamable HTTP: the answer comes as JSON or as an SSE event.
	rpc := func(id int, body string, session string) (json.RawMessage, string) {
		t.Helper()
		headers := map[string]string{}
		for k, v := range mcpHeaders {
			headers[k] = v
		}
		if session != "" {
			headers["Mcp-Session-Id"] = session
			headers["Mcp-Protocol-Version"] = "2025-06-18"
		}
		resp, data := do(http.MethodPost, "/mcp", "Bearer "+token, body, headers)
		if id < 0 {
			if resp.StatusCode != http.StatusAccepted {
				t.Fatalf("notification: %d %s", resp.StatusCode, data)
			}
			return nil, ""
		}
		var payloads [][]byte
		if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			for _, line := range strings.Split(string(data), "\n") {
				if p, ok := strings.CutPrefix(line, "data: "); ok {
					payloads = append(payloads, []byte(p))
				}
			}
		} else {
			payloads = append(payloads, data)
		}
		for _, p := range payloads {
			var msg rpcMessage
			if json.Unmarshal(p, &msg) == nil && string(msg.ID) == strconv.Itoa(id) {
				if msg.Result != nil {
					return msg.Result, resp.Header.Get("Mcp-Session-Id")
				}
				return msg.Error, resp.Header.Get("Mcp-Session-Id")
			}
		}
		t.Fatalf("no answer to %d: %d %s", id, resp.StatusCode, data)
		return nil, ""
	}
	initResult, session := rpc(0, initialize, "")
	if session == "" {
		t.Fatal("no Mcp-Session-Id")
	}
	record("initialize over HTTP", initResult)
	rpc(-1, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, session)
	httpTools, _ := rpc(1, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, session)
	record("tools/list over HTTP", httpTools)
	if !bytes.Equal(httpTools, stdioTools) {
		t.Errorf("tools/list over HTTP differs from stdio")
	}

	cmd.Process.Signal(syscall.SIGTERM)
	cmd.Wait()
	checkGolden(t, "http.jsonl", strings.Join(lines, "\n")+"\n")
}

// testGoldenCLI pins exit codes and output of the command line: bad
// transports, the token rule, --help, and the export subcommand.
func testGoldenCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), goldenTimeout)
	defer cancel()
	e := newGoldenEnv(t, nil, nil)
	out := filepath.Join(e.root, "out")
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	type run struct {
		args []string
		env  []string
		file string // written by the run, recorded
	}
	runs := []run{
		{args: []string{"--transport", "bogus"}},
		{args: []string{"--transport", "http"}},
		{args: []string{"--transport", "http"}, env: []string{"WHATSAPP_MCP_TOKEN=golden-token-01"}},
		{args: []string{"--help"}},
		{args: []string{"export"}},
		{args: []string{"export", goldenGroup, "--format", "bogus"}},
	}
	for _, format := range []string{"txt", "json", "html"} {
		path := filepath.Join(out, "chat."+format)
		runs = append(runs, run{args: []string{"export", goldenGroup, "--format", format, "--out", path}, file: path})
	}
	var lines []string
	for _, r := range runs {
		cmd := exec.CommandContext(ctx, builtBinary(t), r.args...)
		cmd.Env = append(e.environ(), r.env...)
		cmd.Dir = e.root
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		code := 0
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			t.Fatalf("%v: still running after the scenario's %v\n%s", r.args, goldenTimeout, stderr.String())
		case errors.As(err, &exit):
			code = exit.ExitCode()
		case err != nil:
			t.Fatalf("%v: %v\n%s", r.args, err, stderr.String())
		}
		entry := struct {
			Args   []string `json:"args"`
			Env    []string `json:"env"`
			Exit   int      `json:"exit"`
			Stdout string   `json:"stdout"`
			Stderr string   `json:"stderr"`
			File   *string  `json:"file,omitempty"`
		}{Args: r.args, Env: r.env, Exit: code, Stdout: stdout.String(), Stderr: stderr.String()}
		if entry.Env == nil {
			entry.Env = []string{}
		}
		if r.file != "" {
			data, err := os.ReadFile(r.file)
			if err != nil {
				t.Errorf("%v wrote nothing: %v", r.args, err)
			}
			s := string(data)
			entry.File = &s
		}
		raw, err := marshalGolden(entry)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, e.normalize(string(raw)))
	}
	checkGolden(t, "cli.jsonl", strings.Join(lines, "\n")+"\n")
}

// checkGolden compares got with testdata/golden/<name>, or rewrites it
// with -update.
func checkGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got {
		t.Errorf("%s differs from the golden (-want +got):\n%s", path, lineDiff(string(want), got))
	}
}

// lineDiff is a unified diff of two texts by line, without the context
// lines: the goldens' lines are long, and for each changed pair the first
// difference is pointed out.
func lineDiff(want, got string) string {
	a, b := strings.Split(want, "\n"), strings.Split(got, "\n")
	// Longest common subsequence, by dynamic programming: the files have a
	// few hundred lines at most.
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out strings.Builder
	var removed, added []string
	var at int
	flush := func() {
		if len(removed) == 0 && len(added) == 0 {
			return
		}
		fmt.Fprintf(&out, "@@ line %d @@\n", at+1)
		for _, l := range removed {
			out.WriteString("-" + l + "\n")
		}
		for _, l := range added {
			out.WriteString("+" + l + "\n")
		}
		for k := range min(len(removed), len(added)) {
			out.WriteString(firstDifference(removed[k], added[k]))
		}
		removed, added = nil, nil
	}
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			flush()
			i++
			j++
		case j < len(b) && (i == len(a) || lcs[i][j+1] >= lcs[i+1][j]):
			if len(removed) == 0 && len(added) == 0 {
				at = i
			}
			added = append(added, b[j])
			j++
		default:
			if len(removed) == 0 && len(added) == 0 {
				at = i
			}
			removed = append(removed, a[i])
			i++
		}
	}
	flush()
	return out.String()
}

// firstDifference shows where two lines start to differ.
func firstDifference(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	from := max(0, n-60)
	clip := func(s string) string { return s[min(from, len(s)):min(n+60, len(s))] }
	return fmt.Sprintf("  first difference at byte %d:\n    want …%s…\n    got  …%s…\n", n, clip(a), clip(b))
}
