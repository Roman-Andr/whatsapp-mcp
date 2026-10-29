// Package audit writes store/audit.log: one JSON line per state-changing API
// call, so the owner can see afterwards exactly what an agent sent where.
//
// It owns the log file and nothing else. It must not import other internal
// packages; callers decide what is audited.
package audit
