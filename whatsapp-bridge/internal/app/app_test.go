package app

import (
	"bytes"
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"testing"

	"go.mau.fi/whatsmeow"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa/watest"
)

// The tests change into a temporary bridge directory, as main does with
// WHATSAPP_BRIDGE_DIR, so they do not run in parallel.

var ownJID = types.NewJID("1000", types.DefaultUserServer)

// syncBuffer is an output both the run and its pairing-code worker write.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// fakeClient is the lifecycle on top of the watest client: it never
// contacts WhatsApp.
type fakeClient struct {
	*watest.Client
	qr         chan whatsmeow.QRChannelItem
	connectErr error
	stays      bool   // the connection holds after Connect
	onConnect  func() // runs inside Connect
	record     func(string)

	mu       sync.Mutex
	handlers int
}

func (c *fakeClient) Connect() error {
	c.record("connect")
	if c.onConnect != nil {
		c.onConnect()
	}
	c.Connected = c.stays
	return c.connectErr
}

func (c *fakeClient) Disconnect() { c.record("disconnected") }

func (c *fakeClient) GetQRChannel(context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return c.qr, nil
}

func (c *fakeClient) PairPhone(_ context.Context, phone string, _ bool, _ whatsmeow.PairClientType, _ string) (string, error) {
	if phone != "15551234" {
		return "", context.Canceled
	}
	return "ABCD-EFGH", nil
}

func (c *fakeClient) AddEventHandler(whatsmeow.EventHandler) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers++
	return uint32(c.handlers)
}

// harness runs the application with a fake client in a temporary bridge
// directory.
type harness struct {
	t      *testing.T
	cfg    *config.Config
	client *fakeClient
	out    *syncBuffer
	logs   *syncBuffer

	mu    sync.Mutex
	steps []string // connect, then the shutdown steps
}

func newHarness(t *testing.T, linked bool) *harness {
	t.Helper()
	t.Chdir(t.TempDir())
	h := &harness{
		t: t,
		cfg: &config.Config{
			Addr:         "127.0.0.1:0",
			MediaOutbox:  t.TempDir(),
			AutoDownload: config.AutoDownload{Enabled: true, MaxVideoBytes: 25 << 20, MaxFileBytes: 100 << 20},
			// Never reached: no event is published in these runs.
			Webhook: config.Webhook{URL: "http://127.0.0.1:1/hook"},
		},
		out:  &syncBuffer{},
		logs: &syncBuffer{},
	}
	fake := watest.New(ownJID, types.EmptyJID)
	fake.Connected = false
	if !linked {
		fake.ID = nil
	}
	h.client = &fakeClient{Client: fake, stays: true, record: h.record, qr: make(chan whatsmeow.QRChannelItem, 4)}
	return h
}

func (h *harness) record(step string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steps = append(h.steps, step)
}

func (h *harness) recorded() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.steps)
}

// run runs the application; ready is called once it waits.
func (h *harness) run(ctx context.Context, ready func(*bridge.Bridge)) int {
	return run(ctx, h.cfg, IO{Stdout: h.out, Log: slog.New(slog.NewTextHandler(h.logs, nil))}, hooks{
		newClient: func(*wastore.Device, *slog.Logger) client { return h.client },
		ready:     ready,
		step:      h.record,
	})
}

// shutdownOrder is every shutdown step in order, with the client's
// Disconnect call ("disconnected") inside its step.
var shutdownOrder = []string{"api", "disconnect", "disconnected", "workers", "queue", "webhook", "audit", "store", "container"}

func TestRunStopsOnASignal(t *testing.T) {
	h := newHarness(t, true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	code := h.run(ctx, func(*bridge.Bridge) { cancel() })
	if code != 0 {
		t.Errorf("exit code %d; log:\n%s", code, h.logs.String())
	}
	if got := h.recorded(); !slices.Equal(got, append([]string{"connect"}, shutdownOrder...)) {
		t.Errorf("steps %v", got)
	}
	out := h.out.String()
	for _, want := range []string{
		"Files may be sent from: ",
		"Downloading new media automatically (videos up to 25 MB, other files up to 100 MB)\n",
		"\n✓ Connected to WhatsApp!\nREST server is running. Press Ctrl+C to disconnect and exit.\n",
		"Disconnecting...\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console lacks %q:\n%s", want, out)
		}
	}
}

