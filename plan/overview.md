# GUI Library Conversion — mod-users

## Purpose and scope

Align `mod-users/gui` with the canonical ModuleForge reusable-component-library pattern already established by the sibling GUI modules (`mod-core`, `mod-contacts`, `mod-tags`, `mod-tasks`), so applications consume `@moduleforge/users-gui` as a simple tsup/React/Ladle library — the same way they already consume the siblings.

This is **not** a from-scratch Next.js→library migration. The `gui-library-split` (May 8) already extracted the Next.js app router into a sibling `example/`, which was subsequently deleted in favor of the aggregate-level `app-mfdemo`. `gui/src` today contains only library-shaped code, and `gui/package.json` already has a tsup build, an `exports` map, and a Ladle `dev` script. The remaining work is **build-tooling alignment, stale-artifact cleanup, a CI fix, and dev-stack cleanup** — not re-planning the conversion.

### What must change
- Replace mod-users' bespoke two-entry `tsup` config + separate `build:css` script with the plain-`tsup` sibling convention; add the `vite.config.ts` siblings use for Ladle. (See [sibling build mechanism](./notes/sibling-build-mechanism.md).)
- Remove the committed `file:.yalc/@moduleforge/core-gui` entry from `gui/package.json` `dependencies` (keep the optional peer) so fresh `bun install`/CI works — matching mod-contacts. (See [gap analysis](./notes/gap-analysis-and-scope.md).)
- Remove stale Next.js/create-next-app scaffold artifacts from `gui/`.
- Remove the broken `deploy/local/docker-compose.yml` `gui` service + `gui/Dockerfile.dev` entirely, and repoint the docs at `make preview` (Ladle) — **decision resolved: remove**. (See [gap analysis, `## Answer`](./notes/gap-analysis-and-scope.md).)
- Migrate `.github/workflows/ci.yml` from pnpm to bun (clears blocker `r86L`).
- Add a module-level `make preview` target and align docs.

