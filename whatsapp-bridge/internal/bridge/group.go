package bridge

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

// GroupInfo fetches a group's metadata from WhatsApp and resolves member
// names. It changes nothing, so the send allowlist does not apply.
func (b *Bridge) GroupInfo(ctx context.Context, req GroupInfoRequest) GroupInfoResponse {
	chat, err := types.ParseJID(req.ChatJID)
	if err != nil || chat.Server != types.GroupServer {
		return GroupInfoResponse{Message: fmt.Sprintf("%q is not a group JID (…@g.us)", req.ChatJID)}
	}
	if !b.Online() {
		return GroupInfoResponse{Message: "Not connected to WhatsApp"}
	}
	info, err := b.client.GetGroupInfo(ctx, chat)
	if err != nil {
		return GroupInfoResponse{Message: fmt.Sprintf("Failed to get group info: %v", err)}
	}
	resp := b.describeGroup(ctx, info)
	if req.InviteLink {
		link, err := b.client.GetGroupInviteLink(ctx, chat, false)
		if err != nil {
			resp.InviteLinkError = fmt.Sprintf("%v (only admins can see the invite link)", err)
		} else {
			resp.InviteLink = link
		}
	}
	return resp
}

// describeGroup turns whatsmeow's group info into the API answer.
func (b *Bridge) describeGroup(ctx context.Context, info *types.GroupInfo) GroupInfoResponse {
	resp := GroupInfoResponse{
		Success:          true,
		JID:              info.JID.String(),
		Name:             info.Name,
		Description:      info.Topic,
		DescriptionSetAt: info.TopicSetAt,
		Created:          info.GroupCreated,
		AnnounceOnly:     info.IsAnnounce,
		Locked:           info.IsLocked,
		AddressingMode:   string(info.AddressingMode),
		MemberCount:      len(info.Participants),
	}
	if !info.TopicSetBy.IsEmpty() {
		resp.DescriptionSetBy = b.memberName(ctx, info.TopicSetBy, info.TopicSetByPN, "")
	}
	if !info.OwnerJID.IsEmpty() {
		resp.Owner = b.memberName(ctx, info.OwnerJID, info.OwnerPN, "")
	}
	var ownPN, ownLID string
	if b.client != nil && b.client.OwnID() != nil {
		ownPN = b.client.OwnID().User
		ownLID = b.client.OwnLID().User
	}
	for _, p := range info.Participants {
		m := GroupMember{
			JID: p.JID.String(), IsAdmin: p.IsAdmin || p.IsSuperAdmin, IsSuperAdmin: p.IsSuperAdmin,
			Name: b.memberName(ctx, p.JID, p.PhoneNumber, p.DisplayName),
		}
		if !p.PhoneNumber.IsEmpty() {
			m.PhoneNumber = p.PhoneNumber.User
		} else if p.JID.Server == types.DefaultUserServer {
			m.PhoneNumber = p.JID.User
		}
		if !p.LID.IsEmpty() {
			m.LID = p.LID.String()
		} else if p.JID.Server == types.HiddenUserServer {
			m.LID = p.JID.String()
		}
		m.IsMe = (ownPN != "" && m.PhoneNumber == ownPN) || (ownLID != "" && strings.HasPrefix(m.LID, ownLID+"@"))
		resp.Members = append(resp.Members, m)
	}
	// Admins first, then by name: the people who run the group are usually
	// who an agent is looking for.
	sort.SliceStable(resp.Members, func(i, j int) bool {
		a, c := resp.Members[i], resp.Members[j]
		if a.IsAdmin != c.IsAdmin {
			return a.IsAdmin
		}
		return strings.ToLower(a.Name) < strings.ToLower(c.Name)
	})
	return resp
}

// memberName resolves a display name, falling back to the phone number.
func (b *Bridge) memberName(ctx context.Context, jid, pn types.JID, displayName string) string {
	if b.client != nil {
		if name := b.resolveName(ctx, jid, pn, ""); name != "" {
			return name
		}
	}
	if displayName != "" {
		return displayName
	}
	if !pn.IsEmpty() {
		return "+" + pn.User
	}
	if jid.Server == types.DefaultUserServer {
		return "+" + jid.User
	}
	return jid.User
}
