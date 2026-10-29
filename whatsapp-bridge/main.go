// Command whatsapp-bridge connects one WhatsApp account and serves it over a
// local REST API; see README.md. main reads the process inputs (signals,
// environment) and hands them to internal/app.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/app"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/logging"
)

func main() {
	// The root context of the run. It is set up first, so a signal during
	// start-up or pairing ends the bridge cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx)
	stop()
	os.Exit(code)
}

func run(ctx context.Context) int {
	getenv := os.Getenv //nolint:forbidigo // main reads the process environment and hands it to config.Load
	log := logging.New(os.Stderr)
	// WHATSAPP_BRIDGE_DIR runs the bridge in another directory, which holds
	// its store/: one directory (and one WHATSAPP_BRIDGE_ADDR port) per
	// WhatsApp account. It is read here and not in internal/config because
	// the chdir must come before anything resolves a relative path,
	// config.Load's allowlist and welcome files included.
	if dir := getenv("WHATSAPP_BRIDGE_DIR"); dir != "" {
		// G703: the directory is the operator's own setting.
		if err := os.MkdirAll(dir, 0700); err != nil { //nolint:gosec // see above
			log.Error("failed to create the bridge directory", "dir", dir, "err", err)
			return 1
		}
		if err := os.Chdir(dir); err != nil {
			log.Error("failed to enter the bridge directory", "dir", dir, "err", err)
			return 1
		}
	}
	// Every setting is checked before anything is opened.
	cfg, err := config.Load(getenv)
	if err != nil {
		log.Error("invalid configuration", "err", err)
		return 1
	}
	return app.Run(ctx, cfg, app.IO{Stdout: os.Stdout, Log: log})
}
