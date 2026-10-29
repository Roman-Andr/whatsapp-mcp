package wa

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// MinReconnectInterval is the shortest time between two reconnects asked
// for through the API. A looping agent would otherwise open connection after
// connection, which WhatsApp treats as abuse.
const MinReconnectInterval = 30 * time.Second

// Reconnector drops and reopens the WhatsApp connection on request.
type Reconnector struct {
	// mu serialises reconnects: a second request waits for the running one
	// and is then throttled by its time stamp.
	mu sync.Mutex
	// last is when a reconnect last started. It is read without mu, so
	// /api/status never waits for a reconnect in progress.
	last     atomic.Pointer[time.Time]
	interval time.Duration
	// Now is the clock; tests replace it.
	Now    func() time.Time
	linked func() bool  // false until the device is paired
	cycle  func() error // disconnect, then connect again
}

// NewReconnector cycles the connection with cycle once linked reports a
// paired device.
func NewReconnector(linked func() bool, cycle func() error) *Reconnector {
	return &Reconnector{interval: MinReconnectInterval, Now: time.Now, linked: linked, cycle: cycle}
}

// ErrTooSoon matches a TooSoonError.
var ErrTooSoon = errors.New("reconnected too recently")

// TooSoonError carries how long to wait (HTTP 429).
type TooSoonError struct{ Wait time.Duration }

func (e *TooSoonError) Error() string {
	return fmt.Sprintf("reconnected less than %s ago; try again in %s", MinReconnectInterval, e.Wait.Round(time.Second))
}

// Is makes errors.Is(err, ErrTooSoon) match.
func (e *TooSoonError) Is(target error) bool { return target == ErrTooSoon }

// ErrNotLinked refuses a reconnect before the device is paired (HTTP 409).
var ErrNotLinked = errors.New("not linked to WhatsApp: pair the device first (GET /api/qr); there is no session to reconnect")

// Reconnect cycles the connection unless it was cycled within the interval.
// A failed attempt counts too, so retries are throttled the same way.
func (r *Reconnector) Reconnect() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.linked() {
		return ErrNotLinked
	}
	now := r.Now()
	if last := r.last.Load(); last != nil {
		if wait := last.Add(r.interval).Sub(now); wait > 0 {
			return &TooSoonError{wait}
		}
	}
	r.last.Store(&now)
	return r.cycle()
}

// LastAt is when the API last asked for a reconnect (zero: never). It does
// not wait for a reconnect in progress.
func (r *Reconnector) LastAt() time.Time {
	if last := r.last.Load(); last != nil {
		return *last
	}
	return time.Time{}
}
