package api

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
)

// contactInfo answers a read-allowlist refusal with 500, like any other
// failure (B3 in the refactor plan).
func (h *handler) contactInfo(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.ContactInfoRequest](w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	info := h.Bridge.ContactInfo(ctx, req)
	writeResult(w, info.Success, info)
}

func (h *handler) blocklist(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	body, status := h.Bridge.Blocklist(ctx)
	writeJSON(w, status, body)
}

func (h *handler) block(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.BlockRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epBlock, Chat: req.JID, Text: fmt.Sprint(req.Blocked)}
	if req.JID == "" {
		h.reject(w, entry, "jid is required")
		return
	}
	respond(h, w, entry, h.Bridge.SetBlocked(r.Context(), req))
}

// presence answers every error, a read-allowlist refusal included, with 400
// (B3 in the refactor plan).
func (h *handler) presence(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		JID string `json:"jid"`
	}](w, r)
	if !ok {
		return
	}
	info, err := h.Bridge.Presence(r.Context(), req.JID)
	if err != nil {
		writeFailure(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "presence": info})
}