### What must NOT change
- `model/`, `api/` (Go), auth/authz product behavior.
- The React component implementations/behavior (packaging/tooling only).
- Sibling gui/ directories (read-only reference).
- The `.yalc` dev-linking convention itself (accepted repo-wide; only mod-users' inconsistent *committed dependency* is corrected).

### Success criteria
- `gui/` builds and packages identically in shape to the sibling tsup libraries: `vite.config.ts` present, plain-`tsup` build (JS + DTS; **no** bundled CSS — consumers generate CSS via Tailwind v4 `@source` scanning, as app-mfdemo already does), no Next.js/pnpm/scaffold artifacts remaining.
- Fresh `bun install` in `gui/` succeeds without a pre-populated `.yalc/`.
- CI passes using bun; no pnpm remains in `ci.yml`.
- The docker-compose `gui`/`Dockerfile.dev` entanglement is resolved per the user's decision, with AGENTS.md/architecture.md/Makefile updated to match.
- `app-mfdemo`'s existing consumption of `@moduleforge/users-gui` via yalc still works (verified).
- Ladle preview works via `make preview`.

### Key correction discovered during investigation
The dispatch assumed siblings emit `dist/index.css` from plain `tsup`. **They do not** — no sibling produces `dist/index.css`; the `"./styles.css": "./dist/index.css"` export is vestigial across all of them, and consumers generate CSS themselves via `@source`. The alignment therefore *drops* mod-users' CSS build rather than reproducing a (non-existent) sibling CSS mechanism. Full detail in [sibling-build-mechanism.md](./notes/sibling-build-mechanism.md).

## Current status

**Status: finalized — ready to execute.** Investigation is complete (captured in `plan/notes/`), the one open decision is resolved, and all six phases are registered in `plan/TODO.yaml` with authored task documents.

Resolved decisions and scope confirmations:
- **Docker-compose `gui` service + `Dockerfile.dev` disposition → remove entirely** (option A, matching the sibling convention; `make preview`/app-mfdemo become the documented preview/integration paths). See [gap analysis, `## Answer`](./notes/gap-analysis-and-scope.md).
- yalc/core-gui dependency (`3RgF`): mod-users' committed `file:.yalc` dep is genuinely inconsistent with the siblings → **in scope** (folded into Phase 01).
- `make preview`: low-cost convention alignment → **in scope** (Phase 05).

One item is **out of the mod-users repo boundary and flagged for the manager, not a mod-users task**: the root aggregate `README.md` module-table + workbench-row correction (`mod-users … Next.js` → `tsup lib`; add mod-users to the workbench row) lives in a different git repo and must be handled at the aggregate level after this plan lands.

**Starting state / sequencing.** Phase 01 (build alignment) begins first and is a prerequisite for validating that `app-mfdemo` still consumes the library; it also unblocks CI's fresh `bun install` (soft dependency for Phase 04). Phases 02 (stale cleanup) and 05 (preview target) touch disjoint files and are fully parallel-eligible with everything. Phases 01, 03, and 04 each edit different regions of `AGENTS.md` (and 01/04 both touch its "Known issues" list and `.claude/CLAUDE.md`), so they should be sequenced rather than run concurrently. Phase 06 (`doc-updates`) runs last, after the implementation phases land.

## Overview

### Phase 01 — GUI build alignment (`gui-build-alignment`)
Bring `gui/` build tooling to the sibling pattern: add `vite.config.ts` (for Ladle); rewrite `tsup.config.ts` to a single plain config with `external` including `@moduleforge/core-gui` (matching mod-contacts); drop the `build:css` script and second CSS entry so `build` is plain `tsup`; remove `@moduleforge/core-gui` from `dependencies` (keep optional peer); relocate the Ladle Tailwind entry to `.ladle/styles.css` per convention; align `package.json` devDeps and `gui/Makefile` build/clean comments. Validate `make build.gui` produces JS+DTS, `bun install` works without `.yalc/`, and `app-mfdemo` still styles correctly via `@source`. See [notes/sibling-build-mechanism.md](./notes/sibling-build-mechanism.md).

### Phase 02 — Stale artifact cleanup (`stale-artifact-cleanup`)
Remove Next.js/create-next-app scaffold from `gui/`: git-tracked `README.md`, `components.json`, `public/*.svg`, and (unless retained by the docker decision) `Dockerfile.dev`; replace `gui/.gitignore` with the minimal sibling ignore; remove stray empty dirs (`next.config.ts/`, `eslint.config.mjs/`, `postcss.config.mjs/`, `worktree/`, `.next/`). Independent of Phase 01. See [notes/gap-analysis-and-scope.md](./notes/gap-analysis-and-scope.md).

### Phase 03 — Dev-stack disposition (`dev-stack-disposition`)
**Decision: remove.** Delete the `docker-compose.yml` `gui` service block (+ its line 13 comment) and `gui/Dockerfile.dev`, and update the directly-coupled comments/steps: `AGENTS.md` dev-stack step (line 58 no longer claims a port-3000 GUI dev server; point to `make preview`/app-mfdemo) and root `Makefile` dev-orchestration comment (lines 73-74). The `docs/architecture.md` local-dev-stack table row and `docs/project-structure.md` compose line are reconciled in Phase 06 (`doc-updates`) to keep single ownership of `docs/`.

### Phase 04 — CI bun migration (`ci-bun-migration`)
Replace pnpm with bun in `.github/workflows/ci.yml` (`lint`, `test`, `build` jobs); drop corepack/pnpm steps; use bun install + `make` targets. Clear the stale "CI uses pnpm" notes in `AGENTS.md`, `.claude/CLAUDE.md`, and annotate/close `r86L` in `plan/next-steps.yaml`. Independent. See [notes/gap-analysis-and-scope.md](./notes/gap-analysis-and-scope.md).

### Phase 05 — Preview target & README alignment (`preview-and-readme`)
Add a module-level `make preview` target to `mod-users/Makefile` (mirroring `mod-core`, wrapping Ladle on 61002). The root aggregate `README.md` module-table + workbench-row corrections are **flagged for the manager** as a cross-repo item (outside `project_root`), not authored as a mod-users task.

### Phase 06 — Documentation updates (`doc-updates`)
Registered by the Phase 4 architectural-implications check (this plan reshapes the GUI component-library build boundary and removes a documented docker-compose topology entry). Update `docs/architecture.md` (GUI library build description; remove/repoint the local-dev-stack port-3000 "GUI dev server" row per the remove decision), `docs/project-structure.md` (gui/ layout: drop `Dockerfile.dev`, add `vite.config.ts`, refresh the `styles.css`/`tsup` notes; compose-line description on line 88), and `docs/mod-users-spec.md` GUI-packaging paragraph if it needs it. Runs after the implementation phases land.
