// Package store is the bridge's message database, store/messages.db: chats,
// messages, reactions, polls and their votes, event responses, labels and
// the bridge_meta values the MCP server reads.
//
// It owns the schema (schema.go, created verbatim; migrate.go adds the later
// columns in a fixed order) and every SQL statement. All SQL names its
// columns, because live databases have upgraded, non-fresh column orders.
// Every method takes the caller's context. It must import nothing internal
// except content (for MediaInfo), and it never reaches the network.
package store
