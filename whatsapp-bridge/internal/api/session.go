package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

func (h *handler) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, h.Bridge.Status())
}

// qr answers the pairing state as JSON, or with ?format=png the QR code.
func (h *handler) qr(w http.ResponseWriter, r *http.Request) {
	info := h.Bridge.Pairing()
	if r.URL.Query().Get("format") != "png" {
		writeJSON(w, http.StatusOK, info)
		return
	}
	if info.QRCode == "" {
		http.Error(w, "No QR code to show (already linked, or not generated yet)", http.StatusNotFound)
		return
	}
	data, err := h.Bridge.QRImage()
	if err != nil {
		http.Error(w, "QR image not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[struct {
		Confirm bool `json:"confirm"`
	}](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epLogout}
	if !req.Confirm {
		h.reject(w, entry, `logout unlinks this device from the account; send {"confirm": true}`)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), lookupTimeout)
	defer cancel()
	resp := h.Bridge.Logout(ctx)
	respond(h, w, entry, resp)
	if resp.Success {
		// The device store is gone; restarting is the only way to pair again.
		h.Bridge.RequestRestart()
	}
}

// reconnect cycles the WhatsApp connection. It is not guarded, so it works
// in read-only mode (B2 in the refactor plan), and has its own throttle; the
// body is ignored. The connection state itself is in /api/status.
func (h *handler) reconnect(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	entry := audit.Entry{Endpoint: epReconnect}
	err := h.Bridge.Reconnect()
	var tooSoon *wa.TooSoonError
	switch {
	case err == nil:
		respond(h, w, entry, bridge.SendMessageResponse{Success: true,
			Message: "Reconnected to WhatsApp; logging in continues in the background, check /api/status"})
	case errors.Is(err, bridge.ErrReconnectUnavailable):
		respond(h, w, entry, failed("%v", err))
	case errors.Is(err, wa.ErrNotLinked):
		entry.Result = err.Error()
		h.Audit.Record(entry)
		writeJSON(w, http.StatusConflict, bridge.SendMessageResponse{Message: err.Error()})
	case errors.As(err, &tooSoon):
		entry.Result = err.Error()
		h.Audit.Record(entry)
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(tooSoon.Wait.Seconds()))))
		writeJSON(w, http.StatusTooManyRequests, bridge.SendMessageResponse{Message: err.Error()})
	default:
		respond(h, w, entry, failed("Reconnect failed: %v", err))
	}
}
