# Migrate OidcConfigPage and OidcSetupGate Into users-gui

## Purpose and scope

The `/oidc-config` screen (setup-token confirm flow the server banner points to, plus the admin reconfigure mode) exists only app-locally in app-mfdemo (`/Users/zane/playground/moduleforge/app-mfdemo/src/app/oidc-config/`: `page.tsx` 645 lines, `provider-add-modal.tsx` 388, `provider-edit-modal.tsx` 727, `layout.tsx` a passthrough). Move it into users-gui as a standard, router-agnostic, exported `OidcConfigPage` so every app gets it and app-mfdemo (wave 5) reduces to "delete the local page, mount the standard one". The migration MUST be behavior-preserving: app-mfdemo's current implementation is the reference (it has no tests; this task writes them). Also extract the OIDC-readiness gate from `ClientLayout` as a reusable `OidcSetupGate` so an app that does not use users-gui's admin chrome (app-mfmanager) can still enforce the gate. Depends on tasks `001` (lazy config; the helpers it calls read the configured base URL) and `005` (`USERS_GUI_ROUTES`). Scope: `gui/src/components/` (new files below, `client-layout.tsx`), `gui/src/lib/auth-context.tsx` (one guard), `gui/src/index.ts`, tests, stories. Nothing outside `mod-users` is edited (app-mfdemo is read-only here).

Backend contract: the server sends users to `/oidc-config` in three places: the action-required `503` envelope (`api/internal/auth/require_confirmed.go:38`, extra `{state}`), the stderr setup-token banner (`api/cmd/server/main.go:251`, `GUIBaseURL + "/oidc-config"`), and the REST routes the page drives (`/v1/oidc-config/status|confirm|saved`, `/v1/oidc-config/providers/*`; all via the existing exported helpers). The path is therefore backend-fixed; the page must be mounted at `USERS_GUI_ROUTES.oidcConfig`.

## Requirements

1. New files, copied from app-mfdemo with only the changes listed here (git-diff against the originals should show import rewrites plus the listed edits):
   - `gui/src/components/oidc-config-page.tsx` (from `page.tsx`): export `OidcConfigPage` and `OidcConfigPageProps`.
   - `gui/src/components/oidc-provider-add-modal.tsx` (from `provider-add-modal.tsx`) and `oidc-provider-edit-modal.tsx` (from `provider-edit-modal.tsx`): internal components (NOT exported from `index.ts`; open question in the overview). Rename `ProviderAddModal`/`ProviderEditModal` to `OidcProviderAddModal`/`OidcProviderEditModal`.
   - Rewrite imports: `@moduleforge/users-gui` self-imports become relative imports (`../lib/api`, `../lib/oidc-config`, `../lib/oidc-provider`, `../lib/auth-context` for `useOptionalAuth`, `./ui/switch`, `./error-message`); `@moduleforge/core-gui` imports stay (already a dependency); `lucide-react` is already a dependency. Keep the `'use client'` banner.
