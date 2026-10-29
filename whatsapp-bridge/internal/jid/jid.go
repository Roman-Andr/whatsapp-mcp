package jid

import (
	"errors"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

// ParseRecipient accepts a JID or a bare phone number.
func ParseRecipient(recipient string) (types.JID, error) {
	if strings.Contains(recipient, "@") {
		return types.ParseJID(recipient)
	}
	number := strings.TrimPrefix(strings.TrimSpace(recipient), "+")
	if number == "" {
		return types.EmptyJID, errors.New("empty recipient")
	}
	for _, r := range number {
		if r < '0' || r > '9' {
			return types.EmptyJID, fmt.Errorf("recipient %q is neither a JID nor a phone number", recipient)
		}
	}
	return types.NewJID(number, types.DefaultUserServer), nil
}

// ParseGroup parses a group JID.
func ParseGroup(chatJID string) (types.JID, error) {
	jid, err := types.ParseJID(chatJID)
	if err != nil || jid.Server != types.GroupServer {
		return types.EmptyJID, fmt.Errorf("%q is not a group JID (…@g.us)", chatJID)
	}
	return jid, nil
}

// String is the device-less form of jid, or "" for the empty JID.
func String(jid types.JID) string {
	if jid.IsEmpty() {
		return ""
	}
	return jid.ToNonAD().String()
}
