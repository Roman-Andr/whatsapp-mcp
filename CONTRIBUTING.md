# Contributing

Bug reports, fixes and features are welcome. For anything larger than a fix, open an issue first so we can agree on the approach.

## Setup

Go 1.26+ and a C compiler (SQLite is built with cgo). See the [README](README.md#quick-start) to build and link a bridge. Never test against an account you cannot afford to lose: the tests use fixtures and fakes, not a live WhatsApp connection.

## Before opening a pull request

```bash
cd whatsapp-bridge && make test
cd whatsapp-bridge && gofmt -l . && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run --build-tags sqlite_fts5
cd whatsapp-mcp-go && make test && make lint
```

CI runs the same checks on every pull request.

- Add or update tests for what you change, in the style of the existing test files.
- Update the README for anything user-visible: a tool, a route, a setting, a limitation.
- A new MCP tool goes into `whatsapp-mcp-go/internal/tools/table.go`, naming its handler (in the file of its area), with the right annotation (read, send, own chat list, signal); anything that changes state goes through the bridge API, where the allowlists, read-only mode and rate limit apply.
- Use [Conventional Commits](https://www.conventionalcommits.org/) (`feat(bridge): …`, `fix(mcp): …`, `docs: …`).

Report security problems privately, as described in [SECURITY.md](SECURITY.md).
