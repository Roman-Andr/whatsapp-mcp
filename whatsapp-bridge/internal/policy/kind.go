package policy

// Kind is the kind of a state-changing route, which decides what the guard
// applies to it.
type Kind int

// Kinds of state-changing routes. All three are refused in read-only mode;
// only ReachesOthers counts against the rate limit. SignalOnly and
// OwnAccount are treated alike today; the difference documents why.
const (
	// ReachesOthers: messages, reactions, group changes.
	ReachesOthers Kind = iota
	// SignalOnly: read receipts and typing.
	SignalOnly
	// OwnAccount: archive, pin, labels, logout.
	OwnAccount
)
