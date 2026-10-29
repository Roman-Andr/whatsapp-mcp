package bridge

import (
	"errors"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
)

func snowGroup() *types.GroupInfo {
	return &types.GroupInfo{
		JID:            types.NewJID("120363431597928274", types.GroupServer),
		OwnerJID:       types.NewJID("32547396399312", types.HiddenUserServer),
		OwnerPN:        types.NewJID("18046727669", types.DefaultUserServer),
		GroupName:      types.GroupName{Name: "Snow operations system"},
		GroupTopic:     types.GroupTopic{Topic: "Routes and depots", TopicSetAt: time.Unix(1700000000, 0), TopicSetBy: types.NewJID("2", types.HiddenUserServer)},
		GroupAnnounce:  types.GroupAnnounce{IsAnnounce: true},
		AddressingMode: types.AddressingModeLID,
		Participants: []types.GroupParticipant{
			{JID: types.NewJID("2", types.HiddenUserServer), DisplayName: "zed"},
			{JID: types.NewJID("32547396399312", types.HiddenUserServer), PhoneNumber: types.NewJID("18046727669", types.DefaultUserServer), IsSuperAdmin: true},
			{JID: types.NewJID("79966764464", types.DefaultUserServer)},
			{JID: ownLID},
		},
	}
}

func TestDescribeGroupListsAdminsFirst(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	resp := b.describeGroup(t.Context(), snowGroup())
	if !resp.Success || resp.Name != "Snow operations system" || resp.Description != "Routes and depots" || !resp.AnnounceOnly || resp.DescriptionSetBy != "2" {
		t.Errorf("group %+v", resp)
	}
	if resp.Owner != "+18046727669" || resp.MemberCount != 4 || resp.AddressingMode != "lid" {
		t.Errorf("owner %q count %d mode %q", resp.Owner, resp.MemberCount, resp.AddressingMode)
	}
	cases := []struct {
		name  string
		check func(GroupMember) bool
	}{
		{"creator first", func(m GroupMember) bool {
			return m.IsAdmin && m.IsSuperAdmin && m.PhoneNumber == "18046727669" && m.LID == "32547396399312@lid"
		}},
		{"phone member by number", func(m GroupMember) bool { return m.PhoneNumber == "79966764464" && m.Name == "+79966764464" }},
		{"ourselves", func(m GroupMember) bool { return m.IsMe && m.LID == "9000@lid" }},
		{"anonymous member", func(m GroupMember) bool { return m.Name == "zed" && !m.IsAdmin }},
	}
	for i, c := range cases {
		if !c.check(resp.Members[i]) {
			t.Errorf("%s: %+v", c.name, resp.Members[i])
		}
	}
}

func TestGroupInfo(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	info := snowGroup()
	b.fake.Groups[info.JID] = info
	if resp := b.GroupInfo(t.Context(), GroupInfoRequest{ChatJID: info.JID.String(), InviteLink: true}); resp.InviteLink != "https://chat.whatsapp.com/120363431597928274" {
		t.Errorf("%+v", resp)
	}
	b.fake.InviteLinkErr = errors.New("not an admin")
	if resp := b.GroupInfo(t.Context(), GroupInfoRequest{ChatJID: info.JID.String(), InviteLink: true}); resp.InviteLinkError != "not an admin (only admins can see the invite link)" {
		t.Errorf("%+v", resp)
	}
	cases := []struct{ name, chat, want string }{
		{"not a group", aliceChat, `"2000@s.whatsapp.net" is not a group JID (…@g.us)`},
		{"unknown group", "555@g.us", "Failed to get group info: "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if resp := b.GroupInfo(t.Context(), GroupInfoRequest{ChatJID: c.chat}); resp.Success || len(resp.Message) < len(c.want) || resp.Message[:len(c.want)] != c.want {
				t.Errorf("%+v", resp)
			}
		})
	}
}
