package render

import (
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/store"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/storetest"
)

const group = storetest.Group

func newStore(t *testing.T) *storetest.Fixture {
	t.Helper()
	return storetest.New(t, storetest.Options{})
}

// mustList lists the fixture's messages with its current settings.
func mustList(t *testing.T, f *storetest.Fixture, o store.ListOpts) string {
	t.Helper()
	out, err := ListMessages(t.Context(), f.DB(), o)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func opts(edit func(o *store.ListOpts)) store.ListOpts {
	o := store.DefaultListOpts()
	if edit != nil {
		edit(&o)
	}
	return o
}

func noContext(o *store.ListOpts) { o.IncludeContext = false }

func chatOpts() store.ChatOpts {
	return store.ChatOpts{Limit: 20, IncludeLastMessage: true, SortBy: "last_active"}
}

func jids(chats []*store.Chat) []string {
	out := []string{}
	for _, c := range chats {
		out = append(out, c.JID)
	}
	return out
}
