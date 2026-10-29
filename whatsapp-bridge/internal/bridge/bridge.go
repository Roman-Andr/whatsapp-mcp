package bridge

import (
	"context"
	"log/slog"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	hub "github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/media"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// Bridge bundles what the event handlers and the service methods share.
type Bridge struct {
	client wa.Client
	store  *store.Store
	media  *media.Service
	log    *slog.Logger
	events *hub.Hub
	// base is the parent of every event handler's context: the application's
	// root context, cancelled at shutdown.
	base     context.Context //nolint:containedctx // the application's lifetime, which HandleEvent has no other way to receive
	storeDir string          // the store directory, whose files are never sent

	allowlist       *policy.Allowlist // send allowlist
	readAllowlist   *policy.Allowlist
	readOnly        bool                // WHATSAPP_READ_ONLY: refuse every state change
	mediaRoots      *policy.MediaRoots  // WHATSAPP_MEDIA_ROOTS; nil refuses every file
	limiter         *policy.RateLimiter // nil: unlimited
	pairing         *wa.Pairing
	welcomes        map[string]string // group JID -> welcome text (WHATSAPP_WELCOME_FILE)
	calls           *callLog
	presence        *presenceLog
	presenceEnabled bool          // WHATSAPP_PRESENCE
	rejectCalls     *callRejecter // WHATSAPP_REJECT_CALLS
	restart         chan struct{} // asks app.Run to exit so the service restarts the bridge
	audit           *audit.Log
	session         *wa.SessionTracker
	reconnector     *wa.Reconnector // POST /api/connection/reconnect

	autoDownload *media.Queue
}

// Deps is what a Bridge is built from.
type Deps struct {
	// Client is nil only in tests that never reach WhatsApp.
	Client wa.Client
	Store  *store.Store
	// Media downloads attachments; New sets its Author.
	Media  *media.Service
	Events *hub.Hub
	Audit  *audit.Log

	SendAllowlist *policy.Allowlist
	ReadAllowlist *policy.Allowlist
	ReadOnly      bool
	MediaRoots    *policy.MediaRoots
	Limiter       *policy.RateLimiter

	Session     *wa.SessionTracker
	Pairing     *wa.Pairing
	Reconnector *wa.Reconnector
	// AutoDownload queues new attachments; nil downloads nothing on arrival.
	AutoDownload *media.Queue

	Welcomes    map[string]string
	Presence    bool // WHATSAPP_PRESENCE
	RejectCalls CallRejection

	// Log receives the bridge's log lines; nil discards them.
	Log *slog.Logger
	// BaseContext is the parent of every event handler's context: the
	// application's root context. Nil means context.Background().
	BaseContext context.Context //nolint:containedctx // handed on to Bridge.base
	// StoreDir is the store directory; files directly in it hold the session
	// and are never sent. Empty means "store".
	StoreDir string
	// Restart receives a value when the bridge must exit to pair again.
	Restart chan struct{}
}

// CallRejection is WHATSAPP_REJECT_CALLS and WHATSAPP_REJECT_CALLS_MESSAGE.
type CallRejection struct {
	Enabled bool
	Message string
}

// New builds a Bridge, with the call and presence logs it keeps itself.
func New(d Deps) *Bridge {
	b := &Bridge{
		client:          d.Client,
		store:           d.Store,
		media:           d.Media,
		log:             d.Log,
		base:            d.BaseContext,
		storeDir:        d.StoreDir,
		events:          d.Events,
		allowlist:       d.SendAllowlist,
		readAllowlist:   d.ReadAllowlist,
		readOnly:        d.ReadOnly,
		mediaRoots:      d.MediaRoots,
		limiter:         d.Limiter,
		audit:           d.Audit,
		session:         d.Session,
		pairing:         d.Pairing,
		welcomes:        d.Welcomes,
		calls:           newCallLog(),
		presence:        newPresenceLog(),
		presenceEnabled: d.Presence,
		rejectCalls:     newCallRejecter(d.RejectCalls),
		restart:         d.Restart,
		reconnector:     d.Reconnector,
		autoDownload:    d.AutoDownload,
	}
	if b.log == nil {
		b.log = slog.New(slog.DiscardHandler)
	}
	if b.base == nil {
		// Only tests build a bridge without an application around it.
		b.base = context.Background()
	}
	if b.storeDir == "" {
		b.storeDir = media.DefaultRoot
	}
	if b.media != nil {
		// A re-upload request names the message's author, which only the
		// bridge can work out.
		b.media.Author = b.authorJID
	}
	return b
}

// Online reports whether the bridge can talk to WhatsApp as the account:
// connected and linked. A bridge waiting for a QR scan is connected but not
// logged in, and requests then hang until they time out.
func (b *Bridge) Online() bool {
	return b.client != nil && b.client.IsConnected() && b.client.IsLoggedIn()
}

// SetSession records a session state change, logs it and publishes it as a
// "session" event (SSE and webhook) so an agent or a monitor notices.
func (b *Bridge) SetSession(state, reason string, needsAction bool) {
	if b.session == nil {
		return
	}
	status, changed := b.session.Set(state, reason, needsAction)
	if !changed {
		return
	}
	if needsAction {
		b.log.Error("WhatsApp session needs action", "state", state, "reason", reason)
	} else {
		b.log.Info("WhatsApp session changed", "state", state)
	}
	b.events.Publish(hub.Event{Type: "session", State: state, Content: reason, Timestamp: status.Since})
}
