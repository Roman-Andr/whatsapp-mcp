// Package app wires the bridge together and runs it: it opens the stores,
// builds the WhatsApp client and the bridge service, starts the REST API,
// pairs or connects, waits for a signal or a logout and shuts everything
// down in order. console.go is the terminal UI (QR code, banners), the only
// output on stdout.
//
// It owns the process lifecycle and every long-lived goroutine it starts.
// Only main imports it; it reads no environment (main hands it the loaded
// config) and holds no package-level state.
package app
