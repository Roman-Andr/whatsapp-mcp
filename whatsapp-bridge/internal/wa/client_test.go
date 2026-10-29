package wa

import (
	"testing"

	"go.mau.fi/whatsmeow"
	wastore "go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

var (
	own = types.NewADJID("1000", 0, 3)
	lid = types.NewJID("9000", types.HiddenUserServer)
)

func TestNewNil(t *testing.T) {
	t.Parallel()
	if New(nil) != nil {
		t.Error("New(nil) is not a nil Client")
	}
}

func TestAdapterFollowsTheOwnJID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		device *wastore.Device
		events []any
		want   *types.JID
	}{
		{"linked at start", &wastore.Device{ID: &own}, nil, &own},
		{"not linked", &wastore.Device{}, nil, nil},
		{"paired", &wastore.Device{}, []any{&events.PairSuccess{ID: own}}, &own},
		{"logged out", &wastore.Device{ID: &own}, []any{&events.LoggedOut{}}, nil},
		{"other events", &wastore.Device{ID: &own}, []any{&events.Connected{}}, &own},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := New(whatsmeow.NewClient(c.device, nil)).(*adapter)
			for _, evt := range c.events {
				a.track(evt)
			}
			got := a.OwnID()
			if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
				t.Errorf("OwnID %v, want %v", got, c.want)
			}
			if got != nil {
				got.User = "changed"
				if a.OwnID().User == "changed" {
					t.Error("OwnID hands out the adapter's own copy")
				}
			}
		})
	}
}

func TestAdapterLogoutWithoutSession(t *testing.T) {
	t.Parallel()
	a := New(whatsmeow.NewClient(&wastore.Device{ID: &own}, nil))
	if err := a.Logout(t.Context()); err == nil {
		t.Fatal("a never connected client logged out")
	}
	if a.OwnID() == nil {
		t.Error("a failed logout forgot the own JID")
	}
}

func TestAdapterStoreAccessors(t *testing.T) {
	t.Parallel()
	t.Run("with a device store", func(t *testing.T) {
		t.Parallel()
		a := New(whatsmeow.NewClient(&wastore.Device{ID: &own, LID: lid, PushName: "Me"}, nil))
		if a.OwnLID() != lid || a.PushName() != "Me" {
			t.Errorf("LID %s, name %q", a.OwnLID(), a.PushName())
		}
		// A device without its database has no sub-stores.
		if a.LIDs() != nil || a.Contacts() != nil {
			t.Error("sub-stores of a bare device")
		}
		secret, sender, err := a.MessageSecret(t.Context(), lid, lid, "M1")
		if secret != nil || !sender.IsEmpty() || err != nil {
			t.Errorf("secret %v %s %v", secret, sender, err)
		}
	})
	t.Run("without a device store", func(t *testing.T) {
		t.Parallel()
		a := &adapter{Client: &whatsmeow.Client{}}
		if a.OwnID() != nil || !a.OwnLID().IsEmpty() || a.PushName() != "" || a.LIDs() != nil || a.Contacts() != nil {
			t.Error("a client without a store reports an account")
		}
	})
}
