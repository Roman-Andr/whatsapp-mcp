package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	_ "github.com/mattn/go-sqlite3" // the sqlite3 driver of both databases
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/api"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/media"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// Paths and DSNs, relative to the bridge directory (main changes into
// WHATSAPP_BRIDGE_DIR first). They are part of the deployment surface: the
// MCP server and the systemd units find the store there.
const (
	storeDir    = "store"
	sessionDB   = "store/whatsapp.db"
	sessionDSN  = "file:store/whatsapp.db?_foreign_keys=on"
	tokenPath   = "store/api_token"
	secretPath  = "store/webhook_secret" //nolint:gosec // G101: the path of the secret file, not a secret
	auditPath   = "store/audit.log"
	qrImagePath = "store/qr.png"
)

// stableConnection is how long the connection must hold after Connect before
// the bridge reports itself connected: a session WhatsApp rejects drops
// within that time.
const stableConnection = 2 * time.Second

// IO is where the application writes: the terminal UI and the log.
type IO struct {
	// Stdout receives the console output (QR code, banners).
	Stdout io.Writer
	// Log receives everything else.
	Log *slog.Logger
}

// hooks are the seams the lifecycle tests replace.
type hooks struct {
	// newClient builds the WhatsApp client for the device store.
	newClient func(device *wastore.Device, log *slog.Logger) client
	// stable is how long the connection must hold after Connect.
	stable time.Duration
	// ready is called with the bridge once it waits for a signal or a
	// logout; nil does nothing.
	ready func(b *bridge.Bridge)
	// step is called before each shutdown step with its name; nil does
	// nothing.
	step func(name string)
}

func liveHooks() hooks {
	return hooks{newClient: newLiveClient, stable: stableConnection}
}

// Run starts the bridge with cfg and runs it until ctx ends (a signal: exit
// code 0) or it fails, is not paired or is logged out (exit code 1, so the
// service manager restarts it). Everything it started is shut down, in
// order, before it returns.
func Run(ctx context.Context, cfg *config.Config, out IO) int {
	return run(ctx, cfg, out, liveHooks())
}

// app is one run of the bridge: what the steps build and shutdown closes.
type app struct {
	cfg   *config.Config
	out   console
	log   *slog.Logger
	hooks hooks

	container *sqlstore.Container
	device    *wastore.Device
	messages  *store.Store
	client    client
	audit     *audit.Log
	webhook   *events.Webhook
	queue     *media.Queue
	bridge    *bridge.Bridge
	pairing   *wa.Pairing
	restart   chan struct{}
	api       apiDeps
	server    *api.Server

	// workers are the goroutines the app starts itself (the pairing-code
	// request); shutdown waits for them.
	workers sync.WaitGroup
}

func run(ctx context.Context, cfg *config.Config, out IO, h hooks) int {
	a := &app{cfg: cfg, out: console{w: out.Stdout}, log: out.Log, hooks: h, restart: make(chan struct{}, 1)}
	// Shutdown runs after the signal cancelled ctx; its steps get deadlines
	// of their own.
	defer a.shutdown(context.WithoutCancel(ctx))
	a.log.Info("starting WhatsApp client")
	if err := a.openStores(ctx); err != nil {
		a.log.Error("start-up failed", "err", err)
		return 1
	}
	if err := a.newClient(); err != nil {
		a.log.Error("start-up failed", "err", err)
		return 1
	}
	if err := a.newBridge(ctx); err != nil {
		a.log.Error("start-up failed", "err", err)
		return 1
	}
	if err := a.startAPI(ctx); err != nil {
		a.log.Error("failed to start REST API", "addr", cfg.Addr, "err", err)
		return 1
	}
	if a.client.OwnID() == nil {
		if code, done := a.pair(ctx); done {
			return code
		}
	} else if err := a.client.Connect(); err != nil {
		a.log.Error("failed to connect", "err", err)
		return 1
	}
	if code, ok := a.connect(ctx); !ok {
		return code
	}
	return a.wait(ctx)
}

// openStores opens the session database (whatsmeow's container and its
// device) and messages.db, in the store directory.
func (a *app) openStores(ctx context.Context) error {
	if err := store.EnsureDir(storeDir); err != nil {
		return err
	}
	container, err := sqlstore.New(ctx, "sqlite3", sessionDSN, logging.WA(a.log, "Database"))
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	a.container = container
	store.RestrictFiles(sessionDB, a.log)

	// The device store holds the session; without one the bridge has to pair.
	device, err := container.GetFirstDevice(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		device, err = container.NewDevice(), nil
		a.log.Info("created new device")
	}
	if err != nil {
		return fmt.Errorf("failed to get device: %w", err)
	}
	a.device = device

	messages, err := store.Open(ctx, storeDir, store.WithLogger(a.log))
	if err != nil {
		return fmt.Errorf("failed to initialize message store: %w", err)
	}
	a.messages = messages
	return nil
}

