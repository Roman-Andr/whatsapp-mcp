// Package export writes a chat's history to a file (plain text, JSON or
// HTML, optionally with its media in a ZIP archive): the export_chat tool
// and the `whatsapp-mcp export` subcommand.
//
// It reads through internal/store and downloads media with the function it
// is given; it does not know about MCP.
package export
