// Package wa is the bridge's seam to WhatsApp: the Client interface listing
// exactly the whatsmeow calls the bridge makes, the adapter that turns a
// *whatsmeow.Client into one, and the session state around it (the Session
// tracker, the Pairing state /api/qr reports, the Reconnector and the device
// properties applied before pairing).
//
// The client's lifecycle (Connect, Disconnect, GetQRChannel, PairPhone,
// AddEventHandler) is deliberately not on the interface: only the wiring in
// internal/app uses it, on the concrete client. Everything else in the bridge
// talks to WhatsApp through Client, so it can be faked (package watest). The
// package must not import internal packages other than store, content and
// jid, and it never publishes events or logs.
package wa
