package api

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

func TestReadOnlyModeRefusesEveryChange(t *testing.T) {
	b, handler := newTestBridge(t)
	b.readOnly = true
	for path, body := range map[string]string{
		"/api/send":   `{"recipient":"79966764464","message":"x"}`,
		"/api/react":  `{"chat_jid":"120@g.us","message_id":"G1","emoji":"👍"}`,
		"/api/read":   `{"chat_jid":"120@g.us"}`,
		"/api/typing": `{"chat_jid":"120@g.us"}`,
	} {
		rec := call(handler, path, body)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "read-only") {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}
	entries := readAudit(t)
	if len(entries) != 4 || entries[0].Success {
		t.Errorf("audit %+v", entries)
	}
}

func TestRateLimitAnswers429AndSparesReceipts(t *testing.T) {
	b, handler := newTestBridge(t)
	b.limiter = policy.NewRateLimiter(1, 1)
	// The first send passes the limiter and fails later (no connection).
	if rec := call(handler, "/api/send", `{"recipient":"79966764464","message":"1"}`); rec.Code == http.StatusTooManyRequests {
		t.Fatalf("first send limited: %s", rec.Body)
	}
	rec := call(handler, "/api/send", `{"recipient":"79966764464","message":"2"}`)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "rate limit") {
		t.Errorf("second send: %d %s", rec.Code, rec.Body)
	}
	if rec := call(handler, "/api/typing", `{"chat_jid":"79966764464","state":"bogus"}`); rec.Code == http.StatusTooManyRequests {
		t.Error("typing was rate limited")
	}
}

func TestRequireToken(t *testing.T) {
	t.Parallel()
	api := newFakeAPI(t, &fakeService{}, func(d *Deps) { d.Token = "secret" })
	cases := []struct {
		name    string
		path    string
		headers map[string]string
		code    int
		want    string
	}{
		{"no token", "/api/status", nil, 401, "Unauthorized\n"},
		{"wrong bearer", "/api/status", map[string]string{"Authorization": "Bearer wrong"}, 401, "Unauthorized\n"},
		{"prefix of the token", "/api/status", map[string]string{"X-API-Token": "secre"}, 401, "Unauthorized\n"},
		{"bearer without the scheme", "/api/status", map[string]string{"Authorization": "secret"}, 401, "Unauthorized\n"},
		{"lower-case scheme", "/api/status", map[string]string{"Authorization": "bearer secret"}, 401, "Unauthorized\n"},
		{"X-API-Token wins over a good bearer", "/api/status", map[string]string{"X-API-Token": "wrong", "Authorization": "Bearer secret"}, 401, "Unauthorized\n"},
		{"unknown path without a token", "/api/nope", nil, 401, "Unauthorized\n"},
		{"unknown path with a token", "/api/nope", map[string]string{"X-API-Token": "secret"}, 404, "404 page not found\n"},
		{"bearer", "/api/status", map[string]string{"Authorization": "Bearer secret"}, 200, "null\n"},
		{"X-API-Token", "/api/status", map[string]string{"X-API-Token": "secret"}, 200, "null\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			for k, v := range c.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()
			api.handler.ServeHTTP(rec, req)
			if rec.Code != c.code || rec.Body.String() != c.want {
				t.Errorf("%d %q, want %d %q", rec.Code, rec.Body.String(), c.code, c.want)
			}
		})
	}
}

func TestRouteTable(t *testing.T) {
	t.Parallel()
	routes := newHandler(Deps{}).routes()
	if len(routes) != 42 {
		t.Errorf("%d routes, want 42", len(routes))
	}
	kinds := map[policy.Kind]int{}
	seen := map[string]bool{}
	for _, r := range routes {
		kinds[r.kind]++
		if seen[r.path] {
			t.Errorf("%s registered twice", r.path)
		}
		seen[r.path] = true
		if strings.Contains(r.path, " ") {
			t.Errorf("%s: method in the pattern", r.path)
		}
		if r.kind != noGuard && r.endpoint == "" {
			t.Errorf("%s: guarded without an audit endpoint", r.path)
		}
	}
	want := map[policy.Kind]int{policy.ReachesOthers: 19, policy.SignalOnly: 2, policy.OwnAccount: 7, noGuard: 14}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("kind %d: %d routes, want %d", k, kinds[k], n)
		}
	}
	// The two routes that have never been guarded stay unguarded (B1, B2).
	for _, r := range routes {
		if slices.Contains([]string{"/api/history", "/api/connection/reconnect"}, r.path) && r.kind != noGuard {
			t.Errorf("%s is guarded", r.path)
		}
	}
}
