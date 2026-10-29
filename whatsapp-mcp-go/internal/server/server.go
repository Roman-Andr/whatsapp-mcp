package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/account"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/mediaconv"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/resources"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/tools"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/media/transcribe"
)

// Deps are the parts of the server that main and tests provide.
type Deps struct {
	// Media are the conversions the tools use.
	Media mediaconv.Toolkit
	// Log is where the server logs.
	Log *slog.Logger
}

// DefaultDeps are the real conversions, with the whisper settings of cfg,
// logging to log.
func DefaultDeps(cfg *config.Config, log *slog.Logger) Deps {
	w := cfg.Whisper
	return Deps{
		Media: mediaconv.Default(transcribe.Config{Model: w.Model, CLI: w.CLI, CacheDir: w.CacheDir, Threads: w.Threads}),
		Log:   log,
	}
}

// Server is the MCP server with the accounts that have resources of their own.
type Server struct {
	*mcp.Server
	token string
	named []string
	calls *callTracker
	pool  *store.Pool
	res   *resources.Deps
}

// New builds the MCP server with every tool and resource.
func New(cfg *config.Config, deps Deps) (*Server, error) {
	registry := &account.Registry{StoreDir: cfg.StoreDir, APIURL: cfg.APIBaseURL, BridgeToken: cfg.BridgeToken,
		Accounts: cfg.Accounts, AccountsFile: cfg.AccountsFile, Default: cfg.DefaultAccount}
	accounts, err := registry.List()
	if err != nil {
		return nil, err
	}
	pool := store.NewPool(deps.Log)
	httpClient := bridgeapi.NewHTTPClient()
	scope := store.Scope{List: cfg.ReadAllowlist, File: cfg.ReadAllowlistFile}
	s := mcp.NewServer(&mcp.Implementation{Name: "whatsapp", Version: cfg.Version}, &mcp.ServerOptions{
		// Clients subscribe to resources to hear about new messages.
		SubscribeHandler:   func(context.Context, *mcp.SubscribeRequest) error { return nil },
		UnsubscribeHandler: func(context.Context, *mcp.UnsubscribeRequest) error { return nil },
		Capabilities:       &mcp.ServerCapabilities{},
	})
	// With several accounts (WHATSAPP_ACCOUNTS) every tool takes `account`.
	calls := newCallTracker()
	toolDeps := &tools.Deps{Accounts: registry, Pool: pool, HTTP: httpClient, Scope: scope, Media: deps.Media, Log: deps.Log}
	names := tools.Register(s, toolDeps, calls, len(accounts) > 1, cfg.ReadOnly)
	known := map[string]bool{}
	for _, name := range names {
		known[name] = true
	}
	s.AddReceivingMiddleware(tools.UnknownTool(known))
	named := resources.NamedAccounts(accounts, deps.Log)
	res := &resources.Deps{Accounts: registry, Pool: pool, HTTP: httpClient, Scope: scope, Log: deps.Log}
	resources.Register(s, res, named)
	return &Server{Server: s, token: cfg.Token, named: named, calls: calls, pool: pool, res: res}, nil
}

// pumpEvents runs the event pumps until ctx ends; wait returns once they
// have closed their bridge connections.
func (s *Server) pumpEvents(ctx context.Context) (wait func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		resources.NewPump(s.res, s.named, func(uri string) {
			// It only fails for parameters it does not have to check here.
			_ = s.ResourceUpdated(ctx, &mcp.ResourceUpdatedNotificationParams{URI: uri})
		}).Run(ctx)
	}()
	return func() { <-done }
}

// ServeStdio serves one client over t until it closes its end or ctx ends.
// The event pumps stop with it, so the process exits at once when stdin
// closes instead of waiting for a silent event stream's read timeout.
func (s *Server) ServeStdio(ctx context.Context, t mcp.Transport) error {
	ctx, cancel := context.WithCancel(ctx)
	wait := s.pumpEvents(ctx)
	err := s.Run(ctx, t)
	cancel()
	wait()
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		err = nil
	}
	return err
}

// shutdownGrace is how long open requests get to finish on shutdown.
// Clients keep a GET stream open for notifications and wait_for_events may
// run for minutes: after this they are cut off and their calls cancelled.
const shutdownGrace = time.Second

// ServeHTTP serves every client on l until ctx ends, then shuts down
// within about shutdownGrace.
func (s *Server) ServeHTTP(ctx context.Context, l net.Listener) error {
	pumpCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	wait := s.pumpEvents(pumpCtx)
	srv := &http.Server{Handler: s.httpHandler(s.token), ReadHeaderTimeout: 10 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(l) }()
	var err error
	select {
	case err = <-served:
	case <-ctx.Done():
		// ctx has ended: the grace period needs a context of its own.
		shutdownCtx, done := context.WithTimeout(context.WithoutCancel(ctx), shutdownGrace)
		if srv.Shutdown(shutdownCtx) != nil {
			srv.Close()
		}
		done()
		<-served
		// Sessions outlive their HTTP requests, and closing one waits for
		// its running calls: cancel those first, so they let go of the
		// bridge and the sessions close at once.
		s.calls.cancelAll()
		for ss := range s.Sessions() {
			ss.Close()
		}
	}
	cancel()
	wait()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

// Close closes the store connections.
func (s *Server) Close() { s.pool.Close() }

// tokenGuard requires "Authorization: Bearer <token>" on every HTTP
// request. Over HTTP any local process (or a web page making requests to
// localhost) could otherwise read every chat.
func tokenGuard(next http.Handler, token string) http.Handler {
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expected) != 1 {
			w.Header().Set("Content-Type", "text/plain")
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("Unauthorized"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// httpHandler serves the MCP server over streamable HTTP at /mcp, behind
// the token check.
func (s *Server) httpHandler(token string) http.Handler {
	// A session that sends nothing for 30 minutes is closed; clients open a
	// new one on their next call.
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.Server },
		&mcp.StreamableHTTPOptions{SessionTimeout: 30 * time.Minute})
	mux := http.NewServeMux()
	mux.Handle("/mcp", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A client ending its session (Claude Code exiting) cancels its
		// calls; the SDK would wait for them before closing the session.
		if r.Method == http.MethodDelete {
			if id := r.Header.Get("Mcp-Session-Id"); id != "" {
				s.calls.cancelSession(id)
			}
		}
		mcpHandler.ServeHTTP(w, r)
	}))
	return tokenGuard(mux, token)
}
