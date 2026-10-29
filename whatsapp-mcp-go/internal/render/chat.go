package render

import (
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/store"
)

// Chat renders the chat for a tool result (T separator, Z for UTC) or, with
// forResource, for a resource (the space-separated str(datetime) form).
func Chat(c *store.Chat, forResource bool) *pyfmt.OMap {
	var lastTime any
	if c.LastMessageTime != nil {
		if forResource {
			lastTime = c.LastMessageTime.ISOFormat(" ")
		} else {
			lastTime = c.LastMessageTime.Pydantic()
		}
	}
	labels := c.Labels
	if labels == nil {
		labels = []string{}
	}
	return pyfmt.NewOMap("jid", c.JID, "name", pyfmt.Ptr(c.Name), "last_message_time", lastTime,
		"last_message", pyfmt.Ptr(c.LastMessage), "last_sender", pyfmt.Ptr(c.LastSender), "last_is_from_me", pyfmt.Ptr(c.LastIsFromMe),
		"unread_count", pyfmt.Ptr(c.UnreadCount), "archived", pyfmt.Ptr(c.Archived), "pinned", pyfmt.Ptr(c.Pinned),
		"muted_until", pyfmt.Ptr(c.MutedUntil), "labels", labels)
}

// Contact renders a contact for a tool result.
func Contact(c *store.Contact) *pyfmt.OMap {
	return pyfmt.NewOMap("phone_number", c.PhoneNumber, "name", pyfmt.Ptr(c.Name), "jid", c.JID)
}

// Refused is the {success: false, message} answer of a refused call.
func Refused(msg string) *pyfmt.OMap { return pyfmt.NewOMap("success", false, "message", msg) }

// MessageOr is the message of a bridge answer, or fallback when it has none.
func MessageOr(result *pyfmt.OMap, fallback string) string {
	if result.Has("message") {
		return pyfmt.Str(result.Get("message"))
	}
	return fallback
}
