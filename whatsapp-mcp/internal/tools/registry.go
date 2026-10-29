package tools

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/account"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/bridgeapi"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/mediaconv"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/pyfmt"
	"github.com/Roman-Andr/whatsapp-mcp/whatsapp-mcp/internal/store"
)

// Deps are what the tools run with; the server owns them.
type Deps struct {
	Accounts *account.Registry
	Pool     *store.Pool
	HTTP     *http.Client
	Scope    store.Scope
	Media    mediaconv.Toolkit
	Log      *slog.Logger
}

func (d *Deps) db(a *account.Account) *store.DB { return store.NewDB(d.Pool, a.StoreDir, d.Scope) }

func (d *Deps) bridge(a *account.Account) *bridgeapi.Client {
	return &bridgeapi.Client{BaseURL: a.APIURL, Token: a.Token, HTTP: d.HTTP}
}

// Call is one tool call: its arguments, the account it runs against and
// that account's store and bridge.
type Call struct {
	Account *account.Account
	DB      *store.DB
	Bridge  *bridgeapi.Client
	Media   mediaconv.Toolkit
	Args    Args
	Log     *slog.Logger

	deps *Deps
}

// newCall is a call of acc with args.
func (d *Deps) newCall(acc *account.Account, a Args) *Call {
	return &Call{Account: acc, DB: d.db(acc), Bridge: d.bridge(acc), Media: d.Media, Args: a, Log: d.Log, deps: d}
}

// Tracker tracks running calls, so they can be cancelled: Start returns
// the context a call of session runs with, and the function to call when
// it ends.
type Tracker interface {
	Start(ctx context.Context, session string) (context.Context, func())
}

// Register adds every tool and returns their names; with several accounts
// each takes `account`. In read-only mode the tools that send or change
// something are left out.
//
// calls tracks the running calls, so they can be cancelled.
func Register(s *mcp.Server, deps *Deps, calls Tracker, multiAccount, readOnly bool) []string {
	var registered []string
	for _, spec := range toolTable {
		if readOnly && !spec.ann.readOnly() {
			continue
		}
		if multiAccount {
			spec = spec.withAccount()
		}
		tool := &mcp.Tool{
			Name:        spec.name,
			Description: spec.doc,
			InputSchema: json.RawMessage(mustJSON(spec.inputSchema())),
			Annotations: spec.ann.annotations(),
		}
		if out := spec.outputSchema(); out != nil {
			tool.OutputSchema = json.RawMessage(mustJSON(out))
		}
		s.AddTool(tool, toolHandler(deps, calls, spec))
		registered = append(registered, spec.name)
	}
	return registered
}

// mustJSON encodes a schema built from the table; it cannot fail.
func mustJSON(v any) []byte {
	b, err := pyfmt.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func toolHandler(deps *Deps, calls Tracker, spec toolSpec) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (res *mcp.CallToolResult, err error) {
		session := ""
		if req.Session != nil {
			session = req.Session.ID()
		}
		ctx, end := calls.Start(ctx, session)
		defer end()
		// A crash in one tool, or in reading its arguments, must not take
		// the server down.
		defer func() {
			if r := recover(); r != nil {
				res, err = panicked(deps.Log, spec.name, r), nil
			}
		}()
		return run(ctx, deps, spec, req.Params.Arguments), nil
	}
}

// run validates the arguments, resolves the account and runs the handler.
func run(ctx context.Context, deps *Deps, spec toolSpec, raw json.RawMessage) *mcp.CallToolResult {
	parsed, err := spec.parseArgs(raw)
	if err != nil {
		return failed(deps.Log, spec.name, err)
	}
	acc, err := deps.Accounts.Resolve(parsed.String("account"))
	if err != nil {
		return failed(deps.Log, spec.name, err)
	}
	v, err := spec.handle(ctx, deps.newCall(acc, parsed))
	if err != nil {
		return failed(deps.Log, spec.name, err)
	}
	res, err := toolResult(spec.out, v)
	if err != nil {
		return failed(deps.Log, spec.name, err)
	}
	return res
}

// UnknownTool answers a call to a tool that does not exist (or is hidden in
// read-only mode) with a failed result instead of a protocol error.
func UnknownTool(known map[string]bool) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method == "tools/call" {
				if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && !known[p.Name] {
					return errorResult("Unknown tool: " + p.Name), nil
				}
			}
			return next(ctx, method, req)
		}
	}
}
