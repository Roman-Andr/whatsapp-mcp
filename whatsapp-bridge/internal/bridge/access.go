package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// jidAliases returns jid and the other form it is known by: the phone number
// for a LID and the LID for a phone number, when the bridge knows the mapping.
func (b *Bridge) jidAliases(ctx context.Context, jid types.JID) []types.JID {
	jid = jid.ToNonAD()
	out := []types.JID{jid}
	if b.client == nil || b.client.LIDs() == nil {
		return out
	}
	lids := b.client.LIDs()
	switch jid.Server {
	case types.HiddenUserServer:
		if pn, err := lids.GetPNForLID(ctx, jid); err == nil && !pn.IsEmpty() {
			out = append(out, pn.ToNonAD())
		}
	case types.DefaultUserServer:
		if lid, err := lids.GetLIDForPN(ctx, jid); err == nil && !lid.IsEmpty() {
			out = append(out, lid.ToNonAD())
		}
	}
	return out
}

// checkAllowed returns an error matching policy.ErrNotAllowed when the
// allowlist forbids changing anything in chat. A direct chat can be addressed by phone number or by LID; the
// allowlist entry may use either, so both are checked.
func (b *Bridge) checkAllowed(ctx context.Context, chat types.JID) error {
	if !b.allowlist.Restricted() {
		return nil
	}
	if b.allowlist.Allows(chat, b.jidAliases(ctx, chat)...) {
		return nil
	}
	return policy.NotAllowed(fmt.Sprintf("chat %s is not in the send allowlist (WHATSAPP_SEND_ALLOWLIST / WHATSAPP_SEND_ALLOWLIST_FILE); allowed: %s",
		chat.ToNonAD(), strings.Join(b.allowlist.Entries(), ", ")))
}

// checkReadable returns an error when the read allowlist hides chat.
func (b *Bridge) checkReadable(ctx context.Context, chat types.JID) error {
	if !b.readAllowlist.Restricted() {
		return nil
	}
	if b.readAllowlist.Allows(chat, b.jidAliases(ctx, chat)...) {
		return nil
	}
	return fmt.Errorf("chat %s is not in the read allowlist (WHATSAPP_READ_ALLOWLIST / WHATSAPP_READ_ALLOWLIST_FILE)", chat.ToNonAD())
}

// CheckReadable parses chatJID and checks it against the read allowlist.
func (b *Bridge) CheckReadable(ctx context.Context, chatJID string) error {
	if !b.readAllowlist.Restricted() {
		return nil
	}
	chat, err := jid.ParseRecipient(chatJID)
	if err != nil {
		return fmt.Errorf("invalid chat %q: %w", chatJID, err)
	}
	return b.checkReadable(ctx, chat)
}

// PublishReadAllowlist records the read allowlist in messages.db, where the
// MCP server reads it: the server reads the store directly, so it must apply
// the same restriction to everything it returns. The list is written with
// every alias the bridge knows (LID and phone number of each direct chat).
func (b *Bridge) PublishReadAllowlist(ctx context.Context) error {
	if b.store == nil {
		return nil
	}
	if !b.readAllowlist.Restricted() {
		return b.store.SetMeta(ctx, "read_allowlist", "")
	}
	seen := map[string]bool{}
	var all []string
	for _, entry := range b.readAllowlist.Entries() {
		jid, err := types.ParseJID(entry)
		if err != nil {
			continue
		}
		for _, alias := range b.jidAliases(ctx, jid) {
			if s := alias.String(); !seen[s] {
				seen[s] = true
				all = append(all, s)
			}
		}
	}
	sort.Strings(all)
	encoded, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return b.store.SetMeta(ctx, "read_allowlist", string(encoded))
}
