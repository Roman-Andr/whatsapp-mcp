package app

import (
	"fmt"
	"io"
	"strings"

	"github.com/mdp/qrterminal"
)

// console is the bridge's terminal UI on stdout: start-up notes, the QR
// code, the pairing code and the connection banners. It is not a log: the
// texts are what a person at the terminal reads, and they stay as they are.
type console struct {
	w io.Writer
}

func (c console) sendAllowlist(entries []string) {
	fmt.Fprintf(c.w, "Send allowlist active: %s\n", strings.Join(entries, ", "))
}

func (c console) readAllowlist(entries []string) {
	fmt.Fprintf(c.w, "Read allowlist active: %s\n", strings.Join(entries, ", "))
}

func (c console) readOnly() {
	fmt.Fprintln(c.w, "Read-only mode: sending and every other change is refused")
}

func (c console) welcomes(groups int) {
	fmt.Fprintf(c.w, "Welcoming new members in %d group(s)\n", groups)
}

func (c console) mediaRoots(dirs []string) {
	fmt.Fprintf(c.w, "Files may be sent from: %s\n", strings.Join(dirs, ":"))
}

func (c console) webhookDisabled() {
	fmt.Fprintln(c.w, "Webhook disabled by WHATSAPP_WEBHOOK_DISABLED")
}

func (c console) autoDownload(maxVideoMB, maxFileMB uint64) {
	fmt.Fprintf(c.w, "Downloading new media automatically (videos up to %d MB, other files up to %d MB)\n", maxVideoMB, maxFileMB)
}

func (c console) historyDays(days int) {
	fmt.Fprintf(c.w, "Requesting %d days of history from the phone\n", days)
}

func (c console) deviceName(name string) {
	fmt.Fprintf(c.w, "Linking as %q\n", name)
}

func (c console) qrCode(code, pngPath string) {
	fmt.Fprintln(c.w, "\nScan this QR code with your WhatsApp app (also saved as "+pngPath+"):")
	qrterminal.GenerateHalfBlock(code, qrterminal.L, c.w)
}

func (c console) pairingCode(code string) {
	fmt.Fprintf(c.w, "\nOr link with phone number: on the phone open WhatsApp > Linked devices > Link with phone number and enter %s\n", code)
}

func (c console) paired() {
	fmt.Fprintln(c.w, "\nSuccessfully connected and authenticated!")
}

func (c console) connected() {
	fmt.Fprintln(c.w, "\n✓ Connected to WhatsApp!")
	fmt.Fprintln(c.w, "REST server is running. Press Ctrl+C to disconnect and exit.")
}

func (c console) loggedOut() {
	fmt.Fprintln(c.w, "Logged out; exiting so the bridge can pair again")
}

func (c console) disconnecting() {
	fmt.Fprintln(c.w, "Disconnecting...")
}
