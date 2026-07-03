# Phase — Stale Artifact Cleanup

## Goals

Remove the create-next-app / Next.js scaffold residue from `gui/` so the directory contains only the library-shaped files the siblings have. Independent of the build-alignment phase; can run in parallel. Leaves `gui/` matching the sibling file inventory.

## Inputs

- Verified inventory in [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md) (git-tracked cruft vs. stray empty dirs).
- Sibling `gui/.gitignore` (5-line minimal form) as the target for mod-users' `.gitignore`.

## Outputs

- Removed git-tracked files: `gui/README.md` (boilerplate), `gui/components.json`, `gui/public/*.svg` (and the now-empty `gui/public/`). `gui/Dockerfile.dev` removal is owned by the dev-stack phase (tied to the user decision) to avoid double-ownership.
- `gui/.gitignore` replaced with the minimal sibling ignore (`node_modules/`, `dist/`, `build/`, `.yalc/`, `yalc.lock`).
- Removed stray untracked empty dirs: `gui/next.config.ts/`, `gui/eslint.config.mjs/`, `gui/postcss.config.mjs/`, `gui/worktree/`, `gui/.next/`.
- `gui/` file inventory matching the sibling shape (no Next.js/scaffold artifacts remain), verified by `git ls-files gui/` and a find sweep for stray dirs.
