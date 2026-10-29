// Package tools defines the MCP tools: their table (names, descriptions,
// parameters, annotations, output kinds), the pydantic-style schemas and
// argument coercion, the handlers and how their values become results.
//
// A handler gets a *Call with the account it runs against and that
// account's store, bridge client and media toolkit; nothing is global.
package tools
