# Migrate Ci To Bun

## Purpose and scope

Migrate `.github/workflows/ci.yml` from pnpm to bun so CI installs GUI/workspace dependencies the same way the repo now does everywhere else (the workspace lockfile is `bun.lock`; the Makefiles/preflight already assume bun). Clears blocker `r86L`. Soft-depends on Phase 01 — a fresh `bun install --frozen-lockfile` only succeeds once the committed `file:.yalc/@moduleforge/core-gui` dependency has been removed from `gui/package.json` (Phase 01). Run Phase 04 after Phase 01 has landed.

No standard skill; follow the `## Requirements`. Context in [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md) (CI blocker section) and `plan/next-steps.yaml` id `r86L`.

## Requirements

1. **Rewrite the pnpm steps in `ci.yml`** for the three affected jobs — `lint` (lines 40-45), `test` (lines 64-69), and `build` (lines 104-118). In each:
   - Remove the `- name: Install pnpm / run: corepack enable && corepack prepare pnpm@latest --activate` step.
   - Replace the `pnpm install --frozen-lockfile` step (`working-directory: gui`) with a bun install. Prefer adding `- uses: oven-sh/setup-bun@v2` (pin a `bun-version` if the repo pins tool versions elsewhere) and a root-level `- name: Install dependencies / run: bun install --frozen-lockfile` (no `working-directory: gui` — bun hoists to the workspace root, so a single root install covers `gui/`).
   - **Keep** the existing `actions/setup-node@v4` step: the `gui/` Makefile `preflight` target checks for `node` on PATH and will fail without it. (`NODE_VERSION` env stays.)
   - In the `build` job, the `- name: Install GUI dependencies` step (`working-directory: gui`, pnpm) collapses into the single root `bun install --frozen-lockfile` — remove the duplicate gui-scoped install; keep `make build.model` / `make build.api` / `make build.gui` ordering.

2. **Clear the stale "CI uses pnpm" notes:**
   - `AGENTS.md` "Known issues and follow-up items": remove the **CI workflow still uses pnpm** bullet (`next-steps id: r86L`).
   - `.claude/CLAUDE.md` "Known gotchas": remove the **CI still uses pnpm** bullet.

3. **Annotate/close the blocker in `plan/next-steps.yaml`** (the project-root historical tracker, `plan/next-steps.yaml`): mark id `r86L` as resolved by this task (either remove the item or add a resolution note referencing this plan). While there, note that id `3RgF` (yalc dep) is resolved by Phase 01 of this plan.

## Validation

- `grep -rn 'pnpm\|corepack' .github/workflows/ci.yml` returns nothing.
- `ci.yml` `lint`, `test`, and `build` jobs each install deps via `bun install --frozen-lockfile` (root-level, no `working-directory: gui`); `oven-sh/setup-bun` present; `actions/setup-node` retained.
- `ci.yml` still parses as valid YAML (`python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))"` or equivalent).
- `grep -rn 'pnpm' AGENTS.md .claude/CLAUDE.md` returns nothing (the pnpm known-issue bullets are gone).
- `plan/next-steps.yaml`: `r86L` is removed or annotated resolved.
- If CI can be triggered (or dry-run locally): a fresh `bun install --frozen-lockfile` at repo root succeeds without a populated `.yalc/` (confirming the Phase 01 dependency fix).

## Assumptions

- Phase 01 has landed (the `file:.yalc/@moduleforge/core-gui` dependency is removed), so `bun install --frozen-lockfile` resolves cleanly in CI.
- **Known limitation to verify (flag, do not silently ignore):** the `build` job's `make build.gui` step runs `tsup` with `dts: true`, which needs `@moduleforge/core-gui` *types* resolvable (it is imported in `gui/src`). CI does not populate `.yalc/`, so `make build.gui` may still fail at DTS generation even after the pnpm→bun swap — this is a **pre-existing** issue independent of the package manager (CI would have hit it under pnpm too). If it surfaces, do not block this task on it: complete the pnpm→bun migration and record the core-gui-in-CI resolution (link/publish core-gui, or scope `build.gui` out of CI) as a follow-up for the manager. The narrow blocker being cleared here is the pnpm/bun mismatch.

## References

- [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md) — CI blocker section (exact pnpm line locations per job).
- `plan/next-steps.yaml` id `r86L` (blocker) and `3RgF` (yalc dep, resolved by Phase 01).
- `.github/workflows/ci.yml` — the three jobs to edit.

## Checkpoint hints

- After rewriting the three CI jobs.
- After clearing the AGENTS.md / .claude/CLAUDE.md notes and annotating next-steps.yaml.
