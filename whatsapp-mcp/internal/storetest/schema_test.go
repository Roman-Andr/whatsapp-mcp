package storetest

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
)

// The embedded schema is the cross-module contract: it must be the
// golden dump of this module and, in a repository checkout, the bridge's
// own fresh schema.
func TestBridgeSchemaIsTheBridgesOwn(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		path     string
		optional bool
	}{
		{"../../testdata/golden/bridge-schema.sql", false},
		{"../../../whatsapp-bridge/testdata/contract/schema/fresh.sql", true},
	} {
		data, err := os.ReadFile(c.path)
		if c.optional && errors.Is(err, fs.ErrNotExist) {
			t.Logf("%s does not exist here; not compared", c.path)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != BridgeSchema {
			t.Errorf("bridge-schema.sql differs from %s", c.path)
		}
	}
}

func TestNewBuildsTheFixture(t *testing.T) {
	t.Parallel()
	f := New(t, Options{})
	db, err := f.DB().Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM messages").Scan(&n); err != nil || n != 4 {
		t.Errorf("messages: %d %v", n, err)
	}
	if New(t, Options{Schema: UpstreamSchema, Empty: true}).DB().ListChats(t.Context(), store.ChatOpts{Limit: 5}) == nil {
		t.Error("nil chats")
	}
}
