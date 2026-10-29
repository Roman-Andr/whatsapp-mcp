package bridge

import (
	"errors"
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

func TestSendAllowlistAcceptsEitherAlias(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t, allowSend(t, "8000@lid"))
	cases := []struct {
		name    string
		chat    string
		allowed bool
	}{
		{"listed LID", "8000@lid", true},
		{"its phone number", aliceChat, true},
		{"someone else", "3000@s.whatsapp.net", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := b.checkAllowed(t.Context(), mustParse(t, c.chat))
			if (err == nil) != c.allowed {
				t.Errorf("err %v", err)
			}
			if err != nil && (!errors.Is(err, policy.ErrNotAllowed) || !strings.Contains(err.Error(), "allowed: 8000@lid")) {
				t.Errorf("refusal %v", err)
			}
		})
	}
}

func TestCheckReadable(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t, allowRead(t, aliceChat))
	cases := []struct {
		name, chat, want string // want: error prefix; "" allowed
	}{
		{"listed", aliceChat, ""},
		{"alias", "8000@lid", ""},
		{"hidden", opsGroup, "chat 120@g.us is not in the read allowlist"},
		{"invalid", "a.b.c@s.whatsapp.net", `invalid chat "a.b.c@s.whatsapp.net": `},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := b.CheckReadable(t.Context(), c.chat)
			if (c.want == "") != (err == nil) || (err != nil && !strings.HasPrefix(err.Error(), c.want)) {
				t.Errorf("err %v, want %q", err, c.want)
			}
		})
	}
	if err := newTestBridge(t).CheckReadable(t.Context(), "anything"); err != nil {
		t.Errorf("unrestricted: %v", err)
	}
}

func TestPublishReadAllowlist(t *testing.T) {
	t.Parallel()
	t.Run("restricted", func(t *testing.T) {
		t.Parallel()
		b := newOnlineBridge(t, allowRead(t, opsGroup, "8000@lid"))
		if err := b.PublishReadAllowlist(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := queryString(t, b, "SELECT value FROM bridge_meta WHERE key = 'read_allowlist'"); got != `["120@g.us","2000@s.whatsapp.net","8000@lid"]` {
			t.Errorf("published %s", got)
		}
	})
	t.Run("unrestricted", func(t *testing.T) {
		t.Parallel()
		b := newTestBridge(t)
		if err := b.PublishReadAllowlist(t.Context()); err != nil {
			t.Fatal(err)
		}
		if got := queryString(t, b, "SELECT value FROM bridge_meta WHERE key = 'read_allowlist'"); got != "" {
			t.Errorf("published %s", got)
		}
	})
}
