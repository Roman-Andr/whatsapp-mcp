package api

import (
	"fmt"
	"net/http"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
)

// response is a service answer to a state-changing call.
type response interface {
	bridge.SendMessageResponse | bridge.GroupChangeResponse
}

// outcome is what respond reads from a response.
type outcome struct {
	success, refused   bool
	message, messageID string
}

func outcomeOf[T response](resp T) outcome {
	switch r := any(resp).(type) {
	case bridge.SendMessageResponse:
		return outcome{success: r.Success, refused: r.Refused, message: r.Message, messageID: r.MessageID}
	case bridge.GroupChangeResponse:
		return outcome{success: r.Success, refused: r.Refused, message: r.Message}
	}
	return outcome{}
}

// respond answers a state-changing call and appends it to the audit log.
// entry carries what the call was about; the outcome is filled in here. A
// refusal (send allowlist, media roots) is 403, a failure 500.
func respond[T response](h *handler, w http.ResponseWriter, entry audit.Entry, resp T) {
	out := outcomeOf(resp)
	entry.Success = out.success
	entry.Result = out.message
	if entry.MessageID == "" {
		entry.MessageID = out.messageID
	}
	h.Audit.Record(entry)
	status := http.StatusOK
	switch {
	case out.refused:
		status = http.StatusForbidden
	case !out.success:
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, resp)
}

// reject answers a state-changing call that failed validation, and audits it.
func (h *handler) reject(w http.ResponseWriter, entry audit.Entry, msg string) {
	entry.Result = msg
	h.Audit.Record(entry)
	badRequest(w, msg)
}

// failed is a failed state-changing call.
func failed(format string, args ...any) bridge.SendMessageResponse {
	return bridge.SendMessageResponse{Success: false, Message: fmt.Sprintf(format, args...)}
}
