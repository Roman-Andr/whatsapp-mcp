package events

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// WebhookTimeout bounds one delivery, so a hanging receiver delays the queue
// instead of stopping it.
const WebhookTimeout = 15 * time.Second

// webhookQueue is how many events may wait for delivery; more are dropped.
const webhookQueue = 256

// Webhook POSTs events to a URL, signed with a secret. Deliveries go through
// a bounded queue drained by one goroutine: a slow receiver delays
// deliveries but never blocks message handling.
type Webhook struct {
	url    string
	secret string
	client *http.Client
	log    *slog.Logger

	mu     sync.Mutex // guards closed and sending on queue
	closed bool
	queue  chan Event
	done   chan struct{} // closed when run returns
	// cancel ends the delivery in progress when Close gives up waiting.
	cancel context.CancelFunc
}

// NewWebhook starts delivering to url. log, which may be nil, receives
// failed and dropped deliveries.
func NewWebhook(url, secret string, log *slog.Logger) *Webhook {
	return newWebhook(url, secret, WebhookTimeout, log)
}

func newWebhook(url, secret string, timeout time.Duration, log *slog.Logger) *Webhook {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	w := &Webhook{
		url:    url,
		secret: secret,
		// Never follow redirects: a receiver that answers 3xx would
		// otherwise pass the signed events on to wherever it points.
		client: &http.Client{
			Timeout: timeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		log:   log,
		queue: make(chan Event, webhookQueue),
		done:  make(chan struct{}),
	}
	// Deliveries outlive the event that caused them; only Close ends them.
	ctx, cancel := context.WithCancel(context.Background())
	w.cancel = cancel
	go w.run(ctx)
	return w
}

// run delivers the queue until Close closes it. Once ctx is cancelled the
// remaining events are dropped.
func (w *Webhook) run(ctx context.Context) {
	defer close(w.done)
	for evt := range w.queue {
		if ctx.Err() != nil {
			continue
		}
		if err := w.Deliver(ctx, evt); err != nil {
			w.log.Warn("webhook delivery failed", "type", evt.Type, "err", err)
		}
	}
}

// enqueue queues evt without blocking. A nil or closed webhook ignores it.
func (w *Webhook) enqueue(evt Event) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	select {
	case w.queue <- evt:
	default:
		w.log.Warn("webhook queue full, dropping event", "type", evt.Type)
	}
}

// Close stops accepting events and waits until the queued ones are
// delivered, or until ctx is done. Then it cancels the delivery in progress,
// drops what is still queued and returns ctx's error once the worker has
// stopped. Events published after Close are dropped.
func (w *Webhook) Close(ctx context.Context) error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	if !w.closed {
		w.closed = true
		close(w.queue)
	}
	w.mu.Unlock()
	select {
	case <-w.done:
		w.cancel()
		return nil
	case <-ctx.Done():
		w.cancel()
		<-w.done
		return ctx.Err()
	}
}

// Deliver POSTs one event now and reports whether the receiver accepted it.
func (w *Webhook) Deliver(ctx context.Context, evt Event) error {
	body, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Bridge-Timestamp", timestamp)
	req.Header.Set("X-Bridge-Signature", Sign(w.secret, timestamp, body))
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}

// Sign is the X-Bridge-Signature of a delivery: HMAC-SHA256 over
// "<timestamp>.<body>", so a receiver can check both who sent it and that an
// old delivery is not being replayed.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
