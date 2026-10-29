package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
)

func (h *handler) groupInfo(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.GroupInfoRequest](w, r)
	if !ok {
		return
	}
	if req.ChatJID == "" {
		badRequest(w, msgChatJIDRequired)
		return
	}
	if err := h.Bridge.CheckReadable(r.Context(), req.ChatJID); err != nil {
		writeJSON(w, http.StatusForbidden, bridge.GroupInfoResponse{Message: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	resp := h.Bridge.GroupInfo(ctx, req)
	writeResult(w, resp.Success, resp)
}

// respondGroupChange runs a group change with its timeout and answers it.
func (h *handler) respondGroupChange(w http.ResponseWriter, r *http.Request, entry audit.Entry, run func(context.Context) bridge.GroupChangeResponse) {
	ctx, cancel := context.WithTimeout(r.Context(), groupChangeTimeout)
	defer cancel()
	respond(h, w, entry, run(ctx))
}

func (h *handler) createGroup(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.CreateGroupRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epGroupCreate, Text: req.Name + " [" + strings.Join(req.Participants, ", ") + "]"}
	h.respondGroupChange(w, r, entry, func(ctx context.Context) bridge.GroupChangeResponse { return h.Bridge.CreateGroup(ctx, req) })
}

func (h *handler) leaveGroup(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.GroupChatRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epGroupLeave, Chat: req.ChatJID}
	h.respondGroupChange(w, r, entry, func(ctx context.Context) bridge.GroupChangeResponse { return h.Bridge.LeaveGroup(ctx, req) })
}

func (h *handler) groupParticipants(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.GroupParticipantsRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epGroupParticipant, Chat: req.ChatJID, Text: req.Action + " " + strings.Join(req.Participants, ", ")}
	h.respondGroupChange(w, r, entry, func(ctx context.Context) bridge.GroupChangeResponse { return h.Bridge.UpdateParticipants(ctx, req) })
}

func (h *handler) groupSettings(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.GroupSettingsRequest](w, r)
	if !ok {
		return
	}
	text, _ := json.Marshal(req)
	entry := audit.Entry{Endpoint: epGroupSettings, Chat: req.ChatJID, Text: string(text)}
	h.respondGroupChange(w, r, entry, func(ctx context.Context) bridge.GroupChangeResponse { return h.Bridge.UpdateGroupSettings(ctx, req) })
}

func (h *handler) joinGroup(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.JoinGroupRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epGroupJoin, Text: req.Invite}
	h.respondGroupChange(w, r, entry, func(ctx context.Context) bridge.GroupChangeResponse { return h.Bridge.JoinGroup(ctx, req) })
}

func (h *handler) groupRequests(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.GroupChatRequest](w, r)
	if !ok {
		return
	}
	body, status := h.Bridge.GroupRequests(r.Context(), req.ChatJID)
	writeJSON(w, status, body)
}

// updateGroupRequests, resetInvite and linkCommunity have no field checks
// and no timeout of their own (B4 in the refactor plan).
func (h *handler) updateGroupRequests(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.GroupRequestsUpdate](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epGroupRequests, Chat: req.ChatJID, Text: req.Action + " " + strings.Join(req.Participants, ", ")}
	respond(h, w, entry, h.Bridge.UpdateGroupRequests(r.Context(), req))
}

func (h *handler) resetInvite(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.GroupChatRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epGroupInviteReset, Chat: req.ChatJID}
	respond(h, w, entry, h.Bridge.ResetInviteLink(r.Context(), req.ChatJID))
}

func (h *handler) disappearing(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.DisappearingRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epDisappearing, Chat: req.ChatJID, Text: req.Timer}
	if req.ChatJID == "" {
		h.reject(w, entry, msgChatJIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.SetDisappearing(r.Context(), req))
}

func (h *handler) linkCommunity(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.CommunityLinkRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epCommunityLink, Chat: req.CommunityJID, TargetID: req.GroupJID}
	respond(h, w, entry, h.Bridge.LinkCommunity(r.Context(), req))
}

func (h *handler) communityGroups(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		CommunityJID string `json:"community_jid"`
	}](w, r)
	if !ok {
		return
	}
	body, status := h.Bridge.SubGroups(r.Context(), req.CommunityJID)
	writeJSON(w, status, body)
}
