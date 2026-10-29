package policy

import (
	"sync"
	"time"
)

// RateLimiter is a token bucket shared by everything that reaches other
// people. A prompt-injected or looping agent otherwise sends as fast as the
// API answers, and WhatsApp bans accounts that do.
//
// A nil limiter allows everything.
type RateLimiter struct {
	// Now is the clock; tests and the contract harness freeze it. Set it
	// before the limiter is shared.
	Now func() time.Time

	mu     sync.Mutex
	rate   float64 // tokens per second
	burst  float64
	tokens float64
	last   time.Time
}

// NewRateLimiter allows perMinute calls per minute after an initial burst.
// perMinute <= 0 disables the limit and returns nil.
func NewRateLimiter(perMinute, burst int) *RateLimiter {
	if perMinute <= 0 {
		return nil
	}
	burst = max(burst, 1)
	return &RateLimiter{rate: float64(perMinute) / 60, burst: float64(burst), tokens: float64(burst), Now: time.Now}
}

// Limits returns the refill rate (tokens per second) and the burst, for
// start-up messages and tests. A nil limiter has none.
func (l *RateLimiter) Limits() (perSecond, burst float64) {
	if l == nil {
		return 0, 0
	}
	return l.rate, l.burst
}

// Take uses one token, or reports how long until one is available.
func (l *RateLimiter) Take() (bool, time.Duration) {
	if l == nil {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	if !l.last.IsZero() {
		l.tokens = min(l.burst, l.tokens+now.Sub(l.last).Seconds()*l.rate)
	}
	l.last = now
	if l.tokens >= 1 {
		l.tokens--
		return true, 0
	}
	wait := time.Duration((1 - l.tokens) / l.rate * float64(time.Second))
	return false, wait.Round(time.Second) + time.Second
}
