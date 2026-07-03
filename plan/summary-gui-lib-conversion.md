# Session Summary — GUI Library Conversion

## What was planned and why

`mod-users/gui` had drifted from the canonical ModuleForge component-library pattern already established by its siblings (`mod-core`, `mod-contacts`, `mod-tags`, `mod-tasks`), each of which consumers pull in as a plain tsup/React/Ladle library (`@moduleforge/{module}-gui`). The drift was **not** a from-scratch Next.js migration need — the earlier `gui-library-split` (May 8) had already extracted the Next.js app router into a sibling `example/`, subsequently deleted in favor of the aggregate-level `app-mfdemo`. By the time this plan opened, `gui/src` already held only library-shaped code, and `gui/package.json` already had a tsup build, an `exports` map, and a Ladle `dev` script.

What remained was narrower and mechanical:

- **Build-tooling alignment** — mod-users' bespoke two-entry tsup config plus a separate `build:css` script needed to collapse to the plain single-entry tsup convention the siblings use, and a `vite.config.ts` needed to be added for Ladle. Investigation (`plan/notes/sibling-build-mechanism.md`) surfaced a key correction to the original dispatch assumption: **no sibling actually emits `dist/index.css`** from its tsup build — the `"./styles.css"` export map entry is vestigial across all of them, and consumers generate their own CSS via Tailwind v4 `@source` scanning of the built JS. So the alignment work *drops* mod-users' CSS build entirely rather than reproducing a sibling mechanism that doesn't exist.
- **Stale-artifact cleanup** — removing leftover create-next-app/Next.js scaffold (`README.md`, `components.json`, default SVGs, a bloated `.gitignore`, stray empty config dirs, a 294MB `.next/` build cache).
- **Dev-stack cleanup** — the `deploy/local/docker-compose.yml` `gui` service and `gui/Dockerfile.dev` were broken beyond repair (pre-rename paths, `next dev` against a directory with no Next.js pages left) and needed to be removed outright, with docs repointed at `make preview` (Ladle) and `app-mfdemo` as the supported local-preview/integration paths.
- **CI fix** — `.github/workflows/ci.yml` still used pnpm against a workspace that had already moved to bun/`bun.lock` everywhere else, tracked as blocker `r86L`.
- **Documentation reconciliation** — `docs/architecture.md` and `docs/project-structure.md` needed to stop describing the removed CSS build and the removed docker-compose GUI service.

One item was explicitly identified as **out of repo boundary**: correcting the aggregate `README.md` module table (`mod-users … Next.js` → `tsup lib`) and adding mod-users to the workbench row lives in a separate git repo above `project_root` and was flagged for the manager rather than executed as a mod-users task.

## What shipped

All six phases landed on `main`, each as a single-task phase, plus a handful of small interstitial fixes the manager dispatched directly during phase-boundary gate reviews (visible as additional merge commits in `git log` between the phase merges, not tracked as formal plan tasks).

### Phase 01 — GUI build alignment (`gui-build-alignment`)
Merge: `0943165574948346630bc15ce33d750d2ca13090` (branch `phase-01-task-01-align-gui-build-tooling`, commit `347dc57`).
- Added `gui/vite.config.ts` (byte-identical to `mod-core/gui/vite.config.ts`).
- Rewrote `gui/tsup.config.ts` to a single plain `defineConfig` with `external: ['react', 'react-dom', '@moduleforge/core-gui']`, dropping the second CSS entry.
- `gui/package.json`: `build` script reduced to `"tsup"`; `build:css` removed; the committed `file:.yalc/@moduleforge/core-gui` dependency removed (kept as optional peer + `peerDependenciesMeta`, matching `mod-contacts/gui`); `"./styles.css"` export preserved for shape-parity.
- Relocated the Ladle Tailwind entry from `gui/src/styles.css` to `gui/.ladle/styles.css`; updated `.ladle/components.tsx`'s import accordingly.
- Updated `gui/Makefile` header/help comments to drop CSS-build language; cleared the stale `3RgF` yalc known-issue bullet from `AGENTS.md` and rewrote the corresponding `.claude/CLAUDE.md` gotcha.
- Validation: `make build.gui` produced JS+DTS with no `dist/index.css`; a fresh `bun install` (no `.yalc/` populated) succeeded; `make lint.gui` passed. The full app-mfdemo consumer-regression check could not be fully executed in the task-agent sandbox (yalc publish/add against the shared store was denied by the permission layer) — partially substituted by confirming the built JS still carries the Tailwind class literals app-mfdemo's `@source` scan depends on. Flagged as a follow-up (`ThVz`, `HSiS`).
- Interstitial manager fix after this phase: `b6465b6` — "fix yalc wording stutter in CLAUDE.md gotcha bullet."

