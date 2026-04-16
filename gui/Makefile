# gui/ sub-project Makefile (Next.js).
# Self-contained: implements the canonical target contract used by the root
# Makefile so it can be invoked standalone or via `make -C gui <target>`.

.DEFAULT_GOAL := build

# Auto-detect package manager: pnpm > npm.
PM := $(shell if command -v pnpm >/dev/null 2>&1; then echo pnpm; else echo npm; fi)
# npx works with both pnpm and npm for running local binaries.
NPX := npx

.PHONY: build
build: node_modules ## Build the Next.js production bundle
	$(NPX) next build

.PHONY: test
test: test.unit ## Alias for test.unit

.PHONY: test.unit
test.unit: node_modules ## Run unit tests (none yet — placeholder passes)
	@echo "gui: no unit tests configured yet"

.PHONY: test.integration
test.integration: ## Run integration tests (none yet)
	@echo "gui: no integration tests yet"

.PHONY: lint
lint: node_modules ## Run next lint
	$(NPX) next lint

.PHONY: lint-fix
lint-fix: node_modules ## Run next lint --fix
	$(NPX) next lint --fix

.PHONY: dev.start
dev.start: node_modules ## Run Next.js dev server in foreground
	$(NPX) next dev

.PHONY: dev.stop
dev.stop: ## No-op (gui dev.start runs in foreground)
	@echo "gui: dev.start runs in foreground; nothing to stop"

.PHONY: clean
clean: ## Remove build artifacts (preserves source/generated code)
	rm -rf .next

# Re-install when package.json changes, or when node_modules was created by
# a different platform (e.g. Linux container vs macOS host).
node_modules: package.json
	@if [ -d node_modules ] && ! [ -x node_modules/.bin/next ]; then \
		echo "gui: node_modules stale (wrong platform?) — removing..."; \
		rm -rf node_modules; \
	fi
	$(PM) install
	@touch node_modules/.install-stamp

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*?## "} \
		/^[a-zA-Z0-9_.\-]+:.*?## / { printf "  %-20s %s\n", $$1, $$2 }' \
		$(MAKEFILE_LIST) | sort
