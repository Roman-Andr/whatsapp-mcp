package wa

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPairing(t *testing.T) {
	t.Parallel()
	png := filepath.Join(t.TempDir(), "qr.png")
	p := NewPairing(png)
	if p.PNGPath() != png {
		t.Errorf("PNGPath %s", p.PNGPath())
	}
	if err := p.SetQR("2@abc,def,ghi"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(png)
	if err != nil || !bytes.HasPrefix(data, []byte("\x89PNG")) {
		t.Fatalf("QR image %d bytes, %v", len(data), err)
	}
	if st, _ := os.Stat(png); st.Mode().Perm() != 0600 {
		t.Errorf("QR image mode %v", st.Mode().Perm())
	}
	p.SetPairingCode("", errors.New("rate limited"))
	p.SetPairingCode("ABCD-EFGH", nil)
	info := p.Info(false)
	if info.QRCode != "2@abc,def,ghi" || info.QRImage != png || info.PairingCode != "ABCD-EFGH" || info.PairingErr != "" || info.Updated.IsZero() {
		t.Errorf("info %+v", info)
	}
	if linked := p.Info(true); linked != (PairingInfo{Linked: true}) {
		t.Errorf("linked info shows codes: %+v", linked)
	}

	if err := p.Done(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(png); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("QR image left behind: %v", err)
	}
	if info := p.Info(false); info.QRCode != "" || info.QRImage != "" || info.PairingCode != "" {
		t.Errorf("codes kept after Done: %+v", info)
	}
	if err := p.Done(); err != nil {
		t.Errorf("second Done: %v", err)
	}
}

func TestPairingErrors(t *testing.T) {
	t.Parallel()
	t.Run("pairing code error", func(t *testing.T) {
		t.Parallel()
		p := NewPairing(filepath.Join(t.TempDir(), "qr.png"))
		p.SetPairingCode("", errors.New("rate limited"))
		if got := p.Info(false).PairingErr; got != "rate limited" {
			t.Errorf("error %q", got)
		}
	})
	t.Run("image not writable", func(t *testing.T) {
		t.Parallel()
		p := NewPairing(filepath.Join(t.TempDir(), "missing", "qr.png"))
		if err := p.SetQR("code"); err == nil {
			t.Error("no error")
		}
		if p.Info(false).QRCode != "code" {
			t.Error("the code is lost with the image")
		}
	})
	t.Run("image not removable", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "qr.png")
		if err := os.MkdirAll(filepath.Join(dir, "x"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := NewPairing(dir).Done(); err == nil {
			t.Error("no error")
		}
	})
	t.Run("nil pairing", func(t *testing.T) {
		t.Parallel()
		var p *Pairing
		if p.Info(false) != (PairingInfo{}) {
			t.Error("nil pairing reports codes")
		}
	})
}
