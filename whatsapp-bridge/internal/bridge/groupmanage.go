package bridge

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/jid"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// maxGroupName is WhatsApp's limit; longer names fail with 406.
const maxGroupName = 25

// parseParticipants parses phone numbers or JIDs; with check, each must be
// on the send allowlist, since adding someone to a group reaches them.
func (b *Bridge) parseParticipants(ctx context.Context, entries []string, check bool) ([]types.JID, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("participants are required")
	}
	out := make([]types.JID, 0, len(entries))
	for _, e := range entries {
		jid, err := jid.ParseRecipient(strings.TrimSpace(e))
		if err != nil {
			return nil, fmt.Errorf("participant %q: %w", e, err)
		}
		if jid.Server == types.GroupServer {
			return nil, fmt.Errorf("participant %q is a group", e)
		}
		if check {
			if err := b.checkAllowed(ctx, jid); err != nil {
				return nil, err
			}
		}
		out = append(out, jid.ToNonAD())
	}
	return out, nil
}

func (b *Bridge) participantResults(ctx context.Context, participants []types.GroupParticipant) ([]ParticipantResult, int) {
	results := make([]ParticipantResult, 0, len(participants))
	failures := 0
	for _, p := range participants {
		r := ParticipantResult{JID: p.JID.String(), Name: b.memberName(ctx, p.JID, p.PhoneNumber, p.DisplayName), Success: p.Error == 0}
		switch {
		case p.Error == 0:
		case p.AddRequest != nil:
			// The person only accepts invitations from contacts: WhatsApp
			// sends them an invite instead.
			r.Error = "needs an invitation (their privacy settings do not allow being added)"
		case p.Error == 403:
			r.Error = "not allowed (403): their privacy settings or you are not an admin"
		case p.Error == 409:
			r.Error = "already in the group (409)"
		case p.Error == 404:
			r.Error = "not on WhatsApp or not in the group (404)"
		default:
			r.Error = fmt.Sprintf("error %d", p.Error)
		}
		if !r.Success {
			failures++
		}
		results = append(results, r)
	}
	return results, failures
}

// CreateGroup creates a group or a community.
func (b *Bridge) CreateGroup(ctx context.Context, req CreateGroupRequest) GroupChangeResponse {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return groupFailed("name is required")
	}
	if n := utf8.RuneCountInString(name); n > maxGroupName {
		return groupFailed("group name has %d characters; WhatsApp allows %d", n, maxGroupName)
	}
	var participants []types.JID
	if !req.Community || len(req.Participants) > 0 {
		var err error
		participants, err = b.parseParticipants(ctx, req.Participants, true)
		if err != nil {
			if errors.Is(err, policy.ErrNotAllowed) {
				return groupBlocked(err)
			}
			return groupFailed("%v", err)
		}
	}
	if !b.Online() {
		return groupFailed("Not connected to WhatsApp")
	}
	create := whatsmeow.ReqCreateGroup{Name: name, Participants: participants}
	create.IsParent = req.Community
	info, err := b.client.CreateGroup(ctx, create)
	if err != nil {
		return groupFailed("Failed to create group: %v", err)
	}
	if err := b.store.SaveChat(ctx, info.JID.String(), info.Name, time.Now()); err != nil {
		b.log.Warn("failed to store new group", "err", err)
	}
	group := b.describeGroup(ctx, info)
	results, failures := b.participantResults(ctx, info.Participants)
	msg := fmt.Sprintf("Created group %s (%s)", info.Name, info.JID)
	if failures > 0 {
		msg += fmt.Sprintf("; %d participant(s) could not be added", failures)
	}
	return GroupChangeResponse{Success: true, Message: msg, Group: &group, Participants: results}
}

// LeaveGroup leaves a group.
func (b *Bridge) LeaveGroup(ctx context.Context, req GroupChatRequest) GroupChangeResponse {
	group, err := jid.ParseGroup(req.ChatJID)
	if err != nil {
		return groupFailed("%v", err)
	}
	if err := b.checkAllowed(ctx, group); err != nil {
		return groupBlocked(err)
	}
	if !b.Online() {
		return groupFailed("Not connected to WhatsApp")
	}
	if err := b.client.LeaveGroup(ctx, group); err != nil {
		return groupFailed("Failed to leave %s: %v", group, err)
	}
	return GroupChangeResponse{Success: true, Message: fmt.Sprintf("Left %s", group)}
}

