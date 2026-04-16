# gui/ sub-project Makefile (Next.js).
# Self-contained: implements the canonical target contract used by the root
# Makefile so it can be invoked standalone or via `make -C gui <target>`.

.DEFAULT_GOAL := build

# Detect current platform (e.g. "Darwin" on macOS, "Linux" in containers).
CURRENT_OS := $(shell uname -s)

# Auto-detect package manager: pnpm > npm.
PM := $(shell if command -v pnpm >/dev/null 2>&1; then echo pnpm; else echo npm; fi)
NPX := npx

# Stamp file records the OS that installed node_modules.
PLATFORM_STAMP := node_modules/.platform

# ---------------------------------------------------------------------------
# Preflight — verify tools, fix cross-platform node_modules
# ---------------------------------------------------------------------------

.PHONY: preflight
preflight: ## Check tools + fix stale node_modules
	@printf "  gui preflight: "
	@if ! command -v node >/dev/null 2>&1; then \
		echo "FAIL"; \
		echo "ERROR: node is not installed."; \
		exit 1; \
	fi
	@if ! command -v $(PM) >/dev/null 2>&1; then \
		echo "FAIL"; \
		echo "ERROR: neither pnpm nor npm found."; \
		exit 1; \
	fi
	@if [ -d node_modules ] && [ -f $(PLATFORM_STAMP) ] && [ "$$(cat $(PLATFORM_STAMP))" != "$(CURRENT_OS)" ]; then \
		echo "node_modules installed on $$(cat $(PLATFORM_STAMP)), current platform is $(CURRENT_OS) — reinstalling..."; \
		rm -rf node_modules; \
		$(PM) install; \
		echo "$(CURRENT_OS)" > $(PLATFORM_STAMP); \
	elif [ -d node_modules ] && ! [ -f $(PLATFORM_STAMP) ]; then \
		echo "node_modules missing platform stamp — reinstalling to be safe..."; \
		rm -rf node_modules; \
		$(PM) install; \
		echo "$(CURRENT_OS)" > $(PLATFORM_STAMP); \
	elif ! [ -d node_modules ]; then \
		echo "node_modules missing — installing..."; \
		$(PM) install; \
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
	@echo "gui: no unit tests configured yet"

.PHONY: test.integration
test.integration: ## Run integration tests (none yet)
	@echo "gui: no integration tests yet"

.PHONY: lint
lint: preflight ## Run next lint
	$(NPX) next lint

.PHONY: lint-fix
lint-fix: preflight ## Run next lint --fix
	$(NPX) next lint --fix

.PHONY: dev.start
dev.start: preflight ## Run Next.js dev server in foreground
	$(NPX) next dev

.PHONY: dev.stop
dev.stop: ## No-op (gui dev.start runs in foreground)
	@echo "gui: dev.start runs in foreground; nothing to stop"

.PHONY: clean
clean: ## Remove build artifacts (preserves source/generated code)
	rm -rf .next

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_.\-]+:.*?## / { printf "  %-20s %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST) | sort
