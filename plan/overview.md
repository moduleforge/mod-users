# users-gui Integration Seams

## Purpose and scope

Wave 1 (parallel with mod-core's `shared-home-switcher`) of wave plan `home-app-switcher`. Makes `@moduleforge/users-gui` (`mod-users/gui`) embeddable by an app that owns its own router, base URL, and session key, so wave 2's `adopt-users-gui-auth` (app-mfmanager, Next.js 15 app router) can replace MFManager's hand-rolled login with the standard components. The user does not care if MFManager's login page look changes and prefers standard users-gui components.

In scope (all under `mod-users/gui`, plus plan docs): a runtime configuration API for the API base URL (G1), the 401/unauthenticated handling and logout target (G2), and the token storage key (G3); `AuthPage` registration hiding, a forgot-password entry point, and an exported `isSafeReturnPath`; removal of the dangling `./styles.css` export; tests; a consumer integration guide with route expectations and Next.js usage; build/consumption verification including a no-`window` server render; and a written hand-off. Out of scope: any edit outside `mod-users` (app-mftodo/app-mfdemo/app-mfmanager/mod-core change nothing here); backend changes (a configurable reset-link path and a registration on/off switch are filed as followups); `ClientLayout`/`SidebarNav` changes; delegating `request()` to core-gui; a version bump or publish (users-gui is never published).

Backward compatibility is a hard constraint: with no new configuration call, no new prop, behavior for app-mftodo, app-mfdemo and other consumers is identical. The single intentional semantic fix is `NEXT_PUBLIC_API_BASE_URL=""` now meaning same-origin instead of falling through to `http://localhost:8080`.

## Current status

Planned, not started. Phase 1 task 001 starts first (no pre-conditions beyond a working `bun` toolchain and a built `mod-core/gui`; see `AGENTS.md` First-time setup, including its worktree-symlink gap). Independent of mod-core's wave-1 plan in both directions: no new core-gui symbol is imported.

## Overview

### Clarified request and verification summary

Source verification of each seam (file and line references, rejected alternatives, decisions) is in [`notes/seam-design.md`](./notes/seam-design.md). Route and Next.js expectations are in [`notes/embedding-and-routes.md`](./notes/embedding-and-routes.md). Consumption and hand-off mechanics are in [`notes/consumption-and-handoff.md`](./notes/consumption-and-handoff.md). Findings that matter:

- G1 is wider than reported: besides `api`'s singleton, `API_BASE_URL` is used by `LoginForm` (OIDC start), `fetchProviders`, `oidc-config.ts` and `oidc-provider.ts`; all become lazy.
- G2: the 401 redirect also fires from `AuthProvider`'s mount-time `GET /v1/self`; `logout()` has its own `/auth/login` literal.
- The components are already router-agnostic (no router import in `src`); no adapter or `navigate` prop is needed for Next. What Next needs is documentation, a client-only import rule (the bundle has no `"use client"`), and verification.
- `AuthPage`/`LoginForm` have no way to reach `ForgotPasswordPage`; an optional `onForgotPassword` is added.

### Interface wave 2 consumes

Everything is exported from `@moduleforge/users-gui` (`gui/src/index.ts`). Names are fixed by this overview; tasks must not rename them.

Configuration (`gui/src/lib/config.ts`), module-level, read lazily on every use (no import-order dependency; call it at the top of any client module before the first `AuthProvider` mount):

```ts
interface UsersApiConfig {
  baseUrl?: string;                    // '' = same-origin (relative /v1/...); http(s) URL or '/prefix' path; trailing slash stripped
  tokenStorageKey?: string;            // default 'auth_token'
  unauthenticatedRedirectUrl?: string; // default '/auth/login'; path ('/x') or http(s) URL; same rule as mod-core
  unauthenticatedReturnParam?: string | null; // default null; e.g. 'return' appends ?return=<pathname+search>
  onUnauthenticated?: ((ctx: UnauthenticatedContext) => void) | null; // overrides the default redirect; token is cleared first
}
interface UnauthenticatedContext { returnPath: string }  // location.pathname + location.search, never the hash; '/' outside a browser or if unsafe
function configureUsersApi(config: UsersApiConfig): void   // omitted/undefined field = unchanged; invalid value = TypeError, nothing applied
function resetUsersApiConfig(): void                       // defaults; mainly for tests
function getApiBaseUrl(): string                           // configured > NEXT_PUBLIC_API_BASE_URL (if defined, '' allowed) > window.__USERS_API_URL__ (string, '' allowed) > 'http://localhost:8080'
const USERS_TOKEN_KEY = 'auth_token'
function getTokenStorageKey(): string
function getStoredToken(): string | null                   // null on the server
function clearStoredToken(): void
```

Behavior: a 401 from any users-gui request (not `skipAuthRedirect`) clears the stored token, then calls the custom `onUnauthenticated` if set, else navigates `window.location.href` to `unauthenticatedRedirectUrl` (with `?<returnParam>=<encoded returnPath>` when configured, skipped when already on that path). Defaults reproduce today's behavior exactly (`/auth/login`, no return path). Deprecated but kept: `API_BASE_URL` (load-time snapshot), `api`, `createUsersClient`.

Component additions (all optional, default-preserving):

- `AuthProvider loginPath?: string` (default `'/auth/login'`): the path `logout()` and the 401-in-`refreshUser` path pass to `onNavigate`. Existing `onNavigate?: (path: string) => void` unchanged.
- `AuthPage allowRegistration?: boolean` (default `true`; `false` hides "Create one" and the register panel, forces login mode; UI-only, the API register endpoint stays open).
- `AuthPage onForgotPassword?: () => void` and `LoginForm onForgotPassword?: () => void`: render a "Forgot password?" control only when supplied.
- `isSafeReturnPath(candidate: string | null): candidate is string` exported for validating an app's own `?return=`.
- Unchanged and still app-owned: `AuthPage onAuthenticated` / `LoginForm onSuccess` take no argument (the app reads its own `?return=`, sanitizes it, navigates, and passes it as `returnPath` for the OIDC round trip); `OidcCallbackPage onComplete(returnPath)`/`onError(message)`; `ResetPasswordPage token onSuccess onNavigateToLogin`; `ForgotPasswordPage onNavigateToLogin`; `EmailCodePage onSuccess onNavigateToLogin`; `RequireAuth onUnauthenticated onUnauthorized`.

Removed: the dangling `./styles.css` package export (it never resolved). Consumers need core-gui's `styles.css`/`tokens.css` plus a Tailwind v4 `@source` over users-gui's `dist/`.

Route expectations for an embedding app (details in the embedding note): the app chooses its login route (MFManager keeps `/login`); the backend hard-codes `/reset-password?token=` for reset emails; `AUTH_FRONTEND_RETURN_URL` (deploy env, `<gui>/auth/oidc/return`) must be served by `OidcCallbackPage` inside an `AuthProvider` (token and `return` arrive in the URL fragment); `/forgot-password`, email-code, and `/oidc-config` are app choices (no `/oidc-config` page component exists). Next.js: import only from `'use client'` modules; wrap `useSearchParams` users in `<Suspense>`; single `react` instance across users-gui, core-gui and the app; no router adapter needed.

Suggested wave-2 configuration: `configureUsersApi({ baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8090', tokenStorageKey: 'mfmanager_session_token', unauthenticatedRedirectUrl: '/login', unauthenticatedReturnParam: 'return' })` and `<AuthProvider loginPath="/login" onNavigate={router.replace}>`. Setting `tokenStorageKey` to MFManager's existing key avoids both session migration and any change to MFManager's own `api-client.ts`; the label-cache invariant (G4) stays the app's concern.

### Phase 1: `gui-seams` (4 tasks)

- `001-runtime-config-base-url-and-token-key` (sonnet-med): `config.ts`, `configureUsersApi`, lazy base URL and token key everywhere, exports, tests. Must run first.
- `002-unauthenticated-handler` (sonnet-med): 401 handler options, return path, same-path guard, `AuthProvider loginPath`, tests. After 001.
- `003-auth-component-props` (sonnet-med): `allowRegistration`, `onForgotPassword`, exported `isSafeReturnPath`, tests, stories. After 001.
- `004-fix-styles-export` (sonnet-low): remove the `./styles.css` export. Independent.

Parallel-eligible after 001: 002, 003, 004 (004 may also run with 001).

### Phase 2: `consumer-docs-and-handoff` (2 tasks; after phase 1)

- `001-consumer-integration-guide` (sonnet-med): `gui/README.md` plus a root README pointer.
- `002-verify-consumption-and-handoff` (sonnet-med): build, export checks, no-router-import check, no-`window` `react-dom/server` render, consumer and backward-compat type-checks, README snippet type-check, finalize the hand-off note. After 001.

### Phase 3: `doc-updates` (1 task; after phase 2)

- `001-update-architecture-docs` (sonnet-high, architect-frontend): `docs/architecture.md`, `docs/mod-users-spec.md`, `docs/project-structure.md`, `AGENTS.md`. Registered because the plan modifies a public API/component boundary.

### Hand-off summary for wave 2

Wave 2 needs: the merge SHA of this plan on mod-users `main` (filled by the manager at finalization), the aggregate checkout on `main` at or after it, `make pins.update REPOS="mod-users"` (plus a compatible mod-core pin) in app-mfmanager, and the app-side build wiring (second workspace member `../mod-users/gui`, named Docker context after core-gui, three-package single-React check, `@source` of users-gui's `dist/`). app-mftodo and app-mfdemo need nothing; app-mftodo may later retire `configure-users-gui.ts` and its `/auth/login` alias (its followup ZyTU). Full list: [`notes/consumption-and-handoff.md`](./notes/consumption-and-handoff.md).

### Decisions made and open questions

Decisions the plan made (flagged so the manager/user can override before execution):

1. Module-level `configureUsersApi` (lazy), not a provider prop: the OIDC helpers are plain functions and cannot read context.
2. `""` in `NEXT_PUBLIC_API_BASE_URL` or `window.__USERS_API_URL__` now means same-origin (the only behavior change).
3. Token key is configurable, not just readable, so MFManager's existing sessions survive adoption.
4. A custom `onUnauthenticated` always runs after the token is cleared (mod-core's custom handler replaces the default entirely, including token removal).
5. `./styles.css` export removed rather than built.
6. The mount-time `GET /v1/self` 401 keeps its hard-redirect behavior (default-preserving); the same-path guard makes it harmless on the login page.

Open questions (non-blocking; defaults above apply unless changed):

- Should the bundle gain a `"use client"` banner (tsup) so server components can import it? Not adopted (breaks calling `configureUsersApi`/`getStoredToken` from server modules, and Vite consumers warn); wave 2's MFManager shell is already a client component.
- Should hiding registration also have a backend switch? Not planned; the API register endpoint is ungated and a first-user hook grants admin (followup context in the MFManager investigation, `5KxC`).
- Whether wave 2 keeps `mfmanager_session_token` (recommended, zero migration) or adopts `auth_token` (one-time sign-out) is an app-mfmanager decision.

Followups filed by this planning pass: `sUuz` (mod-users: configurable password-reset email path and `/oidc-config` banner path), `TbfT` (app-mfdemo: `/auth/reset` vs emailed `/reset-password`). Existing mod-users followup `HPJi` is closed by this plan's merge (manager action).

## Assumptions

- mod-core's wave-1 `unauthenticatedRedirectUrl` keeps its name and validation (read from its plan worktree, not yet merged); a rename there would require only a doc change here.
- `gui` tests run with `bun test` under happy-dom, and `mod-core/gui` can be built before `gui/` (AGENTS.md).
