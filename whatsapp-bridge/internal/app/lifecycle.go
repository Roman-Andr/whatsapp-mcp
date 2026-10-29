package app

import (
	"context"
	"time"

	"go.mau.fi/whatsmeow"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/api"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// apiDeps is what newBridge prepares for startAPI besides the bridge.
type apiDeps struct {
	token   string
	limiter *policy.RateLimiter
	events  *events.Hub
}

// startAPI starts the REST API. It starts before connecting, so
// /api/status can report a session that needs a QR scan instead of the
// bridge looking dead. It only listens on localhost; WHATSAPP_BRIDGE_ADDR
// changes the port.
func (a *app) startAPI(ctx context.Context) error {
	server, err := api.Start(ctx, a.cfg.Addr, api.Deps{
		Bridge:   a.bridge,
		Token:    a.api.token,
		ReadOnly: a.cfg.ReadOnly,
		Limiter:  a.api.limiter,
		Audit:    a.audit,
		Events:   a.api.events,
		Logger:   a.log,
	})
	if err != nil {
		return err
	}
	a.server = server
	return nil
}

// pair links a new device: by QR code, or by a code entered on the phone
// when WHATSAPP_PAIR_PHONE is set. done reports that the run ends here with
// code: 1 when pairing failed, 0 when ctx ended (a signal) first.
func (a *app) pair(ctx context.Context) (code int, done bool) {
	a.bridge.SetSession(wa.SessionQRRequired, wa.ReasonQRRequired, true)
	if a.cfg.HistoryDays > 0 {
		a.out.historyDays(a.cfg.HistoryDays)
	}
	if a.cfg.DeviceName != "" {
		a.out.deviceName(a.cfg.DeviceName)
	}
	qrChan, err := a.client.GetQRChannel(ctx)
	if err != nil {
		a.log.Error("failed to get the QR channel", "err", err)
		return 1, true
	}
	if err := a.client.Connect(); err != nil {
		a.log.Error("failed to connect", "err", err)
		return 1, true
	}
	paired, cancelled := a.watchQR(ctx, qrChan)
	if err := a.pairing.Done(); err != nil {
		a.log.Warn("failed to remove the QR image", "err", err)
	}
	switch {
	case cancelled:
		return 0, true
	case !paired:
		a.log.Error("pairing did not complete (QR code not scanned in time)")
		return 1, true
	}
	a.out.paired()
	return 0, false
}

// watchQR shows each QR code until the channel closes, and asks for a
// pairing code once the first QR code shows the websocket is up.
func (a *app) watchQR(ctx context.Context, qrChan <-chan whatsmeow.QRChannelItem) (paired, cancelled bool) {
	phone := a.cfg.PairPhone
	for {
		select {
		case <-ctx.Done():
			return false, true
		case evt, ok := <-qrChan:
			if !ok {
				return paired, false
			}
			switch evt.Event {
			case "code":
				a.out.qrCode(evt.Code, a.pairing.PNGPath())
				if err := a.pairing.SetQR(evt.Code); err != nil {
					a.log.Warn("failed to save the QR image", "err", err)
				}
				if phone != "" {
					number := phone
					a.workers.Go(func() { a.requestPairingCode(ctx, number) })
					phone = ""
				}
			case "success":
				paired = true
			default:
				a.log.Warn("pairing", "event", evt.Event)
			}
		}
	}
}

// requestPairingCode asks WhatsApp for a pairing code for phone. It must be
// called once the QR channel produced its first code, i.e. the websocket is
// up.
func (a *app) requestPairingCode(ctx context.Context, phone string) {
	code, err := a.client.PairPhone(ctx, phone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	a.pairing.SetPairingCode(code, err)
	if err != nil {
		a.log.Error("failed to get a pairing code", "phone", phone, "err", err)
		return
	}
	a.out.pairingCode(code)
}

// connect waits for the connection to hold. ok is false when the run ends
// with code: 1 when the connection dropped, 0 on a signal.
func (a *app) connect(ctx context.Context) (code int, ok bool) {
	select {
	case <-time.After(a.hooks.stable):
	case <-ctx.Done():
		return 0, false
	}
	if !a.client.IsConnected() {
		a.log.Error("failed to establish stable connection")
		return 1, false
	}
	a.out.connected()
	return 0, true
}

// wait runs until a signal (0) or a logout through the API (1).
func (a *app) wait(ctx context.Context) int {
	if a.hooks.ready != nil {
		a.hooks.ready(a.bridge)
	}
	select {
	case <-ctx.Done():
		a.out.disconnecting()
		return 0
	case <-a.restart:
		// Logged out through the API: exit non-zero so the service manager
		// starts a fresh bridge that shows a new QR code.
		a.out.loggedOut()
		return 1
	}
}
