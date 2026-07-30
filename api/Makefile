# api/ sub-project Makefile (Go).
# Self-contained: implements the canonical target contract used by the root
# Makefile so it can be invoked standalone or via `make -C api <target>`.

.DEFAULT_GOAL := build

BIN_DIR := bin
SERVER_BIN := $(BIN_DIR)/server

# ---------------------------------------------------------------------------
# Preflight — verify tools
# ---------------------------------------------------------------------------

.PHONY: preflight
preflight: ## Check required tools for api
	@printf "  api preflight: "
	@if ! command -v go >/dev/null 2>&1; then \
		echo "FAIL"; \
		echo "ERROR: go is not installed."; \
		exit 1; \
	fi
	@echo "go=$$(go version | awk '{print $$3}') ✓"

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

.PHONY: build
build: preflight ## Build the api server binary
	@mkdir -p $(BIN_DIR)
	go build -o $(SERVER_BIN) ./cmd/server

.PHONY: test
test: test.unit ## Alias for test.unit

.PHONY: test.unit
test.unit: ## Run unit tests
	go test ./...

.PHONY: test.integration
# Scoped to ./internal/authz/... rather than ./... : running the tagged
# suite across the whole module surfaces a pre-existing, unrelated flaky
# failure in ./auth (TestNewStepUpConsumedCache_JanitorStopsOnCancel, a
# goroutine-count race) that only reproduces when run as part of the full
# `-p 1 ./...` sequence, not standalone — see follow-up filed for the
# authz-single-row-own plan. Requires the shared users-module-postgres
# Postgres container (make dev.start or equivalent) and goose on PATH;
# skips cleanly (exit 0) when prerequisites are absent.
test.integration: ## Run integration tests (requires docker; see internal/authz package doc)
	go test -tags=integration -p 1 ./internal/authz/...

.PHONY: lint
lint: ## Run go vet, check gofmt, and check for server.Error literal-call drift
	go vet ./...
	@test -z "$$(gofmt -l .)" || { echo "Files need gofmt:"; gofmt -l .; exit 1; }
	@./scripts/check-server-error-literals.sh

.PHONY: lint-fix
lint-fix: ## Run gofmt -w on the source tree
	gofmt -w .

.PHONY: dev.start
dev.start: build ## Run api server in foreground
	./$(SERVER_BIN)

.PHONY: dev.stop
dev.stop: ## No-op (api dev.start runs in foreground)
	@echo "api: dev.start runs in foreground; nothing to stop"

.PHONY: clean
clean: ## Remove build artifacts (preserves source/generated code)
	rm -rf $(BIN_DIR)

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_.\-]+:.*?## / { printf "  %-20s %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST) | sort
