package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/logging"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp-go/internal/pyfmt"
)

// serve registers specs on a server with deps and connects a client.
func serve(t *testing.T, deps *Deps, specs ...toolSpec) *mcp.ClientSession {
	t.Helper()
	srv := mcp.NewServer(&mcp.Implementation{Name: "whatsapp", Version: "test"}, nil)
	for _, spec := range specs {
		srv.AddTool(&mcp.Tool{Name: spec.name, InputSchema: json.RawMessage(mustJSON(spec.inputSchema()))}, toolHandler(deps, noTracker{}, spec))
	}
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

func TestFailuresBecomeErrorResults(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		spec   toolSpec
		args   map[string]any
		text   string
		logged string
	}{
		{"rejected argument", toolSpec{name: "demo", out: outText, params: []param{{name: "n", typ: "integer", required: true}},
			handle: func(context.Context, *Call) (any, error) { return "", nil }},
			map[string]any{"n": "x"},
			"Error executing tool demo: 1 validation error for demoArguments\nn\n  Input should be a valid integer",
			"tool rejected arguments tool=demo err=\"1 validation error for demoArguments\\nn\\n  Input should be a valid integer\"\n"},
		{"invalid argument", listMessagesSpec(), map[string]any{"after": "soon"},
			"Error executing tool list_messages",
			"tool rejected arguments tool=list_messages err=\"Invalid date format for 'after': soon. Please use ISO-8601 format.\"\n"},
		{"failure", toolSpec{name: "demo", out: outText, handle: func(context.Context, *Call) (any, error) { return nil, errors.New("SELECT boom") }},
			nil, "Error executing tool demo", "ERROR tool failed tool=demo err=\"SELECT boom\"\n"},
		{"wrong result type", toolSpec{name: "demo", out: outText, handle: func(context.Context, *Call) (any, error) { return 5, nil }},
			nil, "Error executing tool demo", "ERROR tool failed tool=demo err=\"handler returned the wrong type: int\"\n"},
		{"panic", toolSpec{name: "demo", out: outText, handle: func(context.Context, *Call) (any, error) { panic("oops") }},
			nil, "Error executing tool demo", "ERROR tool panicked tool=demo panic=oops\n"},
		{"unknown account", toolSpec{name: "demo", out: outText, params: []param{accountParam},
			handle: func(context.Context, *Call) (any, error) { return "", nil }},
			map[string]any{"account": "work"}, "Error executing tool demo",
			"ERROR tool failed tool=demo err=\"Unknown WhatsApp account 'work'; configured: default\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newStore(t)
			res := callTool(t, serve(t, s.deps(), tc.spec), tc.spec.name, tc.args)
			if !res.IsError || textOf(res) != tc.text {
				t.Errorf("got %v %q, want %q", res.IsError, textOf(res), tc.text)
			}
			if s.log.String() != tc.logged {
				t.Errorf("logged %q, want %q", s.log.String(), tc.logged)
			}
		})
	}
}

func listMessagesSpec() toolSpec {
	for _, spec := range toolTable {
		if spec.name == "list_messages" {
			return spec
		}
	}
	panic("no list_messages")
}

func TestUnknownToolsAreFailedResults(t *testing.T) {
	t.Parallel()
	s := newStore(t)
	s.Config.ReadOnly = true
	cs := s.connectClient(t)
	for _, name := range []string{"send_message", "no_such_tool"} {
		res := callTool(t, cs, name, map[string]any{})
		if !res.IsError || textOf(res) != "Unknown tool: "+name {
			t.Errorf("%s: %v %q", name, res.IsError, textOf(res))
		}
	}
}

func TestToolResultShapes(t *testing.T) {
	t.Parallel()
	text := "hello"
	cases := []struct {
		name       string
		kind       outputKind
		v          any
		texts      int
		structured string
	}{
		{"optional text, none", outOptText, (*string)(nil), 0, `{"result": null}`},
		{"optional text", outOptText, &text, 1, `{"result": "hello"}`},
		{"dict list", outDictList, []*pyfmt.OMap{pyfmt.NewOMap("a", 1), pyfmt.NewOMap("a", 2)}, 2, `{"result": [{"a": 1}, {"a": 2}]}`},
		{"image refusal", outImage, pyfmt.NewOMap("success", false), 1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			res, err := toolResult(tc.kind, tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Content) != tc.texts {
				t.Errorf("%d content blocks", len(res.Content))
			}
			got := ""
			if res.StructuredContent != nil {
				got = pyfmt.Compact(res.StructuredContent)
			}
			if got != tc.structured {
				t.Errorf("structured %s, want %s", got, tc.structured)
			}
		})
	}
	for _, kind := range []outputKind{outText, outOptText, outChats, outOptChat, outContacts, outResult, outDictList, outImage} {
		if _, err := toolResult(kind, struct{}{}); !errors.Is(err, errWrongType) {
			t.Errorf("kind %d: %v", kind, err)
		}
	}
}

func TestLogLinesAreQuotedPerField(t *testing.T) {
	t.Parallel()
	var b bytes.Buffer
	failed(logging.New(&b), "x", errors.New("a=b c"))
	if got, want := b.String(), "ERROR tool failed tool=x err=\"a=b c\"\n"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
