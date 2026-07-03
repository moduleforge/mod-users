# Gap analysis and scope decisions

## Purpose and scope

Verified state of `mod-users/gui` and related infra against the sibling pattern, plus resolutions for the three scope questions the dispatch flagged (docker-compose disposition, yalc/core-gui dependency, `make preview`). Companion to [sibling-build-mechanism.md](./sibling-build-mechanism.md).

## Verified state of gui/ (git-tracked vs on-disk)

Confirmed via `git ls-files gui/` and `ls`:

**Git-tracked cruft to remove** (none exist in any sibling gui/):
- `gui/Dockerfile.dev` — broken (see docker section).
- `gui/README.md` — unmodified create-next-app boilerplate (`pnpm dev`, `localhost:3000`).
- `gui/components.json` — shadcn config.
- `gui/public/{file,globe,next,vercel,window}.svg` — default Next.js SVGs.
- `gui/.gitignore` — full create-next-app boilerplate (siblings use a 5-line minimal ignore: `node_modules/ dist/ build/ .yalc/ yalc.lock`).

**Untracked empty directories on disk** (NOT git-tracked — confirmed absent from `git ls-files`; they are stray empty dirs, remove with `rmdir`/`rm -rf`, no git rm needed):
- `gui/next.config.ts/`, `gui/eslint.config.mjs/`, `gui/postcss.config.mjs/` (all empty dirs, not files), `gui/worktree/` (empty), `gui/.next/` (gitignored Next build cache).

**Library-shaped code (keep, do not touch behavior):** `src/lib/*`, `src/components/*`, `src/components/ui/*`, `src/stories/*.stories.tsx`, `src/index.ts`, `src/styles.css`, `.ladle/*`, `tsconfig.json`, `Makefile`, `package.json`, `tsup.config.ts`.

## Scope decision 1 — docker-compose gui service + Dockerfile.dev  → USER DECISION REQUIRED

`deploy/local/docker-compose.yml` lines 183-204 define a `gui` service that builds `mod-users/gui/Dockerfile.dev`, publishes `3000:3000`, sets `NEXT_PUBLIC_API_BASE_URL`, and volume-mounts `gui/{src,public,next.config.ts,tsconfig.json,postcss.config.mjs,components.json,eslint.config.mjs}` (several of which no longer exist as files).

`Dockerfile.dev` is broken beyond use: `context: ../../..`, references `core-module/gui` and `users-module/` (pre-rename paths), uses `npm ci` + `corepack pnpm` + `pnpm-lock.yaml`/`pnpm-workspace.yaml`, and runs `next dev` — but **there are no Next.js pages in `gui/` anymore**, so there is nothing for `next dev` to serve.

Evidence pointing to **removal**:
- No sibling has a `Dockerfile.dev` or a docker-compose `gui` service; the canonical preview is native Ladle (`make preview`).
- `docs/architecture.md` line 103 already attributes the port-3000 "GUI dev server" to **app-mfdemo's** Next.js dev server, not a mod-users compose service.
- The service cannot function in its current form.

Legitimate alternative: **repurpose** the service to run `ladle serve` in a container for a containerized live-reload preview inside the dev stack (AGENTS.md currently claims `make dev.start` starts "the GUI dev server (3000)").

This is a genuine workflow preference the user must decide; the operating contract mandates halting on it. Raised as a **user_question**. Downstream doc ripples (AGENTS.md dev-stack section + line 58, root `Makefile` line 74/comment, `docker-compose.yml` line 13 comment, `docs/architecture.md` line 103 local-dev table, `docs/project-structure.md` line 88) depend on the answer.

## Scope decision 2 — yalc / core-gui dependency (next-steps id 3RgF)  → BRING INTO SCOPE

The dispatch said to include this only if mod-users' wiring is *inconsistent* with siblings, not merely "also fragile." It **is inconsistent**:

- `mod-contacts/gui` imports `@moduleforge/core-gui` in `src` (5 files) yet declares it **only** as an optional `peerDependency` — it is **absent from `dependencies`**, and `.yalc/`/`yalc.lock` are gitignored (never committed).
- `mod-users/gui/package.json` declares `@moduleforge/core-gui` in **both** `dependencies` (`"file:.yalc/@moduleforge/core-gui"`, committed) **and** `peerDependencies` (optional).

The committed `file:.yalc/...` entry in `dependencies` is exactly what breaks fresh-checkout / CI `bun install` (the path doesn't exist without `.yalc/` populated). Removing it from `dependencies` (keeping the optional peer) makes mod-users identical to mod-contacts and resolves 3RgF's fresh-install fragility as a side effect. The dev-time `yalc add` step (documented in AGENTS.md) remains the local linking mechanism, matching the sibling flow. **In scope**, folded into the build-alignment phase.

(The *broader* "yalc is a fragile repo-wide convention" framing remains out of scope — architecture.md line 126 documents it as intentional and common across modules; fixing it repo-wide is a separate cross-module initiative.)

## Scope decision 3 — `make preview` convention  → BRING INTO SCOPE (assumption)

Siblings expose `make preview` at the **module-level** Makefile (e.g. `mod-core/Makefile` runs `bun run dev` = Ladle on 61000). mod-users' top-level `Makefile` has no `preview` target. The root aggregate `README.md` "GUI workbench" row lists `make preview` for `mod-contacts/`, `mod-core/`, `mod-tags/` and omits mod-users. Adding a `preview` target to `mod-users/Makefile` (mirroring mod-core, wrapping `make -C gui dev` / Ladle on 61002) is low-cost convention alignment. Treated as in scope; not a user decision. Flagged as an applied assumption so the user can veto.

## Cross-repo concern — root aggregate README  → OUTSIDE mod-users repo

The root module table (`/Users/zane/playground/moduleforge/README.md` line 71: `mod-users … yes (Next.js)` → should be `yes (tsup lib)`) and the workbench row (line 114, add mod-users) live in the **aggregate repo**, which is a *different git repository* outside `project_root`. A mod-users task branch cannot contain that edit. This correction must be handled by the aggregate-level process / manager, not a mod-users implementation task. **Flagged for manager**; kept out of the mod-users task set (or recorded as a cross-repo followup).

## CI blocker (next-steps id r86L) — in scope

`.github/workflows/ci.yml` installs pnpm via corepack and runs `pnpm install --frozen-lockfile` in the `lint` (40-45), `test` (64-69), and `build` (104-118) jobs. Replace with bun (`oven-sh/setup-bun` or `bun install --frozen-lockfile`, dropping the corepack/pnpm steps). Then remove the stale "CI still uses pnpm" notes from `AGENTS.md` (Known issues), `.claude/CLAUDE.md`, and clear/annotate id `r86L` in `plan/next-steps.yaml`.

## Explicitly NOT touched

`model/`, `api/` (Go), auth/authz behavior, the React component implementations/behavior, and sibling gui/ directories (read-only reference). `docs/mod-users-spec.md` references to "app-mfdemo Next.js project" are correct (app-mfdemo *is* Next.js) and stay.

## Answer

Remove entirely (option A). Delete the docker-compose `gui` service and `gui/Dockerfile.dev`; update `AGENTS.md`/root `Makefile`/`docs/architecture.md`/`docs/project-structure.md` to point at `make preview` (Ladle) for local component preview, matching the sibling convention exactly. `app-mfdemo` remains the true integration/e2e testbed. (User did not respond within the prompt window; proceeding with the manager's recommended, lower-risk, convention-matching option. Revisit if the user later prefers the repurpose-to-ladle alternative — this is easily amended since it only touches docker-compose.yml, Dockerfile.dev, and doc references.)
