package api

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

const msgReadOnly = "the bridge runs in read-only mode (WHATSAPP_READ_ONLY); nothing can be sent or changed"

// requireToken accepts "X-API-Token" or "Authorization: Bearer <token>". It
// wraps the whole mux, so an unknown path without a token is 401, not 404.
func requireToken(token string, next http.Handler) http.Handler {
	expected := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presented := r.Header.Get("X-API-Token")
		if auth := r.Header.Get("Authorization"); presented == "" && strings.HasPrefix(auth, "Bearer ") {
			presented = strings.TrimPrefix(auth, "Bearer ")
		}
		if subtle.ConstantTimeCompare([]byte(presented), expected) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// guard wraps a state-changing route: WHATSAPP_READ_ONLY refuses it and the
// send rate limit applies to anything that reaches other people. Refusals
// are audited like every other state-changing call, before the body is read.
func (h *handler) guard(endpoint string, kind policy.Kind, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.ReadOnly {
			h.Audit.Record(audit.Entry{Endpoint: endpoint, Result: msgReadOnly})
			writeJSON(w, http.StatusForbidden, bridge.SendMessageResponse{Message: msgReadOnly})
			return
		}
		if kind == policy.ReachesOthers {
			if ok, wait := h.Limiter.Take(); !ok {
				msg := fmt.Sprintf("send rate limit reached (WHATSAPP_SEND_RATE); try again in %s", wait)
				h.Audit.Record(audit.Entry{Endpoint: endpoint, Result: msg})
				w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())))
				writeJSON(w, http.StatusTooManyRequests, bridge.SendMessageResponse{Message: msg})
				return
			}
		}
		next(w, r)
	}
}
