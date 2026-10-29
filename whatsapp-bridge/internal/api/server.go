package api

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// Deps is what the HTTP layer is built from.
type Deps struct {
	// Bridge is the service the handlers call (*bridge.Bridge).
	Bridge Service
	// Token is required on every request (Bearer or X-API-Token).
	Token string
	// ReadOnly refuses every state-changing route (WHATSAPP_READ_ONLY).
	ReadOnly bool
	// Limiter is the send rate limit; nil is unlimited.
	Limiter *policy.RateLimiter
	// Audit records every state-changing call.
	Audit *audit.Log
	// Events feeds /api/events.
	Events *events.Hub
	// Logger is the bridge's logger; nil discards.
	Logger *slog.Logger
}

// readHeaderTimeout bounds how long a client may take to send the headers.
const readHeaderTimeout = 10 * time.Second

// Server is the running REST API.
type Server struct {
	srv  *http.Server
	addr net.Addr
	done chan struct{} // closed when Serve has returned
}

// Start serves the API on addr, which is localhost only. The port is bound
// before returning, so a second bridge on the same port fails at start
// instead of running without an API. ctx bounds only the bind.
func Start(ctx context.Context, addr string, d Deps) (*Server, error) {
	h := newHandler(d)
	h.quit = make(chan struct{})
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	s := &Server{
		srv:  &http.Server{Handler: h.serve(), ReadHeaderTimeout: readHeaderTimeout},
		addr: listener.Addr(),
		done: make(chan struct{}),
	}
	s.srv.RegisterOnShutdown(func() { close(h.quit) })
	h.log.Info("REST API listening", "addr", addr)
	go func() {
		defer close(s.done)
		if err := s.srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			h.log.Error("REST API server error", "err", err)
		}
	}()
	return s, nil
}

// Shutdown stops accepting requests and waits for the running ones until
// ctx ends. Open event streams end at once.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	<-s.done
	return err
}

// Close stops the server at once.
func (s *Server) Close() error {
	err := s.srv.Close()
	<-s.done
	return err
}
