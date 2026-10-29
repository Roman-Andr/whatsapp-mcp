// Package store reads the bridge's store read-only: messages.db (messages,
// chats, labels, reactions, polls, events, bridge_meta) and whatsapp.db
// (contacts and the LID map).
//
// A Pool owned by the server keeps one connection per database file and
// reopens it when the file is replaced. A DB is one account's store,
// limited by the read allowlists (Scope and the bridge's own list), which
// are read on every query. It never writes and never talks to the bridge.
package store
