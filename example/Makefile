# example/ sub-project Makefile (Next.js example app consuming @moduleforge/users-gui).
# Self-contained: implements the canonical target contract used by the root
# Makefile so it can be invoked standalone or via `make -C example <target>`.

.DEFAULT_GOAL := build

# Detect current platform.
CURRENT_OS := $(shell uname -s)

# Package manager: bun.
PM := bun
NPX := bunx

# example/ is not a bun workspace member — it installs its own node_modules.
WORKSPACE_ROOT := .
PLATFORM_STAMP := node_modules/.platform

# ---------------------------------------------------------------------------
# Preflight
# ---------------------------------------------------------------------------

.PHONY: preflight
preflight: ## Check tools + fix stale node_modules
	@printf "  example preflight: "
	@if ! command -v node >/dev/null 2>&1; then \
		echo "FAIL"; \
		echo "ERROR: node is not installed."; \
		exit 1; \
	fi
	@if ! command -v $(PM) >/dev/null 2>&1; then \
		echo "FAIL"; \
		echo "ERROR: bun is not installed."; \
		exit 1; \
	fi
	@NEEDS_INSTALL=false; \
	if [ -f $(PLATFORM_STAMP) ] && [ "$$(cat $(PLATFORM_STAMP))" != "$(CURRENT_OS)" ]; then \
		echo "node_modules installed on $$(cat $(PLATFORM_STAMP)), current platform is $(CURRENT_OS) — reinstalling..."; \
		rm -rf $(WORKSPACE_ROOT)/node_modules node_modules; \
		NEEDS_INSTALL=true; \
	elif [ -d $(WORKSPACE_ROOT)/node_modules ] && ! [ -f $(PLATFORM_STAMP) ]; then \
		echo "node_modules missing platform stamp — reinstalling to be safe..."; \
		rm -rf $(WORKSPACE_ROOT)/node_modules node_modules; \
		NEEDS_INSTALL=true; \
	elif ! [ -d node_modules ] && ! [ -d $(WORKSPACE_ROOT)/node_modules ]; then \
		echo "node_modules missing — installing..."; \
		NEEDS_INSTALL=true; \
	fi; \
	if [ "$$NEEDS_INSTALL" = "true" ]; then \
		bun install; \
		echo "$(CURRENT_OS)" > $(PLATFORM_STAMP); \
	else \
		echo "node=$$(node --version) pm=$(PM) platform=$(CURRENT_OS) ✓"; \
	fi

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

.PHONY: build
build: preflight ## Build the Next.js production bundle
	$(NPX) next build

.PHONY: test
test: test.unit ## Alias for test.unit

.PHONY: test.unit
test.unit: preflight ## Run unit tests (none yet — placeholder passes)
	@echo "example: no unit tests configured yet"

.PHONY: test.integration
test.integration: ## Run integration tests (none yet)
	@echo "example: no integration tests yet"

.PHONY: lint
lint: preflight ## Run next lint
	$(NPX) next lint

.PHONY: lint-fix
lint-fix: preflight ## Run next lint --fix
	$(NPX) next lint --fix

.PHONY: dev.start
dev.start: preflight ## Run Next.js dev server in foreground (requires library published via yalc)
	$(NPX) next dev

.PHONY: dev.stop
dev.stop: ## No-op (dev.start runs in foreground)
	@echo "example: dev.start runs in foreground; nothing to stop"

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf .next

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_.\-]+:.*?## / { printf "  %-20s %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST) | sort
