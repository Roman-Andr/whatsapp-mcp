package wa

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"rsc.io/qr"
)

// Pairing is what /api/qr reports while the bridge is not linked: the
// current QR code, saved as a PNG image for a bridge running headless
// (systemd, a server) where the terminal QR code is not in front of anyone,
// and the pairing code when linking by phone number.
type Pairing struct {
	pngPath string

	mu          sync.Mutex
	qrCode      string
	pairingCode string
	pairingErr  string
	updated     time.Time
}

// qrScale is the size of one QR module in pixels: large enough to scan off
// a screen.
const qrScale = 8

// NewPairing saves QR images at pngPath.
func NewPairing(pngPath string) *Pairing {
	return &Pairing{pngPath: pngPath}
}

// PNGPath is where the QR image is saved.
func (p *Pairing) PNGPath() string { return p.pngPath }

// PairingInfo is the answer of GET /api/qr.
type PairingInfo struct {
	Linked      bool      `json:"linked"`
	QRCode      string    `json:"qr_code,omitempty"` // the text encoded in the QR code
	QRImage     string    `json:"qr_image,omitempty"`
	PairingCode string    `json:"pairing_code,omitempty"` // enter on the phone: Linked devices > Link with phone number
	PairingErr  string    `json:"pairing_error,omitempty"`
	Updated     time.Time `json:"updated,omitzero"`
}

// SetQR records a new QR code and saves it as an image.
func (p *Pairing) SetQR(code string) error {
	p.mu.Lock()
	p.qrCode = code
	p.updated = time.Now()
	p.mu.Unlock()
	img, err := qr.Encode(code, qr.L)
	if err != nil {
		return err
	}
	img.Scale = qrScale
	return os.WriteFile(p.pngPath, img.PNG(), 0600)
}

// SetPairingCode records the answer to a pairing-code request.
func (p *Pairing) SetPairingCode(code string, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pairingCode = code
	p.pairingErr = ""
	if err != nil {
		p.pairingErr = err.Error()
	}
	p.updated = time.Now()
}

// Done forgets the codes once pairing ended and removes the QR image. The
// error is the image's removal; the codes are forgotten either way.
func (p *Pairing) Done() error {
	p.mu.Lock()
	p.qrCode, p.pairingCode, p.pairingErr = "", "", ""
	p.mu.Unlock()
	if err := os.Remove(p.pngPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", p.pngPath, err)
	}
	return nil
}

// Info is the pairing state; linked hides the codes. A nil Pairing reports
// only linked.
func (p *Pairing) Info(linked bool) PairingInfo {
	if p == nil || linked {
		return PairingInfo{Linked: linked}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	info := PairingInfo{QRCode: p.qrCode, PairingCode: p.pairingCode, PairingErr: p.pairingErr, Updated: p.updated}
	if p.qrCode != "" {
		info.QRImage = p.pngPath
	}
	return info
}
