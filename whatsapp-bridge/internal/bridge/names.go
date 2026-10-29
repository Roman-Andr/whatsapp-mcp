package bridge

import (
	"context"
	"fmt"

	"go.mau.fi/whatsmeow/proto/waHistorySync"
	"go.mau.fi/whatsmeow/types"
)

// ownJID is the account's own JID in the addressing mode of a chat: groups
// and chats that use LIDs see us by our LID, everything else by phone number.
func (b *Bridge) ownJID(chat types.JID) types.JID {
	if chat.Server == types.HiddenUserServer && !b.client.OwnLID().IsEmpty() {
		return b.client.OwnLID().ToNonAD()
	}
	if b.client.OwnID() == nil {
		return types.EmptyJID
	}
	return b.client.OwnID().ToNonAD()
}

// storedChat is the JID a chat is stored under. WhatsApp addresses a direct
// chat sometimes by phone number and sometimes by LID: messages typed on the
// phone into "Message yourself" arrive under the account's own LID, while the
// bridge sends to the phone number. Both forms are stored as the phone number
// whenever the mapping is known, so the conversation is one chat.
func (b *Bridge) storedChat(ctx context.Context, chat types.JID) types.JID {
	chat = chat.ToNonAD()
	if chat.Server != types.HiddenUserServer || b.client == nil {
		return chat
	}
	if id, lid := b.client.OwnID(), b.client.OwnLID(); id != nil && !lid.IsEmpty() && chat.User == lid.User {
		return id.ToNonAD()
	}
	if lids := b.client.LIDs(); lids != nil {
		if pn, err := lids.GetPNForLID(ctx, chat); err == nil && !pn.IsEmpty() {
			return pn.ToNonAD()
		}
	}
	return chat
}

func (b *Bridge) storedChatString(ctx context.Context, chat types.JID) string {
	return b.storedChat(ctx, chat).String()
}

// MergeLIDChats moves direct chats stored under a LID into their phone-number
// chat once the mapping is known: the store from before storedChat, and LIDs
// whose phone number the bridge learned later.
func (b *Bridge) MergeLIDChats(ctx context.Context) {
	if b.store == nil || b.client == nil {
		return
	}
	lids, err := b.store.ChatsOnServer(ctx, types.HiddenUserServer)
	if err != nil {
		b.log.Warn("failed to list LID chats", "err", err)
		return
	}
	for _, lid := range lids {
		jid, err := types.ParseJID(lid)
		if err != nil {
			continue
		}
		targetJID := b.storedChat(ctx, jid)
		target := targetJID.String()
		if target == lid {
			continue
		}
		name := b.chatName(ctx, targetJID, nil, "")
		if name == targetJID.User {
			name = ""
		}
		if err := b.store.MergeChat(ctx, lid, target, name); err != nil {
			b.log.Warn("failed to merge chat", "from", lid, "into", target, "err", err)
			continue
		}
		b.log.Info("merged chat", "from", lid, "into", target)
	}
}

// resolveName finds a display name for a user. Group members are addressed
// by LID, which has no name of its own: the saved contact name usually lives
// on the phone-number JID, reached through the LID map.
func (b *Bridge) resolveName(ctx context.Context, jid, alt types.JID, pushName string) string {
	if b.client == nil {
		return pushName
	}
	candidates := []types.JID{jid.ToNonAD()}
	if !alt.IsEmpty() {
		candidates = append(candidates, alt.ToNonAD())
	}
	if jid.Server == types.HiddenUserServer {
		if pn, err := b.client.LIDs().GetPNForLID(ctx, jid.ToNonAD()); err == nil && !pn.IsEmpty() {
			candidates = append(candidates, pn)
		}
	}
	var fallback string
	for _, c := range candidates {
		contact, err := b.client.Contacts().GetContact(ctx, c)
		if err != nil || !contact.Found {
			continue
		}
		if contact.FullName != "" {
			return contact.FullName
		}
		if fallback == "" {
			fallback = firstNonEmpty(contact.FirstName, contact.PushName, contact.BusinessName)
		}
	}
	return firstNonEmpty(fallback, pushName)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// statusChatName names the chat that collects everyone's status updates.
const statusChatName = "Status updates"

// chatName determines the name a chat is stored under. conv is the history
// sync's conversation, nil for live events; sender is the sender's user
// part, the last resort for a direct chat.
func (b *Bridge) chatName(ctx context.Context, jid types.JID, conv *waHistorySync.Conversation, sender string) string {
	// A stored name is kept unless it is just the number the chat was named
	// after for lack of anything better; those get another lookup.
	if existing := b.store.ChatName(ctx, jid.String()); existing != "" && existing != jid.User {
		return existing
	}

	var name string

	if jid.Server == types.GroupServer {
		// History sync names the group itself.
		if conv != nil {
			if dn := conv.GetDisplayName(); dn != "" {
				name = dn
			} else if n := conv.GetName(); n != "" {
				name = n
			}
		}

		// If we didn't get a name, try group info
		if name == "" {
			if info := b.groupInfo(ctx, jid); info != nil && info.Name != "" {
				name = info.Name
			} else {
				// Fallback name for groups
				name = fmt.Sprintf("Group %s", jid.User)
			}
		}
		return name
	}

	if jid == types.StatusBroadcastJID {
		return statusChatName
	}

	// Individual chat: the contact's name, looked up through the phone
	// number when the chat is addressed by LID.
	candidates := []types.JID{jid}
	if pn := b.pnForLID(ctx, jid); !pn.IsEmpty() {
		candidates = append(candidates, pn)
	}
	for _, c := range candidates {
		if name = b.contactName(ctx, c); name != "" {
			return name
		}
	}
	if sender != "" {
		return sender
	}
	return jid.User
}

// groupInfo asks WhatsApp for a group's metadata; nil without a client or
// on failure.
func (b *Bridge) groupInfo(ctx context.Context, group types.JID) *types.GroupInfo {
	if b.client == nil {
		return nil
	}
	info, err := b.client.GetGroupInfo(ctx, group)
	if err != nil {
		return nil
	}
	return info
}

// contactName is the best name of a saved or known contact, "" for none.
func (b *Bridge) contactName(ctx context.Context, user types.JID) string {
	if b.client == nil || b.client.Contacts() == nil {
		return ""
	}
	contact, err := b.client.Contacts().GetContact(ctx, user)
	if err != nil || !contact.Found {
		return ""
	}
	return firstNonEmpty(contact.FullName, contact.FirstName, contact.PushName, contact.BusinessName)
}

// pnForLID is the phone number of a LID, empty when jid is not a LID or the
// mapping is unknown.
func (b *Bridge) pnForLID(ctx context.Context, jid types.JID) types.JID {
	if jid.Server != types.HiddenUserServer || b.client == nil || b.client.LIDs() == nil {
		return types.EmptyJID
	}
	pn, err := b.client.LIDs().GetPNForLID(ctx, jid)
	if err != nil {
		return types.EmptyJID
	}
	return pn
}