2. Props (all optional), replacing app-local navigation:
   - `onComplete?: () => void`: called after a successful **setup-token** confirmation, after `redirectDelayMs` (default `2000`, the reference `REDIRECT_DELAY_MS`), replacing the hard-coded `window.location.assign('/auth/login')`. Not called in admin mode (the reference stays on the page). The pending timer is cleared on unmount (the only intended behavioral addition, so unmounted pages do not navigate). Docs for the prop: apps SHOULD perform a full-page navigation to their login route here (for example `window.location.assign(USERS_GUI_ROUTES.login)`), because `OidcSetupGate`/`ClientLayout` re-probe OIDC status on mount and a soft client navigation leaves them in the stale "needs setup" state (this is the reference's stated reason for the hard navigation).
   - `redirectDelayMs?: number`: default `2000`; `0` calls `onComplete` on the next tick (for tests/apps that show their own success UI).
   Everything else (dual token/admin mode, strict-confirmation handling when the API returns `200 confirmed:false`, toggles with dirty detection, revert, per-provider OK/Failed badges, add/edit modals, test-configuration result banner parsed from `?test_result=...` on mount and stripped with `history.replaceState` using the current pathname, `window.open` for the provider test URL) stays as in the reference. The page reads `window.location` only inside effects/handlers (SSR-safe) and imports no router.
3. `AuthProvider` guard (`lib/auth-context.tsx`): when handling an `ApiActionRequiredError` (mount effect, `refreshUser`, `completeExternalLogin`), skip calling `onNavigate(err.path)` if `typeof window !== 'undefined' && window.location.pathname === err.path`. Rationale: `GET /v1/self` itself returns the `503` action-required envelope while OIDC is unconfirmed, so an `AuthProvider` mounted above `/oidc-config` would otherwise navigate to the page it is already on. Default-preserving everywhere else; add a focused test. (The reference avoids this by not mounting `AuthProvider` on that route; this guard makes the standard page safe either way.)
4. `OidcSetupGate`: extract the OIDC readiness logic (the `loading`/`ok`/`needs-setup` probe via `fetchOIDCStatus`, the redirect effect calling `onNavigateToConfig` when needs-setup and `currentPath !== USERS_GUI_ROUTES.oidcConfig`, the loading screen) from `client-layout.tsx` into `gui/src/components/oidc-setup-gate.tsx`, exporting `OidcSetupGate` and `OidcSetupGateProps` with `currentPath: string`, `onNavigateToConfig?: () => void`, and `children` as a render function `(state: 'ready' | 'setup') => React.ReactNode` (`'setup'` is the one state in which the config page is allowed through, without an `AuthProvider`). `ClientLayout` composes it and its rendered DOM and props are unchanged (`onNavigateToConfig` and `currentPath` keep their meaning; its `CONFIG_PATH` literal becomes `USERS_GUI_ROUTES.oidcConfig`). Add a test that `ClientLayout` still renders the same three states.
5. Export from `gui/src/index.ts`: `OidcConfigPage`, `OidcConfigPageProps`, `OidcSetupGate`, `OidcSetupGateProps`.
6. Tests (`bun test`, testing-library, happy-dom; mock `fetch` per URL; restore state in `afterEach`; no network). Port the reference's behaviors into assertions, at minimum: loading then provider list from `/v1/oidc-config/status`; status fetch failure shows the error card; token mode: Save disabled until a toggle changes (dirty detection), submit posts `setup_token` (trimmed) with `enabled_providers` limited to configured+enabled and `opt_out` when none, success shows the success state and calls `onComplete` after `redirectDelayMs` (fake timers) and clears the token input; `confirmed:false` response shows the failing-provider error and does NOT call `onComplete`; admin mode (inside `AuthProvider` with an admin user and token): token field hidden, submit sends `Authorization: Bearer`, stays on the page with "Configuration saved." and refreshed status, no `onComplete`; revert restores toggles from `/v1/oidc-config/saved` and shows the revert message (and the empty-saved message); `?test_result=ok|fail&...` query renders the banner once and strips the query (assert `window.location.search` is empty after mount); add and edit modals open (disabled without auth), their submit/revert/test-URL calls hit the configured base URL (`configureUsersApi({ baseUrl: '' })` produces relative URLs); `onComplete` timer cleared on unmount; renders inside and outside `AuthProvider`. Gate tests: loading screen, `needs-setup` calls `onNavigateToConfig` off the config path and renders `'setup'` children on it, `ok` renders `'ready'` children, a fetch failure is treated as needs-setup (reference behavior).
7. Add a Ladle story for `OidcConfigPage` (mocked fetch) if the existing story setup supports it; otherwise note why not in the task report.
8. Record in the task report a side-by-side check against the reference: list the reference file's visible strings/aria labels and confirm each is present unchanged in the migrated components (`diff` of the extracted text is sufficient), so wave 5 can delete the local page with no visible change.

## Validation

- `cd gui && bun run typecheck && bun test` pass.
- `grep -rn "react-router\|next/\|from '@moduleforge/users-gui'" gui/src/components/oidc-*.tsx` finds nothing.
- `grep -rn "window.location.assign\|auth/login" gui/src/components/oidc-config-page.tsx` finds nothing (navigation is the `onComplete` prop only).
- `ClientLayout` existing stories/tests unchanged and passing; `git diff --stat` touches only `gui/src/`.

## Metadata

architectural_impact: true

## Assumptions

- Wave 5 (app-mfdemo) mounts `OidcConfigPage` at `/oidc-config` and passes `onComplete` doing a full-page navigation to its login route.
- `core-gui` already exports `Badge`, `Button`, `Input`, `Label`, `Card*` (the reference imports them from there).

## References

- [`../notes/embedding-and-routes.md`](../notes/embedding-and-routes.md), [`../overview.md`](../overview.md)
- Reference implementation (read-only): `/Users/zane/playground/moduleforge/app-mfdemo/src/app/oidc-config/{page,layout,provider-add-modal,provider-edit-modal}.tsx`
- `gui/src/components/client-layout.tsx`, `gui/src/lib/{oidc-config.ts,oidc-provider.ts,auth-context.tsx}`, `api/internal/auth/require_confirmed.go`, `api/cmd/server/main.go` (~line 251)

## Checkpoint hints

- After the three components are copied with import rewrites and typecheck
- After `onComplete`/`redirectDelayMs` and the `AuthProvider` guard, with tests
- After the `OidcSetupGate` extraction and `ClientLayout` test
- After modal/test-banner tests and exports

## Status

- Outcome: succeeded (2026-10-04).
- Validation: `cd gui && bun run typecheck && bun test` pass (157 tests, 12 files); the router/self-import grep and the `window.location.assign|auth/login` grep find nothing; `git diff --stat` touches only `gui/src/` (no ClientLayout stories/tests existed before; new ClientLayout tests added and passing).
- Files: `gui/src/components/oidc-config-page.tsx`, `gui/src/components/oidc-provider-add-modal.tsx`, `gui/src/components/oidc-provider-edit-modal.tsx`, `gui/src/components/oidc-setup-gate.tsx`, `gui/src/components/client-layout.tsx`, `gui/src/lib/auth-context.tsx`, `gui/src/index.ts`, tests `gui/src/components/oidc-config-page.test.tsx`, `gui/src/components/oidc-setup-gate.test.tsx`, `gui/src/lib/auth-context.test.tsx`, story `gui/src/stories/OidcConfigPage.stories.tsx`.
- Reference parity: `diff` of each migrated file against the app-mfdemo original shows only import rewrites, the `Oidc*` renames, the `OidcConfigPageProps`/`onComplete`/`redirectDelayMs` additions with unmount cleanup, and two doc comments. No JSX text, placeholder, `title`, or `aria-label` line differs, so every visible string is unchanged.
- Notes: the `OidcSetupGate` render-prop children are wrapped in a fragment, so the `ClientLayout` DOM is unchanged. Tests set the URL with `window.happyDOM.setURL`, because happy-dom starts at `about:blank`, where `history.replaceState` cannot set a path.
