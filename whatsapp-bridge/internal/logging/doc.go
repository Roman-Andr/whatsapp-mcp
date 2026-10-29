// Package logging builds the bridge's *slog.Logger and adapts it to the
// waLog.Logger interface whatsmeow expects.
//
// It owns the log format only. It must not import other internal packages,
// and it is not for the terminal UI (QR code, banners), which stays on
// stdout.
package logging
