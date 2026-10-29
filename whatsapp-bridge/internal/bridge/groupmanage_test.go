package bridge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/types"
)

func TestInviteCode(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, in string }{
		{"link", "https://chat.whatsapp.com/AbC123xyz"},
		{"link with a query", "chat.whatsapp.com/AbC123xyz/?utm=1"},
		{"bare code", "AbC123xyz"},
		{"padded link", "\t https://chat.whatsapp.com/AbC123xyz/  "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := inviteCode(c.in); got != "AbC123xyz" {
				t.Errorf("inviteCode(%q) = %q", c.in, got)
			}
		})
	}
}

func TestUpdateGroupSettings(t *testing.T) {
	t.Parallel()
	name, on := "Plows", true
	cases := []struct {
		name    string
		req     GroupSettingsRequest
		failOn  string
		want    string
		renamed bool
	}{
		{"everything", GroupSettingsRequest{ChatJID: opsGroup, Name: &name, Description: &name, AnnounceOnly: &on, Locked: &on, JoinApproval: &on},
			"", "Changed name, description, announce_only, locked, join_approval of 120@g.us", true},
		{"fails part way", GroupSettingsRequest{ChatJID: opsGroup, Name: &name, Locked: &on},
			"SetGroupLocked", "Failed to change locked: not an admin (already changed: name)", true},
		{"nothing to change", GroupSettingsRequest{ChatJID: opsGroup},
			"", "nothing to change: give name, description, announce_only, locked or join_approval", false},
		{"name too long", GroupSettingsRequest{ChatJID: opsGroup, Name: new(strings.Repeat("x", 26))},
			"", "group name must have 1 to 25 characters", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t)
			b.fake.SetGroupFunc = func(method string, _ types.JID) error {
				if method == c.failOn {
					return errors.New("not an admin")
				}
				return nil
			}
			if resp := b.UpdateGroupSettings(t.Context(), c.req); resp.Message != c.want {
				t.Errorf("%+v", resp)
			}
			if renamed := b.store.ChatName(t.Context(), opsGroup) == "Plows"; renamed != c.renamed {
				t.Errorf("renamed %v", renamed)
			}
		})
	}
}

func TestJoinGroup(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		opts    []func(*Deps)
		invite  string
		linkErr error
		want    string
		refused bool
	}{
		{"joined", nil, "https://chat.whatsapp.com/AbC", nil, "Joined Plows (120@g.us)", false},
		{"not allowed", []func(*Deps){allowSend(t, aliceChat)}, "AbC", nil, "chat 120@g.us is not in the send allowlist", true},
		{"expired", nil, "AbC", errors.New("gone"), "Invalid or expired invite: gone", false},
		{"not an invite", nil, "https://example.com/a b", nil, "invite must be a https://chat.whatsapp.com/<code> link or its code", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := newOnlineBridge(t, c.opts...)
			b.fake.GroupFromLinkFunc = func(_ context.Context, code string) (*types.GroupInfo, error) {
				if code != "AbC" {
					t.Errorf("code %q", code)
				}
				return &types.GroupInfo{JID: groupJID, GroupName: types.GroupName{Name: "Plows"}}, c.linkErr
			}
			b.fake.JoinWithLinkFunc = func(context.Context, string) (types.JID, error) { return groupJID, nil }
			resp := b.JoinGroup(t.Context(), JoinGroupRequest{Invite: c.invite})
			if !strings.HasPrefix(resp.Message, c.want) || resp.Refused != c.refused {
				t.Errorf("%+v", resp)
			}
			if resp.Success && b.store.ChatName(t.Context(), opsGroup) != "Plows" {
				t.Error("joined group not stored")
			}
		})
	}
}

func TestCreateGroupAndParticipants(t *testing.T) {
	t.Parallel()
	b := newOnlineBridge(t)
	resp := b.CreateGroup(t.Context(), CreateGroupRequest{Name: "Plows", Participants: []string{"+2000"}})
	if !resp.Success || resp.Group == nil || resp.Group.Name != "Plows" || len(resp.Participants) != 1 || resp.Participants[0].Name != "Alice Full" {
		t.Fatalf("%+v", resp)
	}
	group := resp.Group.JID
	b.fake.ParticipantErrors = map[types.JID]int{bobPN: 403}
	update := b.UpdateParticipants(t.Context(), GroupParticipantsRequest{ChatJID: group, Action: "add", Participants: []string{"2000", "3000"}})
	if !update.Success || update.Message != "add: 1 of 2 participant(s) done" || update.Participants[1].Error != "not allowed (403): their privacy settings or you are not an admin" {
		t.Errorf("%+v", update)
	}
	if leave := b.LeaveGroup(t.Context(), GroupChatRequest{ChatJID: group}); !leave.Success {
		t.Errorf("%+v", leave)
	}
	refusals := []struct {
		name string
		resp GroupChangeResponse
		want string
	}{
		{"no name", b.CreateGroup(t.Context(), CreateGroupRequest{}), "name is required"},
		{"long name", b.CreateGroup(t.Context(), CreateGroupRequest{Name: strings.Repeat("x", 30)}), "group name has 30 characters; WhatsApp allows 25"},
		{"group as participant", b.CreateGroup(t.Context(), CreateGroupRequest{Name: "x", Participants: []string{opsGroup}}), `participant "120@g.us" is a group`},
		{"unknown action", b.UpdateParticipants(t.Context(), GroupParticipantsRequest{ChatJID: group, Action: "kick"}), `unknown action "kick" (want add, remove, promote or demote)`},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			if c.resp.Success || c.resp.Message != c.want {
				t.Errorf("%+v", c.resp)
			}
		})
	}
}
