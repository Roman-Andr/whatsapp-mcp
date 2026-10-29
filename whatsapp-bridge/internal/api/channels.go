package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
)

const msgNotConnected = "Not connected to WhatsApp"

func (h *handler) channels(w http.ResponseWriter, r *http.Request) {
	if !h.Bridge.Online() {
		writeFailure(w, http.StatusInternalServerError, msgNotConnected)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	list, err := h.Bridge.Channels(ctx)
	if err != nil {
		writeFailure(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "channels": list})
}

func (h *handler) channelMessages(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		ChannelJID string `json:"channel_jid"`
		Count      int    `json:"count"`
		Before     int    `json:"before_server_id"`
	}](w, r)
	if !ok {
		return
	}
	if h.Bridge.CheckReadable(r.Context(), req.ChannelJID) != nil {
		writeFailure(w, http.StatusForbidden, fmt.Sprintf("channel %s is not in the read allowlist", req.ChannelJID))
		return
	}
	if !h.Bridge.Online() {
		writeFailure(w, http.StatusInternalServerError, msgNotConnected)
		return
	}
	msgs, err := h.Bridge.ChannelMessages(r.Context(), req.ChannelJID, req.Count, req.Before)
	if err != nil {
		writeFailure(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "messages": msgs})
}

func (h *handler) followChannel(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.ChannelFollowRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epChannelFollow, Chat: req.Channel, Text: fmt.Sprint(req.Follow)}
	if req.Channel == "" {
		h.reject(w, entry, "channel is required")
		return
	}
	respond(h, w, entry, h.Bridge.FollowChannel(r.Context(), req))
}
