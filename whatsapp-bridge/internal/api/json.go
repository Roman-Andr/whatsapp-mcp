package api

import (
	"encoding/json"
	"net/http"
)

// maxBody caps a request body; larger bodies fail to decode.
const maxBody = 1 << 20

// requirePost answers 405 to anything but POST.
func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	return true
}

// decode reads a JSON POST body into a T, answering errors itself: 405 for
// another method, 400 for a body that does not decode or is too large.
func decode[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	var req T
	if !requirePost(w, r) {
		return req, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request format", http.StatusBadRequest)
		return req, false
	}
	return req, true
}

// writeJSON answers with v as JSON (json.Encoder adds a trailing newline).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// writeResult answers v with 200, or 500 when the call failed.
func writeResult(w http.ResponseWriter, success bool, v any) {
	status := http.StatusOK
	if !success {
		status = http.StatusInternalServerError
	}
	writeJSON(w, status, v)
}

// writeFailure answers {"message":…,"success":false}.
func writeFailure(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"success": false, "message": msg})
}

// badRequest answers a validation error as plain text.
func badRequest(w http.ResponseWriter, msg string) {
	http.Error(w, msg, http.StatusBadRequest)
}
