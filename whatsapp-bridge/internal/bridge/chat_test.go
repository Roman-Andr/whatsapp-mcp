package bridge

import (
	"strings"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/store"
)

func TestFindLabelFoldsEveryScript(t *testing.T) {
	t.Parallel()
	st := openTestStore(t)
	if err := st.SaveLabel(t.Context(), "1", "Оплачено", 2, false); err != nil {
		t.Fatal(err)
	}
	if id, err := st.FindLabel(t.Context(), "оплачено"); err != nil || id != "1" {
		t.Errorf("%q %v", id, err)
	}
}

func TestChatAction(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		req    ChatActionRequest
		want   string
		column string // chats column to check; "" for none
		value  string
	}{
		{"archive", ChatActionRequest{ChatJID: opsGroup, Action: "archive"}, "Chat 120@g.us archived", "archived", "1"},
		{"unarchive", ChatActionRequest{ChatJID: opsGroup, Action: "Unarchive"}, "Chat 120@g.us unarchived", "archived", "0"},
		{"pin", ChatActionRequest{ChatJID: opsGroup, Action: "pin"}, "Chat 120@g.us pinned", "pinned", "1"},
		{"mute for two hours", ChatActionRequest{ChatJID: opsGroup, Action: "mute", MuteHours: 2}, "Chat 120@g.us muted for 2h0m0s", "", ""},
		{"mute forever", ChatActionRequest{ChatJID: opsGroup, Action: "mute"}, "Chat 120@g.us muted forever", "muted_until", "-1"},
		{"unmute", ChatActionRequest{ChatJID: opsGroup, Action: "unmute"}, "Chat 120@g.us unmuted", "muted_until", "0"},
		{"delete", ChatActionRequest{ChatJID: opsGroup, Action: "delete"}, "Chat 120@g.us deleted (hidden on every device; the bridge keeps its copy of the messages)", "", ""},
		{"negative mute", ChatActionRequest{ChatJID: opsGroup, Action: "mute", MuteHours: -1}, "mute_hours must not be negative (0 mutes forever)", "", ""},
		{"unknown action", ChatActionRequest{ChatJID: opsGroup, Action: "explode"}, `unknown action "explode" (want archive, unarchive, pin, unpin, mute, unmute or delete)`, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			mustStore(t, b.store, store.Message{ID: "M1", ChatJID: opsGroup, Sender: "8000", SenderJID: "8000@lid", Content: "x", Timestamp: time.Now()})
			if resp := b.ChatAction(t.Context(), c.req); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
			if c.column != "" {
				if got := queryString(t, b, "SELECT "+c.column+" FROM chats WHERE jid = ?", opsGroup); got != c.value {
					t.Errorf("%s = %q", c.column, got)
				}
			}
		})
	}
}

func TestLabels(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	steps := []struct {
		name string
		do   func() SendMessageResponse
		want string
	}{
		{"create", func() SendMessageResponse { return b.EditLabel(t.Context(), LabelEditRequest{Name: "Paid", Color: 2}) }, `Label 1 "Paid" saved`},
		{"label a chat by name", func() SendMessageResponse {
			return b.LabelChat(t.Context(), LabelChatRequest{ChatJID: opsGroup, Label: "paid", Labeled: true})
		}, "Label 1 added to 120@g.us"},
		{"remove", func() SendMessageResponse {
			return b.LabelChat(t.Context(), LabelChatRequest{ChatJID: opsGroup, Label: "1"})
		}, "Label 1 removed from 120@g.us"},
		{"unknown label", func() SendMessageResponse {
			return b.LabelChat(t.Context(), LabelChatRequest{ChatJID: opsGroup, Label: "nope"})
		}, `no label "nope"`},
		{"bad colour", func() SendMessageResponse { return b.EditLabel(t.Context(), LabelEditRequest{Name: "X", Color: 20}) }, "color must be 0 to 19 (WhatsApp's label palette)"},
		{"delete without id", func() SendMessageResponse { return b.EditLabel(t.Context(), LabelEditRequest{Deleted: true}) }, "label_id is required to delete a label"},
		{"no name", func() SendMessageResponse { return b.EditLabel(t.Context(), LabelEditRequest{}) }, "name is required"},
		{"delete", func() SendMessageResponse {
			return b.EditLabel(t.Context(), LabelEditRequest{LabelID: "1", Deleted: true})
		}, `Label 1 "Paid" deleted`},
	}
	for _, s := range steps {
		if resp := s.do(); !strings.HasPrefix(resp.Message, s.want) {
			t.Errorf("%s: %+v", s.name, resp)
		}
	}
}
