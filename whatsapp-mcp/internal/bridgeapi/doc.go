// Package bridgeapi is the client of the bridge's REST API: every endpoint
// as a method, the mapping of answers and failures to the texts clients
// see, the SSE event stream and wait_for_events.
//
// It talks HTTP only: it does not read the store or apply the read
// allowlist itself (the event stream takes a filter for that).
package bridgeapi
