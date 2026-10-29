package wa

import (
	"fmt"
	"sync"
	"time"
)

// Session states reported by /api/status and "session" events.
const (
	SessionStarting       = "starting"
	SessionQRRequired     = "qr_required"
	SessionConnected      = "connected"
	SessionDisconnected   = "disconnected" // whatsmeow reconnects on its own
	SessionLoggedOut      = "logged_out"
	SessionStreamReplaced = "stream_replaced"
	SessionConnectFailure = "connect_failure"
	SessionTemporaryBan   = "temporary_ban"
	SessionClientOutdated = "client_outdated"
)

// SessionStatus is the state of the WhatsApp session. Without it a lost
// session looks exactly like a quiet day: no messages arrive and nothing says
// why.
type SessionStatus struct {
	State  string    `json:"state"`
	Reason string    `json:"reason,omitempty"`
	Since  time.Time `json:"since"`
	// NeedsAction is set for states the bridge does not recover from on its
	// own: someone has to restart it, scan a QR code or update it.
	NeedsAction bool `json:"needs_action"`
}

// SessionTracker remembers the current session state.
type SessionTracker struct {
	mu     sync.Mutex
	status SessionStatus
}

// NewSessionTracker starts in SessionStarting.
func NewSessionTracker() *SessionTracker {
	return &SessionTracker{status: SessionStatus{State: SessionStarting, Since: time.Now()}}
}

// Set records a state and reports whether it differs from the previous one.
func (t *SessionTracker) Set(state, reason string, needsAction bool) (SessionStatus, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.status.State == state && t.status.Reason == reason {
		return t.status, false
	}
	t.status = SessionStatus{State: state, Reason: reason, Since: time.Now(), NeedsAction: needsAction}
	return t.status, true
}

// Get returns the current state; a nil tracker is starting.
func (t *SessionTracker) Get() SessionStatus {
	if t == nil {
		return SessionStatus{State: SessionStarting}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

// Reasons shown to whoever has to fix the session.
const (
	ReasonLoggedOut      = "the device was logged out (removed from Linked devices or expired); restart the bridge and scan the QR code"
	ReasonStreamReplaced = "another client connected with this session (is a second bridge running?); stop it and restart this bridge"
	ReasonQRRequired     = "not linked; scan the QR code printed by the bridge (WhatsApp > Settings > Linked devices)"
	ReasonClientOutdated = "WhatsApp rejected this client version; update whatsmeow (go get go.mau.fi/whatsmeow@latest) and rebuild"
)

// LoggedOutReason is ReasonLoggedOut, with WhatsApp's code when the logout
// came while connecting.
func LoggedOutReason(code fmt.Stringer, onConnect bool) string {
	if onConnect {
		return fmt.Sprintf("%s (%s)", ReasonLoggedOut, code)
	}
	return ReasonLoggedOut
}