var participantActions = map[string]whatsmeow.ParticipantChange{
	"add":     whatsmeow.ParticipantChangeAdd,
	"remove":  whatsmeow.ParticipantChangeRemove,
	"promote": whatsmeow.ParticipantChangePromote,
	"demote":  whatsmeow.ParticipantChangeDemote,
}

// UpdateParticipants adds, removes, promotes or demotes group members.
func (b *Bridge) UpdateParticipants(ctx context.Context, req GroupParticipantsRequest) GroupChangeResponse {
	group, err := jid.ParseGroup(req.ChatJID)
	if err != nil {
		return groupFailed("%v", err)
	}
	action, ok := participantActions[strings.ToLower(strings.TrimSpace(req.Action))]
	if !ok {
		return groupFailed("unknown action %q (want add, remove, promote or demote)", req.Action)
	}
	if err := b.checkAllowed(ctx, group); err != nil {
		return groupBlocked(err)
	}
	participants, err := b.parseParticipants(ctx, req.Participants, action == whatsmeow.ParticipantChangeAdd)
	if err != nil {
		if errors.Is(err, policy.ErrNotAllowed) {
			return groupBlocked(err)
		}
		return groupFailed("%v", err)
	}
	if !b.Online() {
		return groupFailed("Not connected to WhatsApp")
	}
	changed, err := b.client.UpdateGroupParticipants(ctx, group, participants, action)
	if err != nil {
		return groupFailed("Failed to %s participants: %v", action, err)
	}
	results, failures := b.participantResults(ctx, changed)
	resp := GroupChangeResponse{
		Success:      failures < len(results) || len(results) == 0,
		Message:      fmt.Sprintf("%s: %d of %d participant(s) done", action, len(results)-failures, len(results)),
		Participants: results,
	}
	return resp
}

// UpdateGroupSettings changes the group settings the request names.
func (b *Bridge) UpdateGroupSettings(ctx context.Context, req GroupSettingsRequest) GroupChangeResponse {
	group, err := jid.ParseGroup(req.ChatJID)
	if err != nil {
		return groupFailed("%v", err)
	}
	if req.Name == nil && req.Description == nil && req.AnnounceOnly == nil && req.Locked == nil && req.JoinApproval == nil {
		return groupFailed("nothing to change: give name, description, announce_only, locked or join_approval")
	}
	if req.Name != nil {
		if n := utf8.RuneCountInString(strings.TrimSpace(*req.Name)); n == 0 || n > maxGroupName {
			return groupFailed("group name must have 1 to %d characters", maxGroupName)
		}
	}
	if err := b.checkAllowed(ctx, group); err != nil {
		return groupBlocked(err)
	}
	if !b.Online() {
		return groupFailed("Not connected to WhatsApp")
	}
	var done []string
	fail := func(what string, err error) GroupChangeResponse {
		msg := fmt.Sprintf("Failed to change %s: %v", what, err)
		if len(done) > 0 {
			msg += fmt.Sprintf(" (already changed: %s)", strings.Join(done, ", "))
		}
		return groupFailed("%s", msg)
	}
	if req.Name != nil {
		if err := b.client.SetGroupName(ctx, group, strings.TrimSpace(*req.Name)); err != nil {
			return fail("name", err)
		}
		if err := b.store.SaveChat(ctx, group.String(), strings.TrimSpace(*req.Name), time.Time{}); err != nil {
			b.log.Warn("failed to store chat", "err", err)
		}
		done = append(done, "name")
	}
	if req.Description != nil {
		if err := b.client.SetGroupTopic(ctx, group, "", "", *req.Description); err != nil {
			return fail("description", err)
		}
		done = append(done, "description")
	}
	if req.AnnounceOnly != nil {
		if err := b.client.SetGroupAnnounce(ctx, group, *req.AnnounceOnly); err != nil {
			return fail("announce_only", err)
		}
		done = append(done, "announce_only")
	}
	if req.Locked != nil {
		if err := b.client.SetGroupLocked(ctx, group, *req.Locked); err != nil {
			return fail("locked", err)
		}
		done = append(done, "locked")
	}
	if req.JoinApproval != nil {
		if err := b.client.SetGroupJoinApprovalMode(ctx, group, *req.JoinApproval); err != nil {
			return fail("join_approval", err)
		}
		done = append(done, "join_approval")
	}
	return GroupChangeResponse{Success: true, Message: fmt.Sprintf("Changed %s of %s", strings.Join(done, ", "), group)}
}

