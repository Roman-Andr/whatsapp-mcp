// Command whatsapp-mcp-go is the WhatsApp MCP server: it serves the tools and
// resources over stdio or streamable HTTP, reading the bridge's store
// read-only and calling its REST API.
//
//	whatsapp-mcp-go [--transport stdio|http] [--host 127.0.0.1] [--port 8091]
//	whatsapp-mcp-go export <chat JID> [--format txt|json|html] ...
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/export"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/server"
)

// version is set at build time (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	log := logging.New(os.Stderr)
	if len(os.Args) > 1 && os.Args[1] == "export" {
		os.Exit(export.Main(context.Background(), config.Env(os.Getenv), log, os.Args[2:], os.Stdout, os.Stderr))
	}
	cfg, err := config.Load(os.Getenv, os.Args)
	if err != nil {
		var e *config.Error
		if errors.As(err, &e) {
			fmt.Fprint(os.Stderr, e.Msg)
			os.Exit(e.Code)
		}
		fatal(log, err)
	}
	cfg.Version = version
	if err := run(cfg, log); err != nil {
		fatal(log, err)
	}
}

// fatal logs err and exits with 1.
func fatal(log *slog.Logger, err error) {
	log.Error("server failed", "err", err)
	os.Exit(1)
}

// run serves until a signal arrives or, over stdio, the client closes its
// end; its deferred clean-up has run when it returns.
func run(cfg *config.Config, log *slog.Logger) error {
	s, err := server.New(cfg, server.DefaultDeps(cfg, log))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if cfg.Transport == "stdio" {
		return s.ServeStdio(ctx, &mcp.StdioTransport{})
	}
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	l, err := (&net.ListenConfig{}).Listen(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	// The line operators and the golden test look for, verbatim.
	log.Info("WhatsApp MCP server on http://" + addr + "/mcp") //nolint:sloglint // the start-up line, pinned by testdata/golden/http.jsonl
	return s.ServeHTTP(ctx, l)
}
