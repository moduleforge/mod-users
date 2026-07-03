@AGENTS.md

# Claude Code — mod-users

This file covers Claude Code-specific configuration and guidance. For build commands, environment setup, project conventions, and general agent guidance, see [AGENTS.md](../AGENTS.md) (referenced above).

## Toolchain notes for Claude Code

- **Make target for most work**: prefer `make build.api`, `make test.unit`, `make lint` over invoking Go or Bun directly. The Makefiles handle tool detection and workspace-root resolution.
- **Generated files**: `model/db/*.go` is sqlc output — do not edit. If a query needs to change, edit `model/queries/*.sql` and run `cd model && sqlc generate`.
- **Environment**: `.env` is gitignored. Never commit secrets. `.env.example` shows all required vars.

## Known gotchas

- **`make clean.build` removes `model/db/`** — restore with `git checkout HEAD -- model/db/` before running Go builds.
- **yalc link required for gui/ builds/typecheck** — `gui/package.json` declares `@moduleforge/core-gui` only as an optional peer dependency, so a fresh `bun install` succeeds without it. But `src` still imports `@moduleforge/core-gui` (sidebar-nav, ui/dialog, error-message), so `make build.gui` and `make lint.gui` still need it resolved via the `yalc add` step from AGENTS.md First-time setup.
- **CI still uses pnpm** — `.github/workflows/ci.yml` has not been updated for bun. CI checks will fail until that's addressed (tracked in `plan/next-steps.yaml`, id `r86L`).

## File-editing scope

When making changes, confine edits to the relevant sub-project. Cross-sub-project changes (e.g. adding a column to a table and updating the API handler that reads it) require touching `model/queries/`, regenerating `model/db/`, and updating `api/internal/`. Confirm the intended scope before starting multi-file changes.
