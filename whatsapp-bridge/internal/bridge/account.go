package bridge

import (
	"context"
	"errors"
	"os"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// Status is the answer of GET /api/status.
func (b *Bridge) Status() map[string]any {
	status := map[string]any{
		"connected":        b.client != nil && b.client.IsConnected(),
		"full_text_search": b.store.FullTextSearch(),
		"session":          b.session.Get(),
		// Empty when sending is unrestricted.
		"send_allowlist": b.allowlist.Entries(),
		// Empty when every chat is readable.
		"read_allowlist": b.readAllowlist.Entries(),
		"read_only":      b.readOnly,
		"pairing":        b.pairing.Info(b.linked()),
	}
	if b.linked() {
		status["jid"] = b.client.OwnID().ToNonAD().String()
	}
	if b.reconnector != nil {
		if last := b.reconnector.LastAt(); !last.IsZero() {
			status["last_reconnect"] = last
		}
	}
	return status
}

// linked reports whether the device is paired with an account.
func (b *Bridge) linked() bool {
	return b.client != nil && b.client.OwnID() != nil
}

// Pairing is the answer of GET /api/qr.
func (b *Bridge) Pairing() PairingInfo {
	return b.pairing.Info(b.linked())
}

// QRImage is the current QR code as a PNG image.
func (b *Bridge) QRImage() ([]byte, error) {
	return os.ReadFile(b.pairing.PNGPath())
}

// Logout unlinks this device from the account. On success the caller
// answers and then calls RequestRestart: without the device store the only
// way to pair again is a fresh start.
func (b *Bridge) Logout(ctx context.Context) SendMessageResponse {
	if !b.linked() {
		return failed("Not linked")
	}
	if err := b.client.Logout(ctx); err != nil {
		return failed("Logout failed: %v", err)
	}
	b.SetSession(wa.SessionLoggedOut, "logged out through /api/logout; the bridge restarts to pair again", true)
	return SendMessageResponse{Success: true, Message: "Logged out; the bridge exits so it can be restarted and paired again"}
}

// RequestRestart asks run() to exit so the service manager restarts the
// bridge.
func (b *Bridge) RequestRestart() {
	if b.restart != nil {
		select {
		case b.restart <- struct{}{}:
		default:
		}
	}
}

// ErrReconnectUnavailable is Reconnect without a reconnector.
var ErrReconnectUnavailable = errors.New("reconnecting is not available")

// Reconnect drops and reopens the WhatsApp connection; see wa.Reconnector
// for the errors it throttles with.
func (b *Bridge) Reconnect() error {
	if b.reconnector == nil {
		return ErrReconnectUnavailable
	}
	return b.reconnector.Reconnect()
}
