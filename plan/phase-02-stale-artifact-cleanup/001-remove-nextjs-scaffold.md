# Remove Nextjs Scaffold

## Purpose and scope

Remove the create-next-app / Next.js scaffold residue from `mod-users/gui/` so the directory contains only the library-shaped files the siblings have. Mechanical deletion + `.gitignore` replacement; touches no source or build config. Independent of Phase 01 (disjoint file set) — safe to run in parallel.

No standard skill; follow the `## Requirements`. Inventory verified in [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md).

## Requirements

1. **Remove git-tracked scaffold cruft** (`git rm`):
   - `gui/README.md` — unmodified create-next-app boilerplate (`pnpm dev`, `localhost:3000`).
   - `gui/components.json` — shadcn config.
   - `gui/public/file.svg`, `gui/public/globe.svg`, `gui/public/next.svg`, `gui/public/vercel.svg`, `gui/public/window.svg` — default Next.js SVGs. Remove the now-empty `gui/public/` directory as well.
   - **Do not** remove `gui/Dockerfile.dev` here — its deletion is owned by Phase 03 (dev-stack-disposition), tied to the docker-compose `gui` service removal, to avoid double-ownership.

2. **Replace `gui/.gitignore`** (currently full create-next-app boilerplate) with the minimal sibling form. Use the actual `mod-core/gui/.gitignore` content as the target (six lines — note it includes `.ladle/.cache/`, which the relocated Ladle stylesheet in Phase 01 will produce on `ladle serve`):
   ```gitignore
   node_modules/
   dist/
   build/
   .ladle/.cache/
   .yalc/
   yalc.lock
   ```

3. **Remove stray untracked directories on disk** (confirmed absent from `git ls-files` — they are empty dirs / build caches, not tracked files; use `rm -rf`, no `git rm`):
   - `gui/next.config.ts/` (empty dir), `gui/eslint.config.mjs/` (empty dir), `gui/postcss.config.mjs/` (empty dir), `gui/worktree/` (empty), `gui/.next/` (Next build cache).

## Validation

- `git ls-files gui/` no longer lists `gui/README.md`, `gui/components.json`, or any `gui/public/*.svg`; `gui/public/` no longer exists.
- `gui/Dockerfile.dev` is **still present** (removed by Phase 03, not here).
- `gui/.gitignore` matches the six-line sibling form above; `grep -n 'next\|pnpm\|vercel\|yarn' gui/.gitignore` returns nothing.
- `ls -la gui/` shows none of `next.config.ts/`, `eslint.config.mjs/`, `postcss.config.mjs/`, `worktree/`, `.next/`.
- `find gui -maxdepth 1 -type d -empty` returns nothing (no stray empty dirs left).
- The remaining `gui/` file inventory matches the sibling shape: config files, `Makefile`, `package.json`, `tsconfig.json`, `tsup.config.ts`, `.ladle/`, and `src/`.

## Status

- **Outcome:** succeeded (implementation task agent, work verified directly by the manager after the agent's dispatch was interrupted twice by infrastructure errors — an API connection drop mid-response, then a stall on resume — after the substantive work was already committed; 2026-07-03).
- **Commit:** task worktree `phase-02-task-01-remove-nextjs-scaffold`, commits `7fab552` (`git rm` scaffold cruft: README, components.json, public SVGs) and `14f215d` (`.gitignore` replacement; confirmed no stray empty dirs present).
- **Validation summary (re-verified directly against every `## Validation` item):**
  - `git ls-files gui/` no longer lists `gui/README.md`, `gui/components.json`, or any `gui/public/*.svg`; `gui/public/` no longer exists — confirmed.
  - `gui/Dockerfile.dev` still present — confirmed (correctly left for Phase 03).
  - `gui/.gitignore` matches the six-line sibling form exactly; `grep -n 'next\|pnpm\|vercel\|yarn' gui/.gitignore` returns nothing — confirmed.
  - `ls -la gui/` shows none of `next.config.ts/`, `eslint.config.mjs/`, `postcss.config.mjs/`, `worktree/`, `.next/` — confirmed **in the task worktree**, but see correction below.
  - `find gui -maxdepth 1 -type d -empty` returns nothing — confirmed **in the task worktree**, but see correction below.
  - Remaining `gui/` inventory matches sibling shape: `.gitignore`, `.ladle/`, `Dockerfile.dev`, `Makefile`, `node_modules/`, `package.json`, `src/`, `tsconfig.json`, `tsup.config.ts`, `vite.config.ts` — confirmed.
  - Task worktree working tree is clean (`git status --porcelain` empty).
- **Correction (phase-02 gate review, 2026-07-03):** the review-changes-style lens caught that Requirement 3 (removing the stray untracked empty dirs and `gui/.next/`) was never actually effective against the project root. Those directories are untracked filesystem artifacts local to the checkout at `/Users/zane/playground/moduleforge/mod-users` — `git worktree add` does not copy untracked files into a new worktree, so the task agent's isolated task worktree never contained them in the first place, and its `rm -rf`/verification there was checking a location that was already clean by construction, not performing or confirming the removal the task doc intended. The manager verified this directly against the actual project root, found all four stray dirs and `gui/.next/` (294MB build cache) still present, and removed them there directly (`rm -rf gui/next.config.ts gui/eslint.config.mjs gui/postcss.config.mjs gui/worktree gui/.next`), then re-verified `find gui -maxdepth 1 -type d -empty` and `ls -la gui/` clean. Requirement 3 is now genuinely satisfied. This is a latent gap in how per-worktree task isolation interacts with untracked-file cleanup tasks generally, worth noting for future phases/plans that include stray-file removal.
- **Assumptions applied:** both `## Assumptions` entries held as written — the stray directories were genuinely empty (none contained files) once actually located in the project root, and Phase 01 had already landed by dispatch time, though this task's disjoint file set made that irrelevant.

## Assumptions

- The stray empty directories (`next.config.ts/`, etc.) exist on disk as artifacts of a prior rename/checkout and are genuinely empty — verified in the gap-analysis note. If any unexpectedly contains files, stop and report rather than force-removing.
- Phase 01 may or may not have landed first; this task does not depend on it and edits a disjoint file set.

## References

- [../notes/gap-analysis-and-scope.md](../notes/gap-analysis-and-scope.md) — the verified git-tracked-cruft vs. stray-empty-dir inventory.
- Sibling reference (read-only): `mod-core/gui/.gitignore` (target `.gitignore` content).

## Checkpoint hints

- After the `git rm` of tracked scaffold files.
- After replacing `.gitignore` and removing the stray dirs.
