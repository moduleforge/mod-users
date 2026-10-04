# Consuming the New users-gui and the Wave-2 Hand-off

## Purpose and scope

Records how consumers obtain `@moduleforge/users-gui`, what that means for delivering these seams, and exactly what wave 2 (`adopt-users-gui-auth` in app-mfmanager) needs from this plan. Mirrors mod-core's `shared-home-switcher` note of the same name. Facts below were read from `mod-users/AGENTS.md`, `gui/package.json`, `gui/tsup.config.ts`, `versions.lock.yaml`, `app-mftodo/versions.lock.yaml`, `app-mftodo/gui/src/lib/configure-users-gui.ts`, `app-mftodo/gui/src/App.tsx`, and mod-core's matching note. The verification task (phase 2, task 002) fills in the marked placeholders.

## How users-gui is versioned and distributed

- `gui/package.json` is `version 0.1.0` and is **never published** (README's `npm install` line is aspirational; consumers use bun workspaces). **No version bump is part of this plan.**
- The deliverable is a **commit on mod-users `main`** (aggregate checkout `/Users/zane/playground/moduleforge/mod-users`) from which `gui/dist` is rebuilt by `bun run build`; `dist` is not committed.
- users-gui imports `@moduleforge/core-gui` at module top level (declared an optional peer but effectively required), so any consumer must also resolve and build `mod-core/gui` first. These seams add **no new core-gui requirement** (no new core-gui symbol is imported) and do not depend on mod-core's wave-1 plan landing; the two plans are independent in both directions. `unauthenticatedRedirectUrl` is deliberately the same name and validation in both libraries.

## Consumers

- **app-mftodo** (workspace member `.gui-siblings/mod-users/gui`, refreshed from the aggregate checkout's working tree on `make gui.deps`; pin `mod-users` in `versions.lock.yaml` for CI). All seams are additive and default-preserving, so it needs **no change** and no pin bump. Optional later cleanup (separate, app-mftodo-owned): drop `gui/src/lib/configure-users-gui.ts` (use `configureUsersApi({ baseUrl: window.location.origin })`) and the `/auth/login` compat route (use `unauthenticatedRedirectUrl: '/login'`); closes its followup ZyTU.
- **app-mfdemo** (Next 15 via yalc): unchanged; additive API only. Its `/auth/reset` vs emailed `/reset-password` mismatch is pre-existing (followup filed in that repo).
- **app-mfmanager**: not a consumer yet; wave 2 adds it.

## Exact hand-off wave 2 needs

1. This plan's branch `plan/users-gui-integration-seams` is merged to `mod-users` `main`; the manager reports the **merge commit SHA**: `<to be filled by the manager at finalization>` (do not invent one).
2. The aggregate checkout `/Users/zane/playground/moduleforge/mod-users` is on `main` at or after that SHA when app-mfmanager builds its GUI deps (its `.gui-siblings`/workspace materialization copies the aggregate working tree, not a git ref).
3. `cd mod-users/gui && bun run build` succeeds after `mod-core/gui` is built, and `dist/index.d.ts` exports the symbols in the overview's "Interface wave 2 consumes" (verified by phase-02 task 002; verification result: `<to be filled by task 002>`).
4. app-mfmanager's `versions.lock.yaml`: `make pins.update REPOS="mod-users"` to the merge SHA (plus `mod-core` to a SHA that builds users-gui; users-gui's own lock pins an old mod-core `14fa8f5` which is harmless here, but app-mfmanager's single mod-core pin must satisfy both users-gui and core-gui consumers).
5. app-mfmanager build wiring (app-side, not mod-users work), extending its core-gui pattern: add `../mod-users/gui` as a second bun workspace member and a named Docker context after core-gui in the ordered `gui.deps`; extend the single-React check to three packages; install users-gui's runtime deps (`radix-ui`, `class-variance-authority`, `lucide-react`, `tailwind-merge`, `tw-animate-css`) via the workspace; `@source` users-gui `dist/` in its CSS.
6. App-side configuration (see the overview interface): `configureUsersApi({ baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8090', tokenStorageKey: 'mfmanager_session_token' (or accept the default and a one-time sign-out), unauthenticatedRedirectUrl: '/login', unauthenticatedReturnParam: 'return' })`, `AuthProvider loginPath="/login"`.
7. No npm publish, no `yalc`, no version bump.

## Open points for the manager

- Close mod-users followup `HPJi` (users-gui hard-navigates on 401) when this plan merges; app-mftodo `ZyTU` stays open for its own cleanup (and its mod-core half is covered by mod-core wave 1).
- Known environment gap (AGENTS.md): building `mod-core/gui` through a worktree's `../mod-core` symlink can fail to resolve its devDependencies; build it once from the real checkout before building `gui/` in a task worktree.
