package jid

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestParseRecipient(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in, want string
		ok             bool
	}{
		{"phone number with plus", "+79161234567", "79161234567@s.whatsapp.net", true},
		{"group JID", "120363431597928274@g.us", "120363431597928274@g.us", true},
		{"name", "John", "", false},
		{"empty", "  ", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseRecipient(c.in)
			if (err == nil) != c.ok || (c.ok && got.String() != c.want) {
				t.Errorf("ParseRecipient(%q) = %v, %v", c.in, got, err)
			}
		})
	}
}

func TestParseGroup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, in string
		ok       bool
	}{
		{"group", "120363000000000001@g.us", true},
		{"direct chat", "79990000002@s.whatsapp.net", false},
		{"garbage", "@@@", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseGroup(c.in)
			if (err == nil) != c.ok || (c.ok && got.Server != types.GroupServer) {
				t.Errorf("ParseGroup(%q) = %v, %v", c.in, got, err)
			}
		})
	}
	if _, err := ParseGroup("79990000002@s.whatsapp.net"); err == nil || err.Error() != `"79990000002@s.whatsapp.net" is not a group JID (…@g.us)` {
		t.Errorf("error text %v", err)
	}
}

func TestString(t *testing.T) {
	t.Parallel()
	if got := String(types.EmptyJID); got != "" {
		t.Errorf("empty JID = %q", got)
	}
	if got := String(types.NewADJID("555", 0, 3)); got != "555@s.whatsapp.net" {
		t.Errorf("device JID = %q", got)
	}
}