func TestRunExitsOnLogout(t *testing.T) {
	h := newHarness(t, true)
	code := h.run(t.Context(), func(b *bridge.Bridge) { b.RequestRestart() })
	if code != 1 {
		t.Errorf("exit code %d", code)
	}
	if !strings.Contains(h.out.String(), "Logged out; exiting so the bridge can pair again\n") {
		t.Errorf("console:\n%s", h.out.String())
	}
	if got := h.recorded(); !slices.Equal(got, append([]string{"connect"}, shutdownOrder...)) {
		t.Errorf("steps %v", got)
	}
}

func TestRunPairs(t *testing.T) {
	h := newHarness(t, false)
	h.cfg.PairPhone = "15551234"
	h.cfg.HistoryDays = 30
	h.cfg.DeviceName = "Snow ops"
	h.client.qr <- whatsmeow.QRChannelItem{Event: "code", Code: "2@abc,def"}
	h.client.qr <- whatsmeow.QRChannelItem{Event: "timeout-ish"}
	h.client.qr <- whatsmeow.QRChannelItem{Event: "success"}
	close(h.client.qr)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if code := h.run(ctx, func(*bridge.Bridge) { cancel() }); code != 0 {
		t.Errorf("exit code %d; log:\n%s", code, h.logs.String())
	}
	out := h.out.String()
	for _, want := range []string{
		"Requesting 30 days of history from the phone\n",
		`Linking as "Snow ops"` + "\n",
		"\nScan this QR code with your WhatsApp app (also saved as store/qr.png):\n",
		"\nOr link with phone number: on the phone open WhatsApp > Linked devices > Link with phone number and enter ABCD-EFGH\n",
		"\nSuccessfully connected and authenticated!\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("console lacks %q:\n%s", want, out)
		}
	}
	if !strings.Contains(h.logs.String(), "event=timeout-ish") {
		t.Errorf("unknown pairing event not logged:\n%s", h.logs.String())
	}
}

func TestRunFailures(t *testing.T) {
	cases := []struct {
		name   string
		linked bool
		setup  func(h *harness, cancel context.CancelFunc)
		code   int
		log    string
	}{
		{"not paired", false, func(h *harness, _ context.CancelFunc) { close(h.client.qr) }, 1, "pairing did not complete"},
		{"signal while pairing", false, func(h *harness, cancel context.CancelFunc) { h.client.onConnect = cancel }, 0, ""},
		{"connection does not hold", true, func(h *harness, _ context.CancelFunc) { h.client.stays = false }, 1, "failed to establish stable connection"},
		{"connect fails", true, func(h *harness, _ context.CancelFunc) { h.client.connectErr = context.DeadlineExceeded }, 1, "failed to connect"},
		{"API port taken", true, func(h *harness, _ context.CancelFunc) { h.cfg.Addr = "256.0.0.1:1" }, 1, "failed to start REST API"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.linked)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c.setup(h, cancel)
			if code := h.run(ctx, func(*bridge.Bridge) { t.Error("the run reached the wait") }); code != c.code {
				t.Errorf("exit code %d, want %d", code, c.code)
			}
			if !strings.Contains(h.logs.String(), c.log) {
				t.Errorf("log lacks %q:\n%s", c.log, h.logs.String())
			}
			steps := h.recorded()
			if slices.Contains(steps, "connect") && !slices.Contains(steps, "disconnected") {
				t.Errorf("connected but never disconnected: %v", steps)
			}
			if !slices.Contains(steps, "container") {
				t.Errorf("session database left open: %v", steps)
			}
		})
	}
}