// newClient applies the device properties a new pairing announces and
// builds the WhatsApp client.
func (a *app) newClient() error {
	if err := wa.ApplyDeviceProps(a.cfg.HistoryDays, a.cfg.DeviceName); err != nil {
		return err
	}
	a.client = a.hooks.newClient(a.device, a.log)
	return nil
}

// newBridge builds the bridge service and what it depends on, in the order
// the start-up notes are printed, then brings the store up to date with
// what the device store knows.
func (a *app) newBridge(ctx context.Context) error {
	cfg := a.cfg
	token, err := config.Secret(cfg.Token, tokenPath, a.log)
	if err != nil {
		return fmt.Errorf("failed to load API token: %w", err)
	}
	// config.Load has checked the entries; these cannot fail.
	sendAllow, err := policy.NewAllowlist(cfg.SendAllowlist)
	if err != nil {
		return err
	}
	if sendAllow.Restricted() {
		a.out.sendAllowlist(sendAllow.Entries())
	}
	readAllow, err := policy.NewAllowlist(cfg.ReadAllowlist)
	if err != nil {
		return err
	}
	if readAllow.Restricted() {
		a.out.readAllowlist(readAllow.Entries())
	}
	limiter := policy.NewRateLimiter(cfg.SendRate, cfg.SendBurst)
	if cfg.ReadOnly {
		a.out.readOnly()
	}
	if len(cfg.Welcomes) > 0 {
		a.out.welcomes(len(cfg.Welcomes))
	}
	autoDownload := media.NewAutoDownloadConfig(cfg.AutoDownload.Enabled, cfg.AutoDownload.MaxVideoBytes, cfg.AutoDownload.MaxFileBytes)
	roots, err := policy.NewMediaRoots(cfg.MediaRoots, cfg.MediaOutbox)
	if err != nil {
		return err
	}
	a.out.mediaRoots(roots.Dirs())
	// The secret is only loaded (or generated into store/webhook_secret)
	// when there is somewhere to send it.
	switch {
	case cfg.Webhook.Disabled:
		a.out.webhookDisabled()
	case cfg.Webhook.URL != "":
		secret, err := config.Secret(cfg.Webhook.Secret, secretPath, a.log)
		if err != nil {
			return fmt.Errorf("failed to load the webhook secret: %w", err)
		}
		// The deliveries outlive the run's context: shutdown drains them after
		// the signal, through Close.
		a.webhook = events.NewWebhook(cfg.Webhook.URL, secret, a.log) //nolint:contextcheck // see above
	}
	if a.audit, err = audit.Open(auditPath, a.log); err != nil {
		return err
	}

	hub := events.NewHub(a.webhook)
	mediaService := &media.Service{Client: a.client, Store: a.messages, Host: media.DefaultHost, Log: a.log}
	a.pairing = wa.NewPairing(qrImagePath)
	reconnector := wa.NewReconnector(
		func() bool { return a.client.OwnID() != nil },
		func() error {
			a.client.Disconnect()
			return a.client.Connect()
		},
	)
	if autoDownload.Enabled {
		a.queue = media.NewQueue(ctx, autoDownload, func(ctx context.Context, job media.Job) error {
			_, err := mediaService.Download(ctx, job.MessageID, job.ChatJID)
			return err
		}, a.log)
		a.out.autoDownload(autoDownload.MaxVideoBytes>>20, autoDownload.MaxFileBytes>>20)
	}
	a.bridge = bridge.New(bridge.Deps{
		Client:        a.client,
		Store:         a.messages,
		Media:         mediaService,
		Events:        hub,
		Audit:         a.audit,
		SendAllowlist: sendAllow,
		ReadAllowlist: readAllow,
		ReadOnly:      cfg.ReadOnly,
		MediaRoots:    roots,
		Limiter:       limiter,
		Session:       wa.NewSessionTracker(),
		Pairing:       a.pairing,
		Reconnector:   reconnector,
		AutoDownload:  a.queue,
		Welcomes:      cfg.Welcomes,
		Presence:      cfg.Presence,
		RejectCalls:   bridge.CallRejection{Enabled: cfg.RejectCalls.Enabled, Message: cfg.RejectCalls.Message},
		Log:           a.log,
		BaseContext:   ctx,
		StoreDir:      storeDir,
		Restart:       a.restart,
	})
	a.client.AddEventHandler(a.bridge.HandleEvent)
	a.bridge.MergeLIDChats(ctx)
	if err := a.bridge.PublishReadAllowlist(ctx); err != nil {
		return fmt.Errorf("failed to publish read allowlist: %w", err)
	}
	if err := roots.Publish(ctx, a.messages); err != nil {
		return fmt.Errorf("failed to publish media roots: %w", err)
	}
	a.api = apiDeps{token: token, limiter: limiter, events: hub}
	return nil
}
