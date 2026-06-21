---
task: example/install-example-deps
phase: 3
number: "002"
title: Verify example/ installs cleanly with bun
status: done
tier: sonnet-low
depends_on: [example/update-example-makefile]
---

# Verify example/ installs cleanly with bun

## Purpose and scope

Install `example/` dependencies with bun, delete the stale `package-lock.json`, and confirm the example Next.js app builds.

## Requirements

1. **Delete `example/package-lock.json`**:
   ```bash
   rm example/package-lock.json
   ```

2. **Run `bun install` from within `example/`** (standalone, not workspace-managed):
   ```bash
   cd /Users/zane/playground/moduleforge/users-module/example && bun install
   ```
   This generates `example/bun.lock`.

3. **Add `example/bun.lock` to git** and commit with message: `chore(bun-migration): add example/bun.lock, remove package-lock.json`

4. **Run `make build.example`** from the repo root to confirm the Next.js build works:
   ```bash
   make -C /Users/zane/playground/moduleforge/users-module build.example
   ```

## Validation

- `example/package-lock.json` does not exist
- `example/bun.lock` exists and is tracked in git
- `bun install` exits with code 0 in `example/`
- `make build.example` exits with code 0
- `example/.next/` is populated with Next.js build output
- No npm/pnpm invocations appear in build output

## Status

outcome: succeeded
date: 2026-06-21
validation_summary: All 6 checks passed. `bun install` resolved 641 packages in example/ (including .yalc local packages). `make build.example` completed with exit 0, producing 16 static pages. No npm/pnpm invocations in output.
files_affected:
  - example/bun.lock (created, tracked)
  - example/package-lock.json (deleted)
decisions_made:
  - Copied .yalc/ from main checkout into worktree example/ (gitignored, developer-local) so bun could resolve file: dependencies for @moduleforge/core-gui and @moduleforge/users-gui.
notes:
  - Next.js emitted a warning about multiple lockfiles (bun.lock at repo root and in example/); build succeeded despite warning. Consider setting `outputFileTracingRoot` in next.config.ts to silence it.
commit: 090c404
