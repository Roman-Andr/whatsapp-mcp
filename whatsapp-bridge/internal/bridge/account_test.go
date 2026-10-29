package bridge

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

func TestStatus(t *testing.T) {
	t.Parallel()
	at := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	reconnector := wa.NewReconnector(func() bool { return true }, func() error { return nil })
	reconnector.Now = func() time.Time { return at }
	b := newOnlineBridge(t, func(d *Deps) { d.Reconnector = reconnector })
	if err := b.Reconnect(); err != nil {
		t.Fatal(err)
	}
	status := b.Status()
	if status["connected"] != true || status["jid"] != "1000@s.whatsapp.net" || status["last_reconnect"] != at {
		t.Errorf("status %+v", status)
	}
	if p := status["pairing"].(PairingInfo); !p.Linked {
		t.Errorf("pairing %+v", p)
	}

	offline := newTestBridge(t).Status()
	if _, ok := offline["jid"]; ok || offline["connected"] != false {
		t.Errorf("offline status %+v", offline)
	}
	if _, ok := offline["last_reconnect"]; ok {
		t.Error("last_reconnect without a reconnect")
	}
}

func TestPairingAndQRImage(t *testing.T) {
	t.Parallel()
	b := newTestBridge(t)
	if err := b.pairing.SetQR("2@abc"); err != nil {
		t.Fatal(err)
	}
	if p := b.Pairing(); p.Linked || p.QRCode != "2@abc" {
		t.Errorf("pairing %+v", p)
	}
	img, err := b.QRImage()
	if err != nil || len(img) == 0 {
		t.Errorf("image %d bytes, %v", len(img), err)
	}
	if err := b.pairing.Done(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.QRImage(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("image after pairing: %v", err)
	}
}

func TestLogout(t *testing.T) {
	t.Parallel()
	t.Run("linked", func(t *testing.T) {
		t.Parallel()
		b := newOnlineBridge(t)
		resp := b.Logout(t.Context())
		if !resp.Success || b.session.Get().State != wa.SessionLoggedOut {
			t.Errorf("%+v, session %+v", resp, b.session.Get())
		}
		b.RequestRestart()
		b.RequestRestart() // a pending request is enough
		select {
		case <-b.restart:
		default:
			t.Error("no restart requested")
		}
	})
	t.Run("not linked", func(t *testing.T) {
		t.Parallel()
		if resp := newTestBridge(t).Logout(t.Context()); resp.Success || resp.Message != "Not linked" {
			t.Errorf("%+v", resp)
		}
	})
	t.Run("without a restart channel", func(t *testing.T) {
		t.Parallel()
		New(Deps{}).RequestRestart() // must not block or panic
	})
}

func TestReconnectUnavailable(t *testing.T) {
	t.Parallel()
	if err := newTestBridge(t).Reconnect(); !errors.Is(err, ErrReconnectUnavailable) {
		t.Errorf("err %v", err)
	}
}
