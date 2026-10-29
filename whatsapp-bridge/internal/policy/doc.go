// Package policy holds the bridge's security rules: the send and read
// allowlists, the send rate limit, the kinds of state-changing routes the
// read-only mode and the rate limit tell apart, and the media roots files
// may be sent from.
//
// It decides; it does not enforce. Callers (HTTP guard, services) ask it and
// answer the client. It must import nothing internal except jid, and it
// writes to the store only through the one-method MetaWriter.
package policy