### Phase 02 — Stale artifact cleanup (`stale-artifact-cleanup`)
Merge: `927b31aed7d45ad0d40007b15cde031bf840aa8a` (branch `phase-02-task-01-remove-nextjs-scaffold`, commits `7fab552`, `14f215d`).
- Removed git-tracked Next.js scaffold: `gui/README.md`, `gui/components.json`, `gui/public/*.svg` (and the now-empty `gui/public/`).
- Replaced `gui/.gitignore` with the minimal six-line sibling form.
- Task-agent dispatch hit two consecutive infrastructure failures (API drop, then a stall) after the substantive work was already committed — no content issue, pure infra flakiness (`h4Un`).
- **Gate-review correction:** Requirement 3 (removing stray untracked empty dirs and the 294MB `gui/.next/` build cache) was never actually effective against the project root — `git worktree add` does not copy untracked files into a fresh task worktree, so the task agent's `rm -rf`/verification silently checked an already-clean location. The manager verified this directly against the real project root, found all four stray dirs and `gui/.next/` still present, and removed them there directly, then re-verified. This produced the `de6E` followup, a generalizable process finding about worktree isolation and untracked-file cleanup tasks.

### Phase 03 — Dev-stack disposition (`dev-stack-disposition`)
Merge: `72cd22a463de7e4032eceba74aea14dd193fcb43` (branch `phase-03-task-01-remove-gui-compose-service`, commits `8a24b23` and an earlier `99d3e8c`).
- **Decision: remove** (not repurpose) the docker-compose `gui` service and `gui/Dockerfile.dev` entirely. Deleted the `deploy/local/docker-compose.yml` `gui:` block (lines 183-208) and its services-list header comment; `git rm`'d the broken `gui/Dockerfile.dev` (pre-rename paths, npm/pnpm, `next dev` against a directory with no Next.js pages).
- Updated `AGENTS.md` First-time-setup step 5 to drop the "GUI dev server (3000)" clause, with a forward-reference pointer to `make preview` (not yet landed at that point — Phase 05 adds it).
- Updated the root `Makefile` dev-orchestration comment to drop the GUI-dev-server line; left `_dev.urls`'s app-mfdemo pointer untouched (already correct).
- Validation: no live gui/Dockerfile.dev references remained outside `deploy/`; compose file still parsed via `docker compose config -q`.
- Flagged the forward-reference-to-not-yet-existing-target sequencing concern (`5arF`); the phase-03 gate review separately noted `deploy/local/README.md`'s stale "API and GUI containers... Phase 3+ may add them to compose" prose, untouched and unowned by any phase (`S74e`).
- Interstitial manager fix after this phase: `87262ad` — "qualify make preview as forthcoming in AGENTS.md setup step 5" (closing the `5arF` sequencing gap in real time, before Phase 05 actually landed the target).

