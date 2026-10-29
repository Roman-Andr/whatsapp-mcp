// Package server wires the MCP server together: it owns the store pool,
// the bridge HTTP client and the call tracker, registers the tools and
// resources, and serves them over stdio or streamable HTTP (token guard,
// DELETE cancelling a session's calls, session timeout, quick shutdown).
//
// Only main uses it.
package server
