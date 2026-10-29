// Package pyfmt renders values exactly as the former Python server did:
// ordered JSON objects (OMap), pydantic-style indented JSON, Python's
// str/repr/float forms, datetime formats and fromisoformat parsing, and
// os.path's user and absolute paths.
//
// It is part of the output contract that the golden transcripts pin, so
// its logic changes only together with them. It imports the standard
// library only and knows nothing of the store, the bridge or MCP.
package pyfmt