### Phase 04 — CI bun migration (`ci-bun-migration`)
Merge: `51a64a7a8ceac826d96030f59005b241335cf693` (branch `phase-04-task-01-migrate-ci-to-bun`, commits `5c3c12b`, `1c7f73c`).
- Rewrote the `lint`, `test`, and `build` jobs in `.github/workflows/ci.yml` to use `oven-sh/setup-bun@v2` (pinned via a new `BUN_VERSION` env var following the existing version-pinning convention) plus a single root-level `bun install --frozen-lockfile`, removing all pnpm/corepack steps and the duplicate gui-scoped install; `actions/setup-node@v4` retained (still needed by `gui/`'s `preflight` target).
- Cleared the stale "CI uses pnpm" bullets from `AGENTS.md` and `.claude/CLAUDE.md`; annotated `r86L` and `3RgF` as `resolved: true` in `plan/next-steps.yaml` (kept, not deleted, for history).
- Confirmed a pre-existing, package-manager-independent limitation: `make build.gui`'s `tsup --dts` step still fails in CI (`Cannot find module '@moduleforge/core-gui'`) because CI does not populate `.yalc/` — out of this task's narrower scope (clearing the pnpm/bun mismatch only) and flagged as a follow-up (`VqCM`).
- Gate review additionally noted `plan/summary-bun-migration.md` (a frozen historical doc) still describing `r86L`/`3RgF` as open (`kcN3`, low priority — historical prose) and the absence of a CI dependency cache step (`twPi`, pre-existing, not a regression).

### Phase 05 — Preview target & README alignment (`preview-and-readme`)
Merge: `f6e303e1dba25dd2ded61f4ee4ebebf161b6a320` (branch `phase-05-task-01-add-preview-target`, commit `027c6dc`).
- Added a `.PHONY: preview` target to the root `mod-users/Makefile` (Dev orchestration section), delegating to `$(MAKE) -C gui dev.start` so the Ladle port (61002) stays owned by `gui/Makefile` as the single source of truth.
- Validated live: `make preview` started Ladle, `curl http://localhost:61002` returned HTTP 200, `make help` lists it correctly.
- The cross-repo aggregate `README.md` module-table/workbench-row correction was explicitly **not** done here (outside `project_root`, separate git repo) — flagged for the manager (`0PHW`).
- Interstitial manager fix after this phase: `9dc3726` — "mark make preview as available in AGENTS.md" (updating the Phase 03 forward-reference now that the target actually exists).

### Phase 06 — Documentation updates (`doc-updates`)
Merge: `397d3026f5b9814cd3263738d61a0543454e8671` (branch `phase-06-task-01-update-architecture-docs`, commit `dbccabb`). Registered by the Phase 04 architectural-implications check; deliberately scoped to run last, after all implementation phases landed.
- **`docs/architecture.md`**: replaced the `src/styles.css`/base-styles claim in the "GUI component library" section with a description of the plain-tsup JS+DTS-only build, the `.ladle/styles.css` workbench-only Tailwind entry, and `@source`-based consumer CSS generation. Removed the "GUI dev server | 3000" row from the local-development-stack table and added a pointer to `make preview` (Ladle) / `app-mfdemo` as the actual local-preview/integration paths.
- **`docs/project-structure.md`**: `gui/` layout block updated to drop `Dockerfile.dev`, add `vite.config.ts` and `.ladle/` (noting `styles.css`), remove `src/styles.css`, and refine the `tsup.config.ts` comment ("no CSS"). `deploy/` block's compose-file description comment dropped "GUI dev server."
- **`docs/mod-users-spec.md`**: reviewed, no edit needed — its GUI-packaging paragraph and app-mfdemo references were already accurate.
- Deliberately left one qualitative claim unedited (the `docs/project-structure.md` note that `gui/` depends on `@moduleforge/core-gui` "via a `file:.yalc/` link") on the grounds that the yalc-linking mechanism for local dev is unchanged even though the committed `file:` package.json entry is gone, and `AGENTS.md` retains identical phrasing post-Phase-01.
- Flagged a pre-existing, out-of-scope terminology inconsistency: `docs/project-structure.md` and `docs/mod-users-spec.md` both describe the `src/stories/` convention/showcase role using "Storybook" language, when the actual tool is (and has been, since before this plan) Ladle (`PZfi`).
- Interstitial manager fix after this phase: `ebabb42` — "polish phase-06 doc-update wording (path prefix, abbreviation)," a small style cleanup closing a phase-06 gate-review finding.

## Key decisions

- **Docker-compose `gui` service + `Dockerfile.dev`: remove, don't repurpose.** Option A was chosen over repurposing the compose service into a Ladle-backed container — both `docker-compose.yml`'s `gui` block and `gui/Dockerfile.dev` were broken beyond salvage (pre-rename paths, wrong package manager, `next dev` against a directory with no Next.js pages), and the sibling convention already has `make preview` (Ladle) for local component workbench and `app-mfdemo` for integration testing, so there was no gap to fill by repurposing. Recorded in `plan/notes/gap-analysis-and-scope.md` `## Answer`; documented as final-for-this-session in Phase 03's `## Assumptions` (a repurpose-to-Ladle path remains a documented, easily-amendable alternative if the decision is later revisited, since it only touches three files).
- **`@moduleforge/core-gui`: demote to optional peer dependency, drop the committed `file:.yalc/...` entry.** The committed `file:` dependency was uniquely inconsistent with the sibling convention (`mod-contacts/gui` treats `core-gui` as an optional peer only) and is what broke fresh `bun install`/CI. The fix keeps the existing `peerDependencies`/`peerDependenciesMeta` entries untouched — the local yalc-linking workflow for actually resolving the import at build/lint time is unchanged, only the committed-dependency inconsistency is corrected.
- **No CSS build for the library — confirmed, not assumed.** The original dispatch assumed siblings emit `dist/index.css` from plain tsup. Investigation (`sibling-build-mechanism.md`) established that none of the four siblings actually do this; the `"./styles.css"` export map entry is a vestigial no-op everywhere. mod-users' alignment therefore drops its CSS build rather than reproducing a mechanism that doesn't exist upstream, and the Ladle-only Tailwind entry moves to `.ladle/styles.css` to match sibling convention (it never ships to consumers).
- **Phase 06 doc-update scope: `docs/architecture.md` and `docs/project-structure.md` only** (plus a review-only pass on `docs/mod-users-spec.md`). `deploy/local/README.md`'s stale gui-container prose (`S74e`) and the Storybook/Ladle terminology inconsistency (`PZfi`) were both explicitly identified as out of this phase's scope, requiring separate scope decisions rather than being folded in opportunistically.
- **Aggregate-repo README correction stays out of this plan.** Both the original dispatch scoping (overview.md) and Phase 05's task doc treat the root `/Users/zane/playground/moduleforge/README.md` module-table/workbench-row correction as a cross-repo item outside `project_root`, to be routed to the aggregate-repo process by the manager rather than authored as a mod-users task.
- **Worktree isolation is unsafe for untracked-file/stray-directory cleanup steps.** The Phase 02 gate-review finding (`de6E`) establishes that `git worktree add` does not copy untracked files, so any task requirement that cleans up untracked artifacts (stray empty dirs, build caches) silently no-ops when dispatched into an isolated task worktree and self-reports success. This is treated as a process/tooling finding for future plans, not specific to this session's scope.

## Follow-up items

All 12 items currently recorded in `plan/followups.yaml`, organized by theme:

**Consumer-regression / yalc-in-sandbox verification gaps**
- `ThVz` (gui-build-alignment) — The app-mfdemo consumer-regression check was not fully executed: app-mfdemo has no `node_modules` installed and its own `@moduleforge/users-gui`/`@moduleforge/core-gui` deps are also `file:.yalc/...` links requiring `yalc publish`/`yalc add` there, outside this task's edit boundary. Recommend the manager (or a session with broader permissions) run the full check: rebuild `users-gui`, link it into app-mfdemo via yalc, confirm Tailwind still emits the expected classes.
- `HSiS` (gui-build-alignment) — The sandbox's permission classifier denies `yalc publish` (and likely `yalc add`) run from a task-agent session against the shared `~/.yalc` store or a sibling repo, even for legitimate validation named in the task doc's own Assumptions. Environment/tooling gap worth noting for future GUI-alignment tasks generally.
- `cJ41` (gui-build-alignment) — `AGENTS.md` First-time-setup step 4 still describes `@moduleforge/core-gui` as reached via a `file:.yalc/` link — now technically imprecise (it's an optional peer dep resolved by yalc, not a committed `file:` dependency), but the task doc explicitly instructed not to touch step 4. Worth a follow-up wording pass for full accuracy.

**CI / build-environment gaps**
- `VqCM` (ci-bun-migration) — Pre-existing, package-manager-independent limitation confirmed live: `make build.gui`'s `tsup --dts` step fails with `Cannot find module '@moduleforge/core-gui'` in any environment (including CI) where `.yalc/` is not populated, since `gui/src` still imports the package directly. Manager should track a follow-up for resolving `@moduleforge/core-gui` types in CI (publish/link the package, or scope `build.gui` out of the CI build job).
- `twPi` (phase-04-gate) — The migrated `ci.yml` has no `actions/cache` step for the bun install across CI runs. Pre-existing condition (the prior pnpm workflow had no cache step either), not a regression — worth a follow-up for CI wall-clock time if the manager wants to optimize further.

**Documentation staleness (out of this plan's phase scope)**
- `S74e` (phase-03-gate) — `deploy/local/README.md`'s "## What's not here" section still contains phase-3-era prose ("API and GUI containers — they run on the host during development (Phase 3+ may add them to compose)") that is now doubly stale given the compose `gui` service existed and was just removed. Not owned by any registered plan phase (Phase 06 covers `docs/architecture.md`/`docs/project-structure.md` only). Worth a follow-up wording pass.
- `kcN3` (phase-04-gate) — `plan/summary-bun-migration.md` (a dated, frozen historical summary from a prior migration phase) still calls `r86L` a "[BLOCKER]" and references `3RgF` as an open follow-up, but both are now marked resolved in `plan/next-steps.yaml`. Low-priority — frozen historical prose, not live documentation, but worth a wording sync if desired.
- `PZfi` (doc-updates) — Pre-existing Storybook-vs-actual-tool-Ladle terminology inconsistency in `docs/project-structure.md` (`src/stories/` comment) and `docs/mod-users-spec.md` (use case 13, "story tool"/"Storybook role" framing). Predates this plan's task docs, out of Phase 06's scope, and needs a separate scope decision (rename "Storybook" → "Ladle" / reconcile the "no dedicated story tool" framing).

**Cross-repo item**
- `0PHW` (preview-and-readme) — Per the task doc's explicit instruction, the cross-repo aggregate-README correction (`/Users/zane/playground/moduleforge/README.md` module-table cell `mod-users … Next.js` → `yes (tsup lib)`, and the "GUI workbench" row) lives outside `project_root` in a separate git repo and was intentionally not touched. Remains outstanding for the manager to route to the aggregate-repo process.

**Tooling / process gaps (session-generalizable findings)**
- `de6E` (phase-02-gate) — Worktree isolation misses untracked cleanup: when a task's Requirements call for removing untracked/stray filesystem artifacts (not git-tracked), dispatching that task into an isolated task worktree silently no-ops against the actual project root, since `git worktree add` doesn't copy untracked files. Future plan tasks with untracked-file/stray-directory cleanup requirements should either target `project_root` directly for that step or have the manager independently re-verify against `project_root` after merge, rather than trusting the task worktree's self-reported validation.
- `5arF` (dev-stack-disposition) — The `AGENTS.md` pointer to `make preview` documented a target that did not exist yet in the Phase 03 branch in isolation (added by Phase 05, not yet run in that lineage). Intentional forward-reference per the task doc's own note, since resolved by the manager's interstitial fix (`87262ad`) and Phase 05 landing; flagged originally to confirm merge/phase sequencing wouldn't leave an inconsistent intermediate state if branches were inspected individually before Phase 05 landed.
- `h4Un` (stale-artifact-cleanup) — The task agent dispatch for the Phase 02 task hit two consecutive infrastructure failures (an API connection drop, then a stall) after completing the substantive work — no content issue, purely infra flakiness. Noted for awareness in case this recurs on future phases/plans.
