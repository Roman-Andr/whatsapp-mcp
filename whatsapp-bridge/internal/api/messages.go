package api

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
)

// Validation messages that several routes share.
const (
	msgChatJIDRequired     = "chat_jid is required"
	msgChatAndIDRequired   = "chat_jid and message_id are required"
	msgRecipientIsRequired = "recipient is required"
)

func (h *handler) send(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.SendMessageRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epSend, Chat: req.Recipient, TargetID: req.ReplyToMessageID, Text: req.Message}
	if req.MediaPath != "" {
		entry.Text = strings.TrimSpace("[file " + filepath.Base(req.MediaPath) + "] " + req.Message)
	}
	if req.Recipient == "" {
		h.reject(w, entry, "Recipient is required")
		return
	}
	if req.Message == "" && req.MediaPath == "" {
		h.reject(w, entry, "Message or media path is required")
		return
	}
	resp := h.Bridge.Send(r.Context(), req)
	h.log.Info("send", "success", resp.Success, "message", resp.Message)
	respond(h, w, entry, resp)
}

func (h *handler) react(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.ReactRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epReact, Chat: req.ChatJID, TargetID: req.MessageID, Text: req.Emoji}
	if req.ChatJID == "" || req.MessageID == "" {
		h.reject(w, entry, msgChatAndIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.React(r.Context(), req))
}

func (h *handler) poll(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.PollRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epPoll, Chat: req.Recipient, Text: req.Question + " [" + strings.Join(req.Options, " / ") + "]"}
	if req.Recipient == "" {
		h.reject(w, entry, msgRecipientIsRequired)
		return
	}
	respond(h, w, entry, h.Bridge.SendPoll(r.Context(), req))
}

func (h *handler) location(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.LocationRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epLocation, Chat: req.Recipient, Text: req.Summary()}
	if req.Recipient == "" {
		h.reject(w, entry, msgRecipientIsRequired)
		return
	}
	respond(h, w, entry, h.Bridge.SendLocation(r.Context(), req))
}

func (h *handler) edit(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.EditRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epEdit, Chat: req.ChatJID, TargetID: req.MessageID, Text: req.Message}
	if req.ChatJID == "" || req.MessageID == "" || req.Message == "" {
		h.reject(w, entry, "chat_jid, message_id and message are required")
		return
	}
	respond(h, w, entry, h.Bridge.Edit(r.Context(), req))
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.DeleteRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epDelete, Chat: req.ChatJID, TargetID: req.MessageID}
	if req.ChatJID == "" || req.MessageID == "" {
		h.reject(w, entry, msgChatAndIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.Delete(r.Context(), req))
}

func (h *handler) markRead(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.ReadRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epRead, Chat: req.ChatJID, TargetID: strings.Join(req.MessageIDs, ",")}
	if req.ChatJID == "" {
		h.reject(w, entry, msgChatJIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.MarkRead(r.Context(), req))
}

func (h *handler) typing(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.TypingRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epTyping, Chat: req.ChatJID, Text: req.State}
	if req.ChatJID == "" {
		h.reject(w, entry, msgChatJIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.SendTyping(r.Context(), req))
}

// history asks the phone for older messages. It is neither guarded nor
// audited (B1 in the refactor plan).
func (h *handler) history(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.HistoryRequest](w, r)
	if !ok {
		return
	}
	if req.ChatJID == "" {
		badRequest(w, msgChatJIDRequired)
		return
	}
	if err := h.Bridge.CheckReadable(r.Context(), req.ChatJID); err != nil {
		writeJSON(w, http.StatusForbidden, bridge.SendMessageResponse{Message: err.Error()})
		return
	}
	resp := h.Bridge.RequestHistory(r.Context(), req)
	writeResult(w, resp.Success, resp)
}

func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.DownloadMediaRequest](w, r)
	if !ok {
		return
	}
	if req.MessageID == "" || req.ChatJID == "" {
		badRequest(w, "Message ID and Chat JID are required")
		return
	}
	if err := h.Bridge.CheckReadable(r.Context(), req.ChatJID); err != nil {
		writeJSON(w, http.StatusForbidden, bridge.DownloadMediaResponse{Message: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), downloadTimeout)
	defer cancel()
	resp, err := h.Bridge.DownloadMedia(ctx, req.MessageID, req.ChatJID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, bridge.DownloadMediaResponse{
			Success: false, Message: fmt.Sprintf("Failed to download media: %v", err),
		})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *handler) vote(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.VoteRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epVote, Chat: req.ChatJID, TargetID: req.MessageID, Text: strings.Join(req.Options, " / ")}
	if req.ChatJID == "" || req.MessageID == "" {
		h.reject(w, entry, msgChatAndIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.VotePoll(r.Context(), req))
}

func (h *handler) sendContact(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.ContactRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epContact, Chat: req.Recipient, Text: req.Name + " " + req.Phone}
	if req.Recipient == "" {
		h.reject(w, entry, msgRecipientIsRequired)
		return
	}
	respond(h, w, entry, h.Bridge.SendContact(r.Context(), req))
}

func (h *handler) sticker(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.SendMessageRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epSticker, Chat: req.Recipient, TargetID: req.ReplyToMessageID, Text: "[sticker " + filepath.Base(req.MediaPath) + "]"}
	if req.Recipient == "" || req.MediaPath == "" {
		h.reject(w, entry, "recipient and media_path are required")
		return
	}
	req.Sticker = true
	respond(h, w, entry, h.Bridge.Send(r.Context(), req))
}

func (h *handler) forward(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.ForwardRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epForward, Chat: req.Recipient, TargetID: req.ChatJID + "/" + req.MessageID}
	if req.ChatJID == "" || req.MessageID == "" || req.Recipient == "" {
		h.reject(w, entry, "chat_jid, message_id and recipient are required")
		return
	}
	respond(h, w, entry, h.Bridge.Forward(r.Context(), req))
}

func (h *handler) star(w http.ResponseWriter, r *http.Request) {
	req, ok := decode[bridge.StarRequest](w, r)
	if !ok {
		return
	}
	entry := audit.Entry{Endpoint: epStar, Chat: req.ChatJID, TargetID: req.MessageID}
	if req.ChatJID == "" || req.MessageID == "" {
		h.reject(w, entry, msgChatAndIDRequired)
		return
	}
	respond(h, w, entry, h.Bridge.Star(r.Context(), req))
}