// inviteCode extracts the code of a chat.whatsapp.com link.
func inviteCode(invite string) string {
	invite = strings.TrimSpace(invite)
	invite = strings.TrimPrefix(invite, "http://")
	invite = strings.TrimPrefix(invite, "https://")
	invite = strings.TrimPrefix(invite, "chat.whatsapp.com/")
	invite, _, _ = strings.Cut(invite, "?")
	return strings.Trim(invite, "/ ")
}

// JoinGroup joins a group by invite link, once the allowlist allows the group.
func (b *Bridge) JoinGroup(ctx context.Context, req JoinGroupRequest) GroupChangeResponse {
	code := inviteCode(req.Invite)
	if code == "" || strings.ContainsAny(code, "/ ") {
		return groupFailed("invite must be a https://chat.whatsapp.com/<code> link or its code")
	}
	if !b.Online() {
		return groupFailed("Not connected to WhatsApp")
	}
	// The group is looked up first so the allowlist decides before joining.
	info, err := b.client.GetGroupInfoFromLink(ctx, code)
	if err != nil {
		return groupFailed("Invalid or expired invite: %v", err)
	}
	if err := b.checkAllowed(ctx, info.JID); err != nil {
		return groupBlocked(err)
	}
	jid, err := b.client.JoinGroupWithLink(ctx, code)
	if err != nil {
		return groupFailed("Failed to join %s: %v", info.Name, err)
	}
	if err := b.store.SaveChat(ctx, jid.String(), info.Name, time.Now()); err != nil {
		b.log.Warn("failed to store chat", "err", err)
	}
	group := b.describeGroup(ctx, info)
	return GroupChangeResponse{Success: true, Message: fmt.Sprintf("Joined %s (%s)", info.Name, jid), Group: &group}
}

var disappearingTimers = map[string]time.Duration{
	"off": whatsmeow.DisappearingTimerOff, "0": whatsmeow.DisappearingTimerOff,
	"24h": whatsmeow.DisappearingTimer24Hours, "1d": whatsmeow.DisappearingTimer24Hours,
	"7d": whatsmeow.DisappearingTimer7Days, "90d": whatsmeow.DisappearingTimer90Days,
}

// GroupRequests lists the pending requests to join a group, with the HTTP
// status to answer.
func (b *Bridge) GroupRequests(ctx context.Context, chatJID string) (map[string]any, int) {
	group, err := jid.ParseGroup(chatJID)
	if err != nil {
		return map[string]any{"success": false, "message": err.Error()}, http.StatusBadRequest
	}
	if err := b.checkReadable(ctx, group); err != nil {
		return map[string]any{"success": false, "message": err.Error()}, http.StatusForbidden
	}
	if !b.Online() {
		return map[string]any{"success": false, "message": "Not connected to WhatsApp"}, http.StatusInternalServerError
	}
	reqs, err := b.client.GetGroupRequestParticipants(ctx, group)
	if err != nil {
		return map[string]any{"success": false, "message": fmt.Sprintf("Failed to get join requests (admins only): %v", err)}, http.StatusInternalServerError
	}
	out := make([]JoinRequest, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, JoinRequest{JID: r.JID.String(), Name: b.memberName(ctx, r.JID, types.EmptyJID, ""), RequestedAt: r.RequestedAt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RequestedAt.Before(out[j].RequestedAt) })
	return map[string]any{"success": true, "requests": out}, http.StatusOK
}

// UpdateGroupRequests approves or rejects requests to join a group.
func (b *Bridge) UpdateGroupRequests(ctx context.Context, req GroupRequestsUpdate) GroupChangeResponse {
	group, err := jid.ParseGroup(req.ChatJID)
	if err != nil {
		return groupFailed("%v", err)
	}
	var action whatsmeow.ParticipantRequestChange
	switch strings.ToLower(strings.TrimSpace(req.Action)) {
	case "approve":
		action = whatsmeow.ParticipantChangeApprove
	case "reject":
		action = whatsmeow.ParticipantChangeReject
	default:
		return groupFailed("unknown action %q (want approve or reject)", req.Action)
	}
	if err := b.checkAllowed(ctx, group); err != nil {
		return groupBlocked(err)
	}
	participants, err := b.parseParticipants(ctx, req.Participants, false)
	if err != nil {
		return groupFailed("%v", err)
	}
	if !b.Online() {
		return groupFailed("Not connected to WhatsApp")
	}
	changed, err := b.client.UpdateGroupRequestParticipants(ctx, group, participants, action)
	if err != nil {
		return groupFailed("Failed to %s join requests: %v", action, err)
	}
	results, failures := b.participantResults(ctx, changed)
	return GroupChangeResponse{
		Success:      failures < len(results) || len(results) == 0,
		Message:      fmt.Sprintf("%s: %d of %d request(s) done", action, len(results)-failures, len(results)),
		Participants: results,
	}
}

