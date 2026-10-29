package policy

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
)

// ErrNotAllowed marks a chat refused by the send allowlist (HTTP 403). The
// refusal's own text says why; match it with errors.Is.
var ErrNotAllowed = errors.New("not in the send allowlist")

// notAllowedError is a chat refused by the send allowlist. Its text is the
// response.
type notAllowedError struct{ msg string }

func (e *notAllowedError) Error() string        { return e.msg }
func (e *notAllowedError) Is(target error) bool { return target == ErrNotAllowed }

// NotAllowed returns a refusal by the send allowlist that reads msg and
// matches ErrNotAllowed.
func NotAllowed(msg string) error { return &notAllowedError{msg} }

// Allowlist is a set of chats. Two are used:
//
//   - the send allowlist limits the chats the bridge may change anything in:
//     send, react, edit, delete, vote, mark read, show typing, manage;
//   - the read allowlist limits the chats whose messages, media, history and
//     events the bridge and the MCP server hand out.
//
// They are enforced in the bridge and not only in the MCP server, so a
// prompt-injected agent (or any other local client holding the token)
// cannot reach chats outside them.
//
// An empty list means unrestricted; so does a nil one.
type Allowlist struct {
	jids map[string]bool
}

// NewAllowlist parses entries (JIDs or phone numbers). An entry that cannot
// be parsed is an error: a typo must not silently turn the restriction off.
func NewAllowlist(entries []string) (*Allowlist, error) {
	a := &Allowlist{jids: make(map[string]bool)}
	for _, entry := range entries {
		parsed, err := jid.ParseRecipient(strings.TrimSpace(entry))
		if err != nil {
			return nil, fmt.Errorf("%q: %w", entry, err)
		}
		a.jids[parsed.ToNonAD().String()] = true
	}
	return a, nil
}

// Restricted reports whether the list limits anything.
func (a *Allowlist) Restricted() bool {
	return a != nil && len(a.jids) > 0
}

// Allows reports whether chat, or one of the other JIDs the same chat is
// known by, is on the list.
func (a *Allowlist) Allows(chat types.JID, aliases ...types.JID) bool {
	if !a.Restricted() {
		return true
	}
	for _, j := range append([]types.JID{chat}, aliases...) {
		if !j.IsEmpty() && a.jids[j.ToNonAD().String()] {
			return true
		}
	}
	return false
}

// Entries lists the allowed chats, sorted.
func (a *Allowlist) Entries() []string {
	if a == nil {
		return nil
	}
	out := make([]string, 0, len(a.jids))
	for j := range a.jids {
		out = append(out, j)
	}
	slices.Sort(out)
	return out
}
