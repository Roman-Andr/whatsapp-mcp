package app

import (
	"context"
	"time"
)

// Shutdown bounds. Together they stay well under the systemd unit's
// TimeoutStopSec=30: API 5 s, webhook 5 s, the queue and the workers 5 s
// each, and the quick steps 2 s each.
const (
	apiShutdown     = 5 * time.Second
	webhookShutdown = 5 * time.Second
	workerShutdown  = 5 * time.Second
	quickShutdown   = 2 * time.Second
)

// shutdown stops what the run started, in order: the API (so no request
// reaches what follows), the WhatsApp connection (no more events), the
// auto-download queue, the webhook (drained), the audit log, messages.db and
// the session database. Each step is bounded; one that hangs is logged and
// left behind, since the process is about to exit.
func (a *app) shutdown(ctx context.Context) {
	if a.server != nil {
		a.step(ctx, "api", apiShutdown, func(ctx context.Context) {
			// SSE streams end through their request contexts.
			if err := a.server.Shutdown(ctx); err != nil {
				a.log.Warn("REST API did not stop in time", "err", err)
			}
		})
	}
	if a.client != nil {
		a.step(ctx, "disconnect", quickShutdown, func(context.Context) { a.client.Disconnect() })
	}
	a.step(ctx, "workers", workerShutdown, func(context.Context) { a.workers.Wait() })
	if a.queue != nil {
		a.step(ctx, "queue", workerShutdown, func(context.Context) { a.queue.Close() })
	}
	if a.webhook != nil {
		a.step(ctx, "webhook", webhookShutdown, func(ctx context.Context) {
			if err := a.webhook.Close(ctx); err != nil {
				a.log.Warn("webhook queue not drained", "err", err)
			}
		})
	}
	if a.audit != nil {
		a.step(ctx, "audit", quickShutdown, func(context.Context) { a.audit.Close() })
	}
	if a.messages != nil {
		a.step(ctx, "store", quickShutdown, func(context.Context) { a.messages.Close() })
	}
	if a.container != nil {
		a.step(ctx, "container", quickShutdown, func(context.Context) {
			if err := a.container.Close(); err != nil {
				a.log.Warn("failed to close the session database", "err", err)
			}
		})
	}
}

// step runs one shutdown step with a context derived from ctx that ends
// after limit, and stops waiting for it then.
func (a *app) step(ctx context.Context, name string, limit time.Duration, fn func(ctx context.Context)) {
	if a.hooks.step != nil {
		a.hooks.step(name)
	}
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(ctx)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		a.log.Warn("shutdown step did not finish in time", "step", name, "limit", limit)
	}
}
