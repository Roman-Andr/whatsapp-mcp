// Package watest is a fake wa.Client for tests: Client embeds the
// interface and implements the calls the bridge's tests reach, with
// in-memory LID and contact stores and a record of what was sent.
//
// It owns no real connection and must never import whatsmeow's network
// code paths; it is imported only by _test.go files.
package watest
