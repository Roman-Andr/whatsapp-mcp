# FTS5 is compiled into go-sqlite3 only with the sqlite_fts5 tag; without it
# the bridge still runs but full-text search falls back to LIKE.
TAGS := sqlite_fts5
# The same version CI pins (.github/workflows/ci.yml).
GOLANGCI_LINT := github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

# Where install-service puts the user unit; systemctl is a variable so the
# target can be tried out without touching the user's systemd.
SERVICE_DIR ?= $(HOME)/.config/systemd/user
SYSTEMCTL ?= systemctl

.PHONY: build test lint lint-strict run install-service uninstall-service install-account-service

build:
	go build -tags $(TAGS) -o whatsapp-bridge .

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

run: build
	./whatsapp-bridge

# Installs the systemd user unit for this checkout; it does not enable or
# start it.
install-service: build
	mkdir -p $(SERVICE_DIR)
	sed 's|@BRIDGE_DIR@|$(CURDIR)|g' whatsapp-bridge.service > $(SERVICE_DIR)/whatsapp-bridge.service
	$(SYSTEMCTL) --user daemon-reload
	@echo "Installed $(SERVICE_DIR)/whatsapp-bridge.service"
	@echo "Stop any bridge started by hand, then: systemctl --user enable --now whatsapp-bridge"
	@echo "Logs: journalctl --user -u whatsapp-bridge -f"

uninstall-service:
	-$(SYSTEMCTL) --user disable --now whatsapp-bridge
	rm -f $(SERVICE_DIR)/whatsapp-bridge.service
	$(SYSTEMCTL) --user daemon-reload

# Template unit for running several accounts (whatsapp-bridge@<name>).
install-account-service: build
	mkdir -p $(SERVICE_DIR)
	sed 's|@BRIDGE_DIR@|$(CURDIR)|g' whatsapp-bridge@.service > $(SERVICE_DIR)/whatsapp-bridge@.service
	$(SYSTEMCTL) --user daemon-reload
	@echo "Installed $(SERVICE_DIR)/whatsapp-bridge@.service"
	@echo "Per account: ~/.config/whatsapp-bridge/<name>.env with WHATSAPP_BRIDGE_ADDR=127.0.0.1:<port>, then"
	@echo "  systemctl --user enable --now whatsapp-bridge@<name>"
