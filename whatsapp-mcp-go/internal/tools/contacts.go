package tools

import (
	"context"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/render"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/store"
)

func searchContacts(ctx context.Context, c *Call) (any, error) {
	return c.DB.SearchContacts(ctx, c.Args.String("query"), c.Args.Int("limit")), nil
}

// phoneJID is the JID a bare phone number stands for.
func phoneJID(jid string) string {
	if strings.Contains(jid, "@") {
		return jid
	}
	return strings.TrimLeft(jid, "+") + "@s.whatsapp.net"
}

// filterByScope drops the entries of list key whose jid is not readable.
// With always unset and no restriction the list stays as the bridge sent
// it. A read allowlist that cannot be read restricts nothing here: both
// lists fail open, as they always did (PLAN.md §1.5 M1).
func filterByScope(ctx context.Context, c *Call, result *pyfmt.OMap, key string, always bool) {
	list, ok := result.Get(key).([]any)
	if !ok {
		return
	}
	s := c.DB.ScopeOrNil(ctx)
	if s == nil && !always {
		return
	}
	kept := []any{}
	for _, item := range list {
		jid := ""
		if m, ok := item.(*pyfmt.OMap); ok {
			jid, _ = m.Get("jid").(string)
		}
		if store.Readable(jid, s) {
			kept = append(kept, item)
		}
	}
	result.Set(key, kept)
}

// contactInfo asks the bridge about a readable contact.
func contactInfo(ctx context.Context, c *Call, jid string, picture bool) *pyfmt.OMap {
	if hidden := c.DB.CheckReadable(ctx, phoneJID(jid)); hidden != "" {
		return render.Refused(hidden)
	}
	return c.Bridge.ContactInfo(ctx, jid, picture)
}

func getContactInfo(ctx context.Context, c *Call) (any, error) {
	return contactInfo(ctx, c, c.Args.String("jid"), c.Args.Bool("include_picture")), nil
}

// viewProfilePicture shows a contact's or group's picture, or says why
// there is none.
func viewProfilePicture(ctx context.Context, c *Call) (any, error) {
	info := contactInfo(ctx, c, c.Args.String("jid"), true)
	if !pyfmt.Truthy(info.Get("picture_path")) {
		msg := info.Get("picture_error")
		if !pyfmt.Truthy(msg) {
			msg = "No picture"
			if info.Has("message") {
				msg = info.Get("message")
			}
		}
		return pyfmt.NewOMap("success", false, "message", msg), nil
	}
	return loadImage(c, pyfmt.AsString(info.Get("picture_path")))
}

func blockContact(ctx context.Context, c *Call) (any, error) {
	return result(c.Bridge.BlockContact(ctx, c.Args.String("jid"), c.Args.Bool("blocked"))), nil
}

// listBlockedContacts is the bridge's blocklist, filtered by the read
// allowlist.
func listBlockedContacts(ctx context.Context, c *Call) (any, error) {
	r := c.Bridge.Blocklist(ctx)
	filterByScope(ctx, c, r, "blocked", false)
	return r, nil
}

func getPresence(ctx context.Context, c *Call) (any, error) {
	jid := c.Args.String("jid")
	if hidden := c.DB.CheckReadable(ctx, phoneJID(jid)); hidden != "" {
		return render.Refused(hidden), nil
	}
	return c.Bridge.Presence(ctx, jid), nil
}
