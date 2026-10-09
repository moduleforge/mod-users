# 003 Commit gui bun.lock And Document core-gui Link

## Purpose and scope
Flow-accounts lJE0: `gui/` commits only `yalc.lock`; consumers cannot `bun install --frozen-lockfile`. Type build expects `node_modules/@moduleforge/core-gui` to link to sibling mod-core/gui.

## Requirements
1. Decide and implement: commit `gui/bun.lock` (preferred) or a workspace layout; verify `bun install --frozen-lockfile` succeeds on a clean clone.
2. Make the core-gui link resolvable (script in `scripts/link-siblings.sh` or documented step) or document it in `gui/README.md`/`gui/CLAUDE.md`.
3. Ensure `.gitignore` does not exclude the lockfile; do not commit `node_modules`.

## Validation
- Clean checkout in a temp dir: `bun install --frozen-lockfile && bun run typecheck && bun run build`.
