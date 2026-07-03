# Phase — CI Bun Migration

## Goals

Migrate `.github/workflows/ci.yml` from pnpm to bun so CI passes after the repo-wide bun migration. Clears the recorded blocker `r86L` (next-steps.yaml) and the matching "Known issues" notes. Independent of the other phases; can run in parallel.

## Inputs

- Current `.github/workflows/ci.yml` — pnpm appears in the `lint` (lines 40-45), `test` (64-69), and `build` (104-118) jobs (corepack + `pnpm install --frozen-lockfile`, `working-directory: gui`).
- `plan/summary-bun-migration.md` (bun migration that explicitly scoped CI out) and `plan/next-steps.yaml` id `r86L`.
- The gui/ Makefile/preflight already assume bun; the workspace lockfile is `bun.lock`.

## Outputs

- `ci.yml` `lint`/`test`/`build` jobs install deps with bun (e.g. `oven-sh/setup-bun` + `bun install --frozen-lockfile`), with the corepack/pnpm steps removed. Consider whether the separate `working-directory: gui` install is still needed given the bun workspace root, or whether a root `bun install` suffices.
- Fresh-checkout `bun install` must succeed in CI — this depends on the Phase 01 removal of the committed `file:.yalc/@moduleforge/core-gui` dependency (soft dependency: CI green requires Phase 01's package.json change).
- Stale "CI still uses pnpm" notes removed from `AGENTS.md` (Known issues) and `.claude/CLAUDE.md`; `r86L` annotated/closed in `plan/next-steps.yaml`.
