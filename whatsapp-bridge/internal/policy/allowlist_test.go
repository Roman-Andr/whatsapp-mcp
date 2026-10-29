package policy

import (
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestAllowlistParsesAndMatches(t *testing.T) {
	t.Parallel()
	a, err := NewAllowlist([]string{"+79966764464", "120363431597928274@g.us", "555:3@s.whatsapp.net"})
	if err != nil {
		t.Fatal(err)
	}
	own := types.NewJID("79966764464", types.DefaultUserServer)
	group := types.NewJID("120363431597928274", types.GroupServer)
	other := types.NewJID("111", types.DefaultUserServer)
	lid := types.NewJID("32547396399312", types.HiddenUserServer)
	empty, _ := NewAllowlist(nil)
	var none *Allowlist
	cases := []struct {
		name    string
		list    *Allowlist
		chat    types.JID
		aliases []types.JID
		want    bool
	}{
		{"listed phone number", a, own, nil, true},
		{"listed group", a, group, nil, true},
		{"device suffix normalised", a, types.NewADJID("555", 0, 3), nil, true},
		{"unlisted number", a, other, nil, false},
		{"unlisted LID", a, lid, nil, false},
		{"LID allowed through its phone-number alias", a, lid, []types.JID{own}, true},
		{"empty list allows everything", empty, other, nil, true},
		{"nil list allows everything", none, other, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := c.list.Allows(c.chat, c.aliases...); got != c.want {
				t.Errorf("Allows = %v, want %v", got, c.want)
			}
		})
	}
	t.Run("restricted only with entries", func(t *testing.T) {
		t.Parallel()
		if !a.Restricted() || empty.Restricted() || none.Restricted() {
			t.Error("Restricted wrong")
		}
	})
}

func TestNewAllowlistRejectsInvalidEntries(t *testing.T) {
	t.Parallel()
	_, err := NewAllowlist([]string{"79990000002", "John"})
	if err == nil || err.Error() != `"John": recipient "John" is neither a JID nor a phone number` {
		t.Errorf("error %v", err)
	}
}

func TestAllowlistEntriesAreSortedAndNormalised(t *testing.T) {
	t.Parallel()
	a, err := NewAllowlist([]string{"79990000003", " +79990000002 ", "79990000002:5@s.whatsapp.net"})
	if err != nil {
		t.Fatal(err)
	}
	got := a.Entries()
	if len(got) != 2 || got[0] != "79990000002@s.whatsapp.net" || got[1] != "79990000003@s.whatsapp.net" {
		t.Errorf("entries %q", got)
	}
}
