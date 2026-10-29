package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/mediaconv"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/storetest"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/media/transcribe"
)

const (
	group  = storetest.Group
	direct = "32547396399312@lid"
)

var jsonOf = storetest.JSONOf

// testStore is the shared fixture with the media toolkit and the log the
// tools of the test run with.
type testStore struct {
	*storetest.Fixture
	media mediaconv.Toolkit
	log   *bytes.Buffer
}

func newStore(t *testing.T) *testStore {
	t.Helper()
	return &testStore{Fixture: storetest.New(t, storetest.Options{}), media: mediaconv.Default(transcribe.Config{CLI: "/nonexistent"}), log: &bytes.Buffer{}}
}

// deps are the tools' dependencies with the fixture's current settings.
func (s *testStore) deps() *Deps {
	return &Deps{Accounts: s.Registry(), Pool: s.Pool, HTTP: bridgeapi.NewHTTPClient(), Scope: s.Scope(), Media: s.media, Log: logging.New(s.log)}
}

// call is a call of the default account with args and the current
// settings: build it after changing them.
func (s *testStore) call(args Args) *Call {
	return s.deps().newCall(s.Account(), args)
}

type noTracker struct{}

func (noTracker) Start(ctx context.Context, _ string) (context.Context, func()) {
	return ctx, func() {}
}

// connectClient serves the tools of the fixture's settings to an
// in-memory client.
func (s *testStore) connectClient(t *testing.T) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "whatsapp", Version: "test"}, nil)
	accounts, err := s.Registry().List()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, name := range Register(srv, s.deps(), noTracker{}, len(accounts) > 1, s.Config.ReadOnly) {
		known[name] = true
	}
	srv.AddReceivingMiddleware(UnknownTool(known))
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(t.Context(), st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// textOf is the text blocks of a result, joined by newlines.
func textOf(res *mcp.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// structuredOf is a result's structured content as compact JSON with
// sorted keys.
func structuredOf(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	data, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	return jsonOf(t, string(data))
}

func writeFile(t *testing.T, path string, data []byte) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func encodePNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for x := range w {
		img.Set(x, x%h, color.NRGBA{R: 200, A: 255})
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
