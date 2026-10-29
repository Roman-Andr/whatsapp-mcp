// Package bridge is the bridge service: it ingests what WhatsApp sends
// (HandleEvent stores messages, edits, reactions, votes, calls, group and
// chat state changes, and publishes them as events) and carries out the
// actions the API asks for (send, react, edit, manage groups, …).
//
// Every action applies the security rules itself: the send and read
// allowlists, read-only mode for call replies and welcomes, and the media
// roots. The request and response types in types.go are also the REST API's
// JSON; a tag change there is an API change. The package talks to WhatsApp
// only through wa.Client, knows nothing about HTTP routing, and imports
// nothing internal except wa, media, store, content, events, audit, policy
// and jid.
package bridge
