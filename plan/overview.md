# users-gui Integration Seams

## Purpose and scope

Wave 1 (parallel with mod-core's `shared-home-switcher`) of wave plan `home-app-switcher`. Makes `@moduleforge/users-gui` (`mod-users/gui`) embeddable by an app that owns its own router, base URL, and session key, so wave 2's `adopt-users-gui-auth` (app-mfmanager, Next.js 15 app router) can replace MFManager's hand-rolled login with the standard components. Per the user's principle "make everything standard unless there is a design reason not to", it also moves the two screens the backend itself sends users to, `/verify-email` and `/oidc-config`, into users-gui as standard pages, so every app mounts the same components (wave 5 then deletes app-mfdemo's local oidc-config page). The user does not care if MFManager's login page look changes and prefers standard users-gui components.

In scope (all under `mod-users/gui`, plus plan docs): a runtime configuration API for the API base URL (G1), the 401/unauthenticated handling and logout target (G2), and the token storage key (G3); `AuthPage` registration hiding, a forgot-password entry point, and an exported `isSafeReturnPath`; removal of the dangling `./styles.css` export; tests; a consumer integration guide with route expectations and Next.js usage; build/consumption verification including a no-`window` server render; and a written hand-off; plus, from the same principle: a standard `VerifyEmailPage`, a migrated `OidcConfigPage` (with the extracted `OidcSetupGate`), an exported `USERS_GUI_ROUTES` path set, and opt-in standard login return-path handling. Out of scope: any edit outside `mod-users` (app-mftodo/app-mfdemo/app-mfmanager/mod-core change nothing here); backend changes (a configurable reset-link path and a registration on/off switch are filed as followups); `SidebarNav` changes and any `ClientLayout` behavior change (its OIDC gate is only extracted into `OidcSetupGate`, props and DOM unchanged); a `/step-up` page (followup filed); delegating `request()` to core-gui; a version bump or publish (users-gui is never published).

Backward compatibility is a hard constraint: with no new configuration call, no new prop, behavior for app-mftodo, app-mfdemo and other consumers is identical. The single intentional semantic fix is `NEXT_PUBLIC_API_BASE_URL=""` now meaning same-origin instead of falling through to `http://localhost:8080`.

## Current status

Planned, not started (revised once to add tasks 005-007 and the standard-pages scope). Phase 1 task 001 starts first (no pre-conditions beyond a working `bun` toolchain and a built `mod-core/gui`; see `AGENTS.md` First-time setup, including its worktree-symlink gap). Independent of mod-core's wave-1 plan in both directions: no new core-gui symbol is imported.

## Overview

### Clarified request and verification summary

Source verification of each seam (file and line references, rejected alternatives, decisions) is in [`notes/seam-design.md`](./notes/seam-design.md). Route and Next.js expectations are in [`notes/embedding-and-routes.md`](./notes/embedding-and-routes.md). Consumption and hand-off mechanics are in [`notes/consumption-and-handoff.md`](./notes/consumption-and-handoff.md). Findings that matter:

- G1 is wider than reported: besides `api`'s singleton, `API_BASE_URL` is used by `LoginForm` (OIDC start), `fetchProviders`, `oidc-config.ts` and `oidc-provider.ts`; all become lazy.
- G2: the 401 redirect also fires from `AuthProvider`'s mount-time `GET /v1/self`; `logout()` has its own `/auth/login` literal.
- The components are already router-agnostic (no router import in `src`); no adapter or `navigate` prop is needed for Next. What Next needs is documentation, a client-only import rule (the bundle has no `"use client"`), and verification.
- `AuthPage`/`LoginForm` have no way to reach `ForgotPasswordPage`; an optional `onForgotPassword` is added.
- The backend already sends users to `/verify-email` (action-required `users.email_unverified`; verification is a 6-digit emailed code, `purpose: "verify_email"`, verify returns `204`) and `/oidc-config` (action-required `users.oidc_not_confirmed`, setup-token banner URL), and `AuthProvider` already navigates there, but no frontend renders either except app-mfdemo's app-local oidc-config page. Both become standard users-gui pages.
- A wrong email code is a `401`, which `request()` turns into a redirect to login unless `skipAuthRedirect` is passed: the new verify-email calls pass it (the pre-existing `EmailCodePage` has the same latent behavior; followup).

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
- `AuthPage onAuthenticated` / `LoginForm onSuccess` become `(returnPath: string | null) => void` (task 007): when `unauthenticatedReturnParam` is configured the library reads that query param, validates it with `isSafeReturnPath`, uses it for the OIDC round trip, and passes it to the callback (`null` otherwise; an explicit `returnPath` prop wins). Zero-argument callbacks keep working and apps that configure nothing see no change.
- Unchanged and still app-owned: `OidcCallbackPage onComplete(returnPath)`/`onError(message)`; `ResetPasswordPage token onSuccess onNavigateToLogin`; `ForgotPasswordPage onNavigateToLogin`; `EmailCodePage onSuccess onNavigateToLogin`; `RequireAuth onUnauthenticated onUnauthorized`.

Standard pages and routes (tasks 005 and 006; full prop reference in the consumer guide):

```ts
const USERS_GUI_ROUTES: {
  readonly login: '/auth/login'; readonly oidcReturn: '/auth/oidc/return';
  readonly forgotPassword: '/forgot-password'; readonly resetPassword: '/reset-password';
  readonly emailCode: '/auth/email-code'; readonly verifyEmail: '/verify-email'; readonly oidcConfig: '/oidc-config';
}
interface VerifyEmailPageProps {
  onVerified?: () => void;            // app navigates onward (its own sanitized return path or home)
  onNavigateToLogin?: () => void;     // "Sign in with a different account"
  email?: string;                     // default: AuthProvider user's email; else the page shows an email field
  message?: string;                   // default "Verify your email address before continuing."
  resendCooldownSeconds?: number;     // default 30; 0 disables
}
interface OidcConfigPageProps {
  onComplete?: () => void;            // setup-token confirm succeeded; called after redirectDelayMs. Apps should full-page-navigate to login.
  redirectDelayMs?: number;           // default 2000
}
interface OidcSetupGateProps {
  currentPath: string;
  onNavigateToConfig?: () => void;    // called when OIDC is unconfirmed and currentPath !== USERS_GUI_ROUTES.oidcConfig
  children: (state: 'ready' | 'setup') => React.ReactNode;  // 'setup' = only the config page may render, without AuthProvider
}
// also: api.auth.verifyEmail({ email, code }); EmailCodeRequest/EmailCodeVerifyRequest gain optional purpose
```

Backend path contracts the pages serve (action-required envelope `action.path`, followed by `AuthProvider`'s `onNavigate(err.path)`): `users.email_unverified` (403) -> `/verify-email` -> `VerifyEmailPage`; `users.oidc_not_confirmed` (503, extra `{state}`) -> `/oidc-config` -> `OidcConfigPage`; `users.step_up_required` (409) -> `/step-up` -> no page yet (followup). `ClientLayout onNavigateToConfig` stays the mechanism for apps using the admin chrome (app-mfdemo); `AuthProvider onNavigate` plus the two page routes (plus optionally `OidcSetupGate`) is what a chrome-less app such as MFManager needs, so MFManager does not need `ClientLayout`. `AuthProvider` skips `onNavigate` when the browser is already on `err.path`.

Removed: the dangling `./styles.css` package export (it never resolved). Consumers need core-gui's `styles.css`/`tokens.css` plus a Tailwind v4 `@source` over users-gui's `dist/`.

Route expectations for an embedding app (details in the embedding note): the app chooses its login route (MFManager keeps `/login`); the backend hard-codes `/reset-password?token=` for reset emails; `AUTH_FRONTEND_RETURN_URL` (deploy env, `<gui>/auth/oidc/return`) must be served by `OidcCallbackPage` inside an `AuthProvider` (token and `return` arrive in the URL fragment); `/forgot-password` and email-code are app choices; `/verify-email` and `/oidc-config` are backend-fixed and have standard page components (above). Next.js: import only from `'use client'` modules; wrap `useSearchParams` users in `<Suspense>`; single `react` instance across users-gui, core-gui and the app; no router adapter needed.

Suggested wave-2 configuration (standard paths; `/login` stays an allowed, stated deviation if MFManager keeps its existing URL): `configureUsersApi({ baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8090', tokenStorageKey: 'mfmanager_session_token', unauthenticatedRedirectUrl: USERS_GUI_ROUTES.login, unauthenticatedReturnParam: 'return' })` and `<AuthProvider loginPath={USERS_GUI_ROUTES.login} onNavigate={router.replace}>`, plus routes for `USERS_GUI_ROUTES.verifyEmail` and `.oidcConfig` mounting the standard pages (no static notice pages). Setting `tokenStorageKey` to MFManager's existing key avoids both session migration and any change to MFManager's own `api-client.ts`; the label-cache invariant (G4) stays the app's concern.

### Phase 1: `gui-seams` (7 tasks)

- `001-runtime-config-base-url-and-token-key` (sonnet-med): `config.ts`, `configureUsersApi`, lazy base URL and token key everywhere, exports, tests. Must run first.
- `002-unauthenticated-handler` (sonnet-med): 401 handler options, return path, same-path guard, `AuthProvider loginPath`, tests. After 001.
- `003-auth-component-props` (sonnet-med): `allowRegistration`, `onForgotPassword`, exported `isSafeReturnPath`, tests, stories. After 001.
- `004-fix-styles-export` (sonnet-low): remove the `./styles.css` export. Independent.
- `005-verify-email-page-and-routes` (sonnet-high): `VerifyEmailPage`, `api.auth.verifyEmail`, `purpose` on the email-code requests, `USERS_GUI_ROUTES`, tests, story. After 001.
- `006-oidc-config-page-migration` (opus-med): move `OidcConfigPage` and its modals from app-mfdemo (behavior-preserving), `onComplete`/`redirectDelayMs`, the `AuthProvider` same-path guard, `OidcSetupGate` extraction from `ClientLayout`, tests. After 001 and 005 (uses `routes.ts`).
- `007-login-return-path-standard` (sonnet-med): library-side read/validate of the configured return param; `onAuthenticated`/`onSuccess` receive it. After 002 and 003.

Parallel-eligible after 001: 002, 003, 004, 005 (004 may also run with 001). Then 006 (after 005) and 007 (after 002 and 003). 005-007 each add exports to `gui/src/index.ts`; the merges are trivial append-only conflicts.

### Phase 2: `consumer-docs-and-handoff` (2 tasks; after phase 1)

- `001-consumer-integration-guide` (sonnet-med): `gui/README.md` plus a root README pointer; covers the new standard pages, `USERS_GUI_ROUTES`, and the backend path contracts.
- `002-verify-consumption-and-handoff` (sonnet-med): build, export checks, no-router-import check, no-`window` `react-dom/server` render (including `VerifyEmailPage`, `OidcConfigPage`, `OidcSetupGate`), consumer and backward-compat type-checks, README snippet type-check, finalize the hand-off note. After 001.

### Phase 3: `doc-updates` (1 task; after phase 2)

- `001-update-architecture-docs` (sonnet-high, architect-frontend): `docs/architecture.md`, `docs/mod-users-spec.md`, `docs/project-structure.md`, `AGENTS.md`, covering the new pages too. Registered because the plan modifies a public API/component boundary.

### Hand-off summary for wave 2

Wave 2 needs: the merge SHA of this plan on mod-users `main` (filled by the manager at finalization), the aggregate checkout on `main` at or after it, `make pins.update REPOS="mod-users"` (plus a compatible mod-core pin) in app-mfmanager, and the app-side build wiring (second workspace member `../mod-users/gui`, named Docker context after core-gui, three-package single-React check, `@source` of users-gui's `dist/`). Wave 2 additionally mounts `/verify-email` and `/oidc-config` with the standard pages (no static notices). Wave 5 (app-mfdemo, a later plan) deletes `src/app/oidc-config/` and mounts `OidcConfigPage` with `onComplete={() => window.location.assign(USERS_GUI_ROUTES.login)}`; the migrated page is behavior-preserving so that is a delete-and-mount. Other than that, app-mftodo and app-mfdemo need nothing now; app-mftodo may later retire `configure-users-gui.ts` and its `/auth/login` alias (its followup ZyTU). Full list: [`notes/consumption-and-handoff.md`](./notes/consumption-and-handoff.md).

### Decisions made and open questions

Decisions the plan made (flagged so the manager/user can override before execution):

1. Module-level `configureUsersApi` (lazy), not a provider prop: the OIDC helpers are plain functions and cannot read context.
2. `""` in `NEXT_PUBLIC_API_BASE_URL` or `window.__USERS_API_URL__` now means same-origin (the only behavior change).
3. Token key is configurable, not just readable, so MFManager's existing sessions survive adoption.
4. A custom `onUnauthenticated` always runs after the token is cleared (mod-core's custom handler replaces the default entirely, including token removal).
5. `./styles.css` export removed rather than built.
6. The mount-time `GET /v1/self` 401 keeps its hard-redirect behavior (default-preserving); the same-path guard makes it harmless on the login page.
7. `/verify-email` and `/oidc-config` become standard users-gui pages (user decision): `VerifyEmailPage` is a code-entry flow because the backend only emails a code; `OidcConfigPage` is extracted behavior-preserving from app-mfdemo with `window.location.assign('/auth/login')` replaced by an `onComplete` callback (default delay 2000 ms) and a timer cleanup on unmount.
8. Standard paths are exported as `USERS_GUI_ROUTES`; wave 2 should use them (see the standardization review).
9. Login return handling is standardized but opt-in (task 007), keyed to the existing `unauthenticatedReturnParam` setting so default behavior is untouched.

### Standardization review ("standard unless there is a design reason")

Each place the plan keeps or kept something app-owned or non-standard, and the outcome:

| Item | Outcome |
|---|---|
| `/verify-email`, `/oidc-config` screens (were app-local or missing) | Standardized: tasks 005, 006. |
| Standard path set (each app picked its own login/forgot/etc. paths) | Standardized as `USERS_GUI_ROUTES`; apps use them unless they state a reason. |
| Local-login `?return=` read/validate/forward (was app-owned) | Standardized, opt-in: task 007. |
| OIDC readiness gate only available inside `ClientLayout`'s admin chrome | Standardized: `OidcSetupGate` extracted (task 006). |
| `AuthProvider` re-navigating to the path it is already on (breaks `/oidc-config`) | Fixed: same-path guard (task 006). |
| MFManager login route `/login` instead of `/auth/login` | Design reason: MFManager's existing URL; allowed through `unauthenticatedRedirectUrl`/`loginPath`; recommended default for wave 2 is still the standard `/auth/login` unless it states a reason. |
| MFManager `tokenStorageKey: 'mfmanager_session_token'` instead of `auth_token` | Design reason: live sessions and the app's own `api-client.ts`/label-cache hook read that key; the cost of standardizing is a one-time sign-out. Wave 2 decision (recommended: keep the key, see open questions). |
| `onUnauthenticated` custom handler and `unauthenticatedReturnParam` | Design reason: a router app needs soft navigation; library default stays the standard hard redirect. |
| Registration hiding is UI-only | Design reason: no backend switch exists; filed as a followup context, not a library concern. |
| No `"use client"` banner in the bundle | Design reason: non-component exports must stay callable from server modules; Vite warns on directives (seam-design note). |
| `/reset-password` path is backend-hard-coded (apps like app-mfdemo serve `/auth/reset`) | Standard path is the backend's `/reset-password` (in `USERS_GUI_ROUTES`); making it configurable is followup `sUuz`; app-mfdemo's mismatch is `TbfT` (wave 5). |
| `ClientLayout`/`SidebarNav` keep the `/auth/login` literal | Design reason: it equals the standard default, and the chrome is admin UI used only by apps that follow the standard path. |
| `/step-up` has no page | Not standardized now: no UI calls the gated endpoints yet; followup filed (do not implement here). |
| Existing `EmailCodePage` login-by-code flow treats a wrong code as a 401 redirect | Out of scope; followup filed. The new page avoids it. |

Open questions (non-blocking; defaults above apply unless changed):

- Should the bundle gain a `"use client"` banner (tsup) so server components can import it? Not adopted (breaks calling `configureUsersApi`/`getStoredToken` from server modules, and Vite consumers warn); wave 2's MFManager shell is already a client component.
- Should hiding registration also have a backend switch? Not planned; the API register endpoint is ungated and a first-user hook grants admin (followup context in the MFManager investigation, `5KxC`).
- Whether wave 2 keeps `mfmanager_session_token` (recommended, zero migration) or adopts `auth_token` (one-time sign-out) is an app-mfmanager decision.
- Should the OIDC add/edit modals also be exported (`OidcProviderAddModal`/`OidcProviderEditModal`)? Default: no, only `OidcConfigPage`; smaller public surface, nothing in the apps needs them.
- Should `VerifyEmailPage` auto-send a code on mount? Default: no (registration already emailed one; a resend button with a 30 s cooldown covers the rest).
- Should wave 2 wrap MFManager in `OidcSetupGate`? Default: yes unless its deployments are guaranteed OIDC-confirmed or opted out; app-mfmanager's plan decides.
- Should `UserAccountSelf` expose `email_verified_at` so `VerifyEmailPage` can skip itself for verified users? Backend change, not in this plan; followup filed.

Followups filed by the revision pass: the `/step-up` page (not implemented here), the `EmailCodePage` 401-redirect behavior, and exposing `email_verified_at` on `/v1/self` (ids in the plan's findings store; the earlier project-level `u353` covers the missing step-up navigation wiring). Followups filed by the original planning pass: `sUuz` (mod-users: configurable password-reset email path and `/oidc-config` banner path), `TbfT` (app-mfdemo: `/auth/reset` vs emailed `/reset-password`). Existing mod-users followup `HPJi` is closed by this plan's merge (manager action).

## Assumptions

- mod-core's wave-1 `unauthenticatedRedirectUrl` keeps its name and validation (read from its plan worktree, not yet merged); a rename there would require only a doc change here.
- `gui` tests run with `bun test` under happy-dom, and `mod-core/gui` can be built before `gui/` (AGENTS.md).
