package api

import (
	"net/http"
	"strconv"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
)

func (h *handler) chatAction(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.ChatActionRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epChat, Chat: req.ChatJID, Text: req.Action}
	if req.ChatJID == "" {
		h.reject(w, entry, msgChatJIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.ChatAction(r.Context(), req))
}

func (h *handler) labelChat(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.LabelChatRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epLabelChat, Chat: req.ChatJID, Text: req.Label + " " + strconv.FormatBool(req.Labeled)}
	if req.ChatJID == "" {
		h.reject(w, entry, msgChatJIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.LabelChat(r.Context(), req))
}

func (h *handler) editLabel(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.LabelEditRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epLabelEdit, TargetID: req.LabelID, Text: req.Name}
	respond(h, w, entry, h.Bridge.EditLabel(r.Context(), req))
}
