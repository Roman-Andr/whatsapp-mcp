# The store's full-text index (messages_fts) needs FTS5 compiled into
# go-sqlite3; without the tag searches fall back to substring matching.
TAGS := sqlite_fts5
# The same version CI pins (.github/workflows/ci.yml).
GOLANGCI_LINT := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

.PHONY: build test lint lint-strict

build:
	go build -tags $(TAGS) -trimpath -ldflags "-s -w" -o whatsapp-mcp-go .

# The server and the media packages.
test:
	go vet -tags $(TAGS) ./...
	go test -tags $(TAGS) -count=1 ./...

lint:
	test -z "$$(gofmt -l .)"
	go run $(GOLANGCI_LINT) run --build-tags $(TAGS) ./...

# The configuration the refactor ends with (.golangci.strict.yml).
lint-strict:
	test -z "$$(gofmt -l .)"
	go run $(GOLANGCI_LINT) run -c .golangci.strict.yml --build-tags $(TAGS) ./...

# Where install-service puts the user unit and the token file; systemctl is
# a variable so the targets can be tried out without touching the user's
# systemd (make install-service SERVICE_DIR=$(mktemp -d) CONFIG_DIR=$(mktemp -d) SYSTEMCTL=true).
SERVICE_DIR ?= $(HOME)/.config/systemd/user
CONFIG_DIR ?= $(HOME)/.config/whatsapp-mcp
SYSTEMCTL ?= systemctl
PORT ?= 8091
BIN ?= $(CURDIR)/whatsapp-mcp-go

.PHONY: install-service uninstall-service

# Installs the systemd user unit whatsapp-mcp.service for the built binary
# and, if missing, the env file (mode 0600) with a random WHATSAPP_MCP_TOKEN;
# an existing token is kept. It does not enable or start the service.
install-service:
	@test -x $(BIN) || { echo "No $(BIN): run 'make build' first" >&2; exit 1; }
	mkdir -p $(SERVICE_DIR)
	sed -e 's|@BIN@|$(BIN)|g' -e 's|@SERVER_DIR@|$(CURDIR)|g' -e 's|@CONFIG_DIR@|$(CONFIG_DIR)|g' \
		whatsapp-mcp.service > $(SERVICE_DIR)/whatsapp-mcp.service
	@umask 077; mkdir -p $(CONFIG_DIR); \
	if [ ! -f $(CONFIG_DIR)/env ]; then \
		echo "WHATSAPP_MCP_TOKEN=$$(od -An -N24 -tx1 /dev/urandom | tr -d ' \n')" > $(CONFIG_DIR)/env; \
		echo "Created $(CONFIG_DIR)/env with a new WHATSAPP_MCP_TOKEN"; \
	fi; \
	chmod 600 $(CONFIG_DIR)/env
	$(SYSTEMCTL) --user daemon-reload
	@echo "Installed $(SERVICE_DIR)/whatsapp-mcp.service"
	@echo "Start it:  systemctl --user enable --now whatsapp-mcp   (or: systemctl --user restart whatsapp-mcp)"
	@echo "Logs:      journalctl --user -u whatsapp-mcp -f"
	@echo "Register it in Claude Code (all projects), replacing a stdio 'whatsapp' entry:"
	@echo "  claude mcp remove whatsapp -s user; claude mcp add -s user --transport http whatsapp http://127.0.0.1:$(PORT)/mcp \\"
	@echo "    --header \"Authorization: Bearer \$$(sed -n 's/^WHATSAPP_MCP_TOKEN=//p' $(CONFIG_DIR)/env)\""

# Keeps the env file: it holds the token clients are configured with.
uninstall-service:
	-$(SYSTEMCTL) --user disable --now whatsapp-mcp
	rm -f $(SERVICE_DIR)/whatsapp-mcp.service
	$(SYSTEMCTL) --user daemon-reload