// ResetInviteLink replaces a group's invite link.
func (b *Bridge) ResetInviteLink(ctx context.Context, chatJID string) GroupChangeResponse {
	group, err := jid.ParseGroup(chatJID)
	if err != nil {
		return groupFailed("%v", err)
	}
	if err := b.checkAllowed(ctx, group); err != nil {
		return groupBlocked(err)
	}
	if !b.Online() {
		return groupFailed("Not connected to WhatsApp")
	}
	link, err := b.client.GetGroupInviteLink(ctx, group, true)
	if err != nil {
		return groupFailed("Failed to reset the invite link (admins only): %v", err)
	}
	return GroupChangeResponse{Success: true, Message: "New invite link: " + link + " (the old one no longer works)"}
}

// SetDisappearing sets the disappearing-messages timer of a chat.
func (b *Bridge) SetDisappearing(ctx context.Context, req DisappearingRequest) SendMessageResponse {
	timer, ok := disappearingTimers[strings.ToLower(strings.TrimSpace(req.Timer))]
	if !ok {
		return failed("timer must be off, 24h, 7d or 90d")
	}
	chat, err := jid.ParseRecipient(req.ChatJID)
	if err != nil {
		return failed("Invalid chat: %v", err)
	}
	if err := b.checkAllowed(ctx, chat); err != nil {
		return blockedResponse(err)
	}
	if !b.Online() {
		return failed("Not connected to WhatsApp")
	}
	if err := b.client.SetDisappearingTimer(ctx, chat, timer, time.Now()); err != nil {
		return failed("Failed to set disappearing messages: %v", err)
	}
	state := "off"
	if timer > 0 {
		state = req.Timer
	}
	return SendMessageResponse{Success: true, Message: fmt.Sprintf("Disappearing messages in %s: %s", chat, state)}
}

// LinkCommunity links a group to a community, or unlinks it.
func (b *Bridge) LinkCommunity(ctx context.Context, req CommunityLinkRequest) GroupChangeResponse {
	community, err := jid.ParseGroup(req.CommunityJID)
	if err != nil {
		return groupFailed("community: %v", err)
	}
	group, err := jid.ParseGroup(req.GroupJID)
	if err != nil {
		return groupFailed("group: %v", err)
	}
	for _, jid := range []types.JID{community, group} {
		if err := b.checkAllowed(ctx, jid); err != nil {
			return groupBlocked(err)
		}
	}
	if !b.Online() {
		return groupFailed("Not connected to WhatsApp")
	}
	verb := "Linked"
	if req.Unlink {
		verb = "Unlinked"
		err = b.client.UnlinkGroup(ctx, community, group)
	} else {
		err = b.client.LinkGroup(ctx, community, group)
	}
	if err != nil {
		return groupFailed("Failed to change the community: %v", err)
	}
	return GroupChangeResponse{Success: true, Message: fmt.Sprintf("%s %s and community %s", verb, group, community)}
}

// SubGroups lists the groups of a community, with the HTTP status to answer.
func (b *Bridge) SubGroups(ctx context.Context, communityJID string) (map[string]any, int) {
	community, err := jid.ParseGroup(communityJID)
	if err != nil {
		return map[string]any{"success": false, "message": err.Error()}, http.StatusBadRequest
	}
	if err := b.checkReadable(ctx, community); err != nil {
		return map[string]any{"success": false, "message": err.Error()}, http.StatusForbidden
	}
	if !b.Online() {
		return map[string]any{"success": false, "message": "Not connected to WhatsApp"}, http.StatusInternalServerError
	}
	targets, err := b.client.GetSubGroups(ctx, community)
	if err != nil {
		return map[string]any{"success": false, "message": fmt.Sprintf("Failed to list groups of the community: %v", err)}, http.StatusInternalServerError
	}
	out := make([]SubGroup, 0, len(targets))
	for _, t := range targets {
		out = append(out, SubGroup{JID: t.JID.String(), Name: t.Name, IsDefault: t.IsDefaultSubGroup})
	}
	return map[string]any{"success": true, "groups": out}, http.StatusOK
}
