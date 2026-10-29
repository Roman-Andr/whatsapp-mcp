package api

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-bridge/internal/events"
)

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-API-Token", "tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestServerStartsAndShutsDown(t *testing.T) {
	t.Parallel()
	fake := &fakeService{status: map[string]any{"connected": false}}
	d := Deps{Bridge: fake, Token: "tok", Events: events.NewHub(nil)}
	s, err := Start(t.Context(), "127.0.0.1:0", d)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + s.addr.String()

	// The port is taken while the server runs.
	if second, err := Start(t.Context(), s.addr.String(), d); err == nil {
		second.Close()
		t.Error("second server on the same port started")
	}

	resp := get(t, base+"/api/status")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != `{"connected":false}`+"\n" {
		t.Errorf("status %d %q", resp.StatusCode, body)
	}

	// An open event stream does not hold up the shutdown.
	stream := get(t, base+"/api/events")
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != ": connected\n" {
		t.Fatalf("stream %q %v", line, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Errorf("shutdown: %v", err)
	}
	if rest, _ := io.ReadAll(reader); string(rest) != "\n" {
		t.Errorf("stream after shutdown %q", rest)
	}
	if err := s.Close(); err != nil {
		t.Errorf("close after shutdown: %v", err)
	}
}

func TestServerCloses(t *testing.T) {
	t.Parallel()
	s, err := Start(t.Context(), "127.0.0.1:0", Deps{Bridge: &fakeService{}, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Errorf("close: %v", err)
	}
	if _, err := Start(t.Context(), "256.0.0.1:0", Deps{}); err == nil {
		t.Error("bad address accepted")
	}
}
