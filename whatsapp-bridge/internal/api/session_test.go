package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/audit"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/bridge"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// qrPNGPath is where the bridge saves the QR image.
const qrPNGPath = "store/qr.png"

func getQR(handler http.Handler, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/qr"+query, nil)
	req.Header.Set("X-API-Token", "tok")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestQRCodeIsServedWhileNotLinked(t *testing.T) {
	b, handler := newTestBridge(t)
	b.pairing = wa.NewPairing(qrPNGPath)
	if err := b.pairing.SetQR("2@abc,def,ghi"); err != nil {
		t.Fatal(err)
	}
	b.pairing.SetPairingCode("ABCD-EFGH", nil)
	st, err := os.Stat(qrPNGPath)
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("qr.png: %v %v", st, err)
	}

	var info bridge.PairingInfo
	json.Unmarshal(getQR(handler, "").Body.Bytes(), &info)
	if info.Linked || info.QRCode != "2@abc,def,ghi" || info.PairingCode != "ABCD-EFGH" || info.QRImage != qrPNGPath {
		t.Errorf("info %+v", info)
	}
	rec := getQR(handler, "?format=png")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.HasPrefix(rec.Body.Bytes(), []byte("\x89PNG")) {
		t.Errorf("png: %d %s", rec.Code, rec.Header())
	}

	b.pairing.Done()
	if _, err := os.Stat(qrPNGPath); !os.IsNotExist(err) {
		t.Error("qr.png left after pairing")
	}
	if rec := getQR(handler, "?format=png"); rec.Code != http.StatusNotFound {
		t.Errorf("png after pairing: %d", rec.Code)
	}
}

