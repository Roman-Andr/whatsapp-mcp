// Package render builds the output text of store rows: message lines,
// chat and contact maps, and the refusal maps, exactly as clients have
// always seen them.
//
// It reads through internal/store and formats with internal/pyfmt; it
// does not talk to the bridge or know about MCP.
package render
