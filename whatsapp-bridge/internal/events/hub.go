package events

import "sync"

// subscriberBuffer is how many events a subscriber may fall behind before it
// misses some.
const subscriberBuffer = 64

// Hub fans events out to subscribers and an optional webhook.
type Hub struct {
	mu          sync.Mutex
	subscribers map[chan Event]struct{}
	webhook     *Webhook
}

// NewHub returns a hub that also hands every event to webhook, unless it is
// nil.
func NewHub(webhook *Webhook) *Hub {
	return &Hub{subscribers: make(map[chan Event]struct{}), webhook: webhook}
}

// Subscribe returns a channel that receives every event published from now
// on, until Unsubscribe.
func (h *Hub) Subscribe() chan Event {
	ch := make(chan Event, subscriberBuffer)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe stops delivering to ch. ch is not closed: Publish may still be
// holding it.
func (h *Hub) Unsubscribe(ch chan Event) {
	h.mu.Lock()
	delete(h.subscribers, ch)
	h.mu.Unlock()
}

// Publish never blocks: a subscriber that cannot keep up misses events, and
// so does a webhook whose queue is full.
func (h *Hub) Publish(evt Event) {
	h.mu.Lock()
	for ch := range h.subscribers {
		select {
		case ch <- evt:
		default:
		}
	}
	h.mu.Unlock()
	h.webhook.enqueue(evt)
}
