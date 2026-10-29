// Package api is the bridge's REST API: the routes under /api/, the token
// check in front of all of them, the guard (read-only mode, rate limit) and
// audit entries of state-changing routes, and the server-sent events stream.
//
// Handlers decode and validate requests, answer with the exact texts and
// status codes the clients depend on, and leave the work to the bridge
// service; they never talk to WhatsApp or the store themselves. Only main
// (later internal/app) starts the API.
package api
