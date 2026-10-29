// Package logging builds the server's *slog.Logger: one plain line per
// record, "message key=value ...", for journald, which adds its own time
// stamps.
//
// It owns the log line format and nothing else; every other package gets
// the logger passed in and never uses slog's default logger.
package logging
