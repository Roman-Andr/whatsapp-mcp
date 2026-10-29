package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// keepAliveInterval is how often an idle event stream sends a comment, so
// proxies and clients do not time it out.
const keepAliveInterval = 25 * time.Second

// events is the server-sent event stream: one JSON Event per "data:" line.
// Events of chats outside the read allowlist are skipped. The stream ends
// with the request context or when the server shuts down.
func (h *handler) events(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	ch := h.Events.Subscribe()
	defer h.Events.Unsubscribe(ch)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()
	keepalive := time.NewTicker(keepAliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case evt := <-ch:
			// Session events concern the whole account and always pass.
			if evt.ChatJID != "" && h.Bridge.CheckReadable(r.Context(), evt.ChatJID) != nil {
				continue
			}
			data, _ := json.Marshal(evt)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		case <-h.quit:
			return
		}
	}
}