func TestLogoutNeedsConfirmationAndIsGuarded(t *testing.T) {
	b, handler := newTestBridge(t)
	rec := call(handler, "/api/logout", `{}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "confirm") {
		t.Errorf("unconfirmed: %d %s", rec.Code, rec.Body)
	}
	b.readOnly = true
	if rec := call(handler, "/api/logout", `{"confirm":true}`); rec.Code != http.StatusForbidden {
		t.Errorf("read-only logout: %d %s", rec.Code, rec.Body)
	}
}

func TestReconnectIsThrottledAndNeedsAPairedDevice(t *testing.T) {
	b, handler := newTestBridge(t)
	linked := false
	cycles := 0
	var cycleErr error
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	b.reconnector = wa.NewReconnector(func() bool { return linked }, func() error { cycles++; return cycleErr })
	b.reconnector.Now = func() time.Time { return now }

	rec := call(handler, "/api/connection/reconnect", `{}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "pair the device first") || cycles != 0 {
		t.Errorf("not linked: %d %s (cycles %d)", rec.Code, rec.Body, cycles)
	}

	linked = true
	rec = call(handler, "/api/connection/reconnect", `{}`)
	if rec.Code != http.StatusOK || cycles != 1 {
		t.Errorf("first: %d %s (cycles %d)", rec.Code, rec.Body, cycles)
	}
	now = now.Add(10 * time.Second)
	rec = call(handler, "/api/connection/reconnect", `{}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "20" || cycles != 1 {
		t.Errorf("too soon: %d %s retry-after %q (cycles %d)", rec.Code, rec.Body, rec.Header().Get("Retry-After"), cycles)
	}

	// A failed attempt is throttled like a successful one.
	now = now.Add(wa.MinReconnectInterval)
	cycleErr = errors.New("websocket refused")
	rec = call(handler, "/api/connection/reconnect", `{}`)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "websocket refused") || cycles != 2 {
		t.Errorf("failure: %d %s (cycles %d)", rec.Code, rec.Body, cycles)
	}
	if rec := call(handler, "/api/connection/reconnect", `{}`); rec.Code != http.StatusTooManyRequests || cycles != 2 {
		t.Errorf("retry after failure: %d (cycles %d)", rec.Code, cycles)
	}

	entries := readAudit(t)
	if len(entries) != 5 || entries[0].Endpoint != "reconnect" || entries[1].Success != true || entries[2].Success {
		t.Errorf("audit %+v", entries)
	}
}

func TestReconnectAnswersWithoutReconnector(t *testing.T) {
	_, handler := newTestBridge(t)
	if rec := call(handler, "/api/connection/reconnect", `{}`); rec.Code != http.StatusInternalServerError {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestSessionRoutes(t *testing.T) {
	t.Parallel()
	tooSoon := &wa.TooSoonError{Wait: 12*time.Second + 300*time.Millisecond}
	reconnected := "Reconnected to WhatsApp; logging in continues in the background, check /api/status"
	restarted := func(want bool) func(*testing.T, *fakeService) {
		return func(t *testing.T, f *fakeService) {
			t.Helper()
			if f.restarted != want {
				t.Errorf("restart requested %v, want %v", f.restarted, want)
			}
		}
	}
	runRouteCases(t, []routeCase{
		{name: "status", method: http.MethodGet, path: "/api/status",
			fake: func(f *fakeService) { f.status = map[string]any{"connected": true, "session": "connected"} },
			code: 200, want: `{"connected":true,"session":"connected"}` + "\n"},
		{name: "qr json", method: http.MethodGet, path: "/api/qr",
			fake: func(f *fakeService) { f.pairing = bridge.PairingInfo{QRCode: "2@abc"} },
			code: 200, want: `{"linked":false,"qr_code":"2@abc"}` + "\n"},
		{name: "qr png without code", method: http.MethodGet, path: "/api/qr?format=png",
			code: 404, want: "No QR code to show (already linked, or not generated yet)\n",
			header: map[string]string{"Content-Type": "text/plain; charset=utf-8", "X-Content-Type-Options": "nosniff"}},
		{name: "qr png missing", method: http.MethodGet, path: "/api/qr?format=png",
			fake: func(f *fakeService) { f.pairing, f.err = bridge.PairingInfo{QRCode: "2@abc"}, errors.New("gone") },
			code: 404, want: "QR image not available\n"},
		{name: "qr png", method: http.MethodGet, path: "/api/qr?format=png",
			fake: func(f *fakeService) { f.pairing, f.qr = bridge.PairingInfo{QRCode: "2@abc"}, []byte("\x89PNG") },
			code: 200, want: "\x89PNG", header: map[string]string{"Content-Type": "image/png", "Cache-Control": "no-store"}},
		{name: "logout unconfirmed", path: "/api/logout", body: `{}`,
			code: 400, want: "logout unlinks this device from the account; send {\"confirm\": true}\n",
			audit: []audit.Entry{{Endpoint: "logout", Result: `logout unlinks this device from the account; send {"confirm": true}`}},
			check: restarted(false)},
		{name: "logout failure", path: "/api/logout", body: `{"confirm":true}`, fake: answer(sendFailed),
			code: 500, want: failedBody, calls: []string{"Logout"}, timeout: lookupTimeout,
			audit: []audit.Entry{{Endpoint: "logout", Result: "Not connected to WhatsApp"}}, check: restarted(false)},
		{name: "logout ok restarts", path: "/api/logout", body: `{"confirm":true}`,
			fake: answer(bridge.SendMessageResponse{Success: true, Message: "Logged out"}),
			code: 200, want: `{"success":true,"message":"Logged out"}` + "\n", calls: []string{"Logout"}, timeout: lookupTimeout,
			audit: []audit.Entry{{Endpoint: "logout", Success: true, Result: "Logged out"}}, check: restarted(true)},
		{name: "logout read-only", path: "/api/logout", body: `{"confirm":true}`, deps: readOnly,
			code: 403, want: readOnlyBody, audit: []audit.Entry{{Endpoint: "logout", Result: msgReadOnly}}},
		{name: "reconnect GET", method: http.MethodGet, path: "/api/connection/reconnect", code: 405, want: "Method not allowed\n"},
		{name: "reconnect ignores the body", path: "/api/connection/reconnect", body: `not json`, deps: readOnly,
			code: 200, want: `{"success":true,"message":"` + reconnected + `"}` + "\n", calls: []string{"Reconnect"},
			audit: []audit.Entry{{Endpoint: "reconnect", Success: true, Result: reconnected}}},
		{name: "reconnect unavailable", path: "/api/connection/reconnect",
			fake: func(f *fakeService) { f.err = bridge.ErrReconnectUnavailable },
			code: 500, want: `{"success":false,"message":"reconnecting is not available"}` + "\n", calls: []string{"Reconnect"},
			audit: []audit.Entry{{Endpoint: "reconnect", Result: "reconnecting is not available"}}},
		{name: "reconnect not linked", path: "/api/connection/reconnect",
			fake: func(f *fakeService) { f.err = wa.ErrNotLinked },
			code: 409, want: `{"success":false,"message":"` + wa.ErrNotLinked.Error() + `"}` + "\n", calls: []string{"Reconnect"},
			audit: []audit.Entry{{Endpoint: "reconnect", Result: wa.ErrNotLinked.Error()}}},
		{name: "reconnect too soon rounds the wait up", path: "/api/connection/reconnect",
			fake: func(f *fakeService) { f.err = tooSoon },
			code: 429, want: `{"success":false,"message":"` + tooSoon.Error() + `"}` + "\n", header: map[string]string{"Retry-After": "13"},
			calls: []string{"Reconnect"}, audit: []audit.Entry{{Endpoint: "reconnect", Result: tooSoon.Error()}}},
		{name: "reconnect failure", path: "/api/connection/reconnect",
			fake: func(f *fakeService) { f.err = errors.New("websocket refused") },
			code: 500, want: `{"success":false,"message":"Reconnect failed: websocket refused"}` + "\n", calls: []string{"Reconnect"},
			audit: []audit.Entry{{Endpoint: "reconnect", Result: "Reconnect failed: websocket refused"}}},
	})
}
