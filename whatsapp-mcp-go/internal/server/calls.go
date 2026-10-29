package server

import (
	"context"
	"sync"
)

// callTracker cancels running tool calls: all of them at shutdown, or one
// session's when its client ends the session. The SDK closes a session only
// after its running calls return, and a wait_for_events call can run for
// ten minutes holding a connection to the bridge.
type callTracker struct {
	mu        sync.Mutex
	next      uint64
	bySession map[string]map[uint64]context.CancelFunc
	// closed is set by cancelAll: calls that start later are cancelled at
	// once.
	closed bool
}

func newCallTracker() *callTracker {
	return &callTracker{bySession: map[string]map[uint64]context.CancelFunc{}}
}

// Start returns the context a call of session runs with, and the function
// to call when it ends.
func (c *callTracker) Start(ctx context.Context, session string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		cancel()
		return ctx, func() {}
	}
	c.next++
	id := c.next
	if c.bySession[session] == nil {
		c.bySession[session] = map[uint64]context.CancelFunc{}
	}
	c.bySession[session][id] = cancel
	return ctx, func() {
		c.mu.Lock()
		delete(c.bySession[session], id)
		if len(c.bySession[session]) == 0 {
			delete(c.bySession, session)
		}
		c.mu.Unlock()
		cancel()
	}
}

// cancelSession cancels the running calls of one session.
func (c *callTracker) cancelSession(session string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, cancel := range c.bySession[session] {
		cancel()
	}
}

// cancelAll cancels every running call and every call that starts later.
func (c *callTracker) cancelAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for _, calls := range c.bySession {
		for _, cancel := range calls {
			cancel()
		}
	}
}
