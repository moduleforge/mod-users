# Plan Summary: users-gui-integration-seams

## What was planned and why

Wave 1 (parallel with mod-core's `shared-home-switcher`) of wave plan `home-app-switcher`. Makes `@moduleforge/users-gui` (`mod-users/gui`) embeddable by an app that owns its own router, base URL, and session key, so wave 2's `adopt-users-gui-auth` (app-mfmanager, Next.js 15 app router) can replace MFManager's hand-rolled login with the standard components. Per the user's principle "make everything standard unless there is a design reason not to", it also moves the two screens the backend itself sends users to, `/verify-email` and `/oidc-config`, into users-gui as standard pages, so every app mounts the same components (wave 5 then deletes app-mfdemo's local oidc-config page). The user does not care if MFManager's login page look changes and prefers standard users-gui components.

In scope (all under `mod-users/gui`, plus plan docs): a runtime configuration API for the API base URL (G1), the 401/unauthenticated handling and logout target (G2), and the token storage key (G3); `AuthPage` registration hiding, a forgot-password entry point, and an exported `isSafeReturnPath`; removal of the dangling `./styles.css` export; tests; a consumer integration guide with route expectations and Next.js usage; build/consumption verification including a no-`window` server render; and a written hand-off; plus, from the same principle: a standard `VerifyEmailPage`, a migrated `OidcConfigPage` (with the extracted `OidcSetupGate`), an exported `USERS_GUI_ROUTES` path set, and opt-in standard login return-path handling. Out of scope: any edit outside `mod-users` (app-mftodo/app-mfdemo/app-mfmanager/mod-core change nothing here); backend changes (a configurable reset-link path and a registration on/off switch are filed as followups); `SidebarNav` changes and any `ClientLayout` behavior change (its OIDC gate is only extracted into `OidcSetupGate`, props and DOM unchanged); a `/step-up` page (followup filed); delegating `request()` to core-gui; a version bump or publish (users-gui is never published).

Backward compatibility is a hard constraint: with no new configuration call, no new prop, behavior for app-mftodo, app-mfdemo and other consumers is identical. The intentional semantic fixes are `NEXT_PUBLIC_API_BASE_URL=""` now meaning same-origin instead of falling through to `http://localhost:8080`, and (task 008, finding `ljyL`) a wrong password, wrong email code, or bad reset token now showing an inline error instead of redirecting to login.

### Clarified request and verification summary

Source verification of each seam (file and line references, rejected alternatives, decisions) is in [`notes/seam-design.md`](./notes/seam-design.md). Route and Next.js expectations are in [`notes/embedding-and-routes.md`](./notes/embedding-and-routes.md). Consumption and hand-off mechanics are in [`notes/consumption-and-handoff.md`](./notes/consumption-and-handoff.md). Findings that matter:

- G1 is wider than reported: besides `api`'s singleton, `API_BASE_URL` is used by `LoginForm` (OIDC start), `fetchProviders`, `oidc-config.ts` and `oidc-provider.ts`; all become lazy.
- G2: the 401 redirect also fires from `AuthProvider`'s mount-time `GET /v1/self`; `logout()` has its own `/auth/login` literal.
- The components are already router-agnostic (no router import in `src`); no adapter or `navigate` prop is needed for Next. What Next needs is documentation, a client-only import rule (the bundle has no `"use client"`), and verification.
- `AuthPage`/`LoginForm` have no way to reach `ForgotPasswordPage`; an optional `onForgotPassword` is added.
- The backend already sends users to `/verify-email` (action-required `users.email_unverified`; verification is a 6-digit emailed code, `purpose: "verify_email"`, verify returns `204`) and `/oidc-config` (action-required `users.oidc_not_confirmed`, setup-token banner URL), and `AuthProvider` already navigates there, but no frontend renders either except app-mfdemo's app-local oidc-config page. Both become standard users-gui pages.
- A wrong email code is a `401`, which `request()` turns into a redirect to login unless `skipAuthRedirect` is passed: the new verify-email calls pass it (the pre-existing `EmailCodePage`, `LoginForm`, and `ResetPasswordPage` have the same bug; fixed in task 008, finding `ljyL`).

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

### Phase 1: `gui-seams` (8 tasks)

- `001-runtime-config-base-url-and-token-key` (sonnet-med): `config.ts`, `configureUsersApi`, lazy base URL and token key everywhere, exports, tests. Must run first.
- `002-unauthenticated-handler` (sonnet-med): 401 handler options, return path, same-path guard, `AuthProvider loginPath`, tests. After 001.
- `003-auth-component-props` (sonnet-med): `allowRegistration`, `onForgotPassword`, exported `isSafeReturnPath`, tests, stories. After 001.
- `004-fix-styles-export` (sonnet-low): remove the `./styles.css` export. Independent.
- `005-verify-email-page-and-routes` (sonnet-high): `VerifyEmailPage`, `api.auth.verifyEmail`, `purpose` on the email-code requests, `USERS_GUI_ROUTES`, tests, story. After 001.
- `006-oidc-config-page-migration` (opus-med): move `OidcConfigPage` and its modals from app-mfdemo (behavior-preserving), `onComplete`/`redirectDelayMs`, the `AuthProvider` same-path guard, `OidcSetupGate` extraction from `ClientLayout`, tests. After 001 and 005 (uses `routes.ts`).
- `007-login-return-path-standard` (sonnet-med): library-side read/validate of the configured return param; `onAuthenticated`/`onSuccess` receive it. After 002 and 003.

- `008-credential-401-inline-errors` (sonnet-med): folds finding `ljyL`. `skipAuthRedirect` on the login, email-code verify, and reset-confirm calls (the three backend endpoints that return 401 for credential failure, verified in the handlers) with inline errors, plus tests that wrong credentials neither clear the token nor redirect while an expired-session 401 still does. After 002 and 005; touches the same components as 007, so run after 007.

Parallel-eligible after 001: 002, 003, 004, 005 (004 may also run with 001). Then 006 (after 005) and 007 (after 002 and 003); 008 last (after 002, 005, and 007, to avoid conflicts in `login-form.tsx`/`auth-page.tsx`). 005-007 each add exports to `gui/src/index.ts`; the merges are trivial append-only conflicts.

### Phase 2: `consumer-docs-and-handoff` (2 tasks; after phase 1)

- `001-consumer-integration-guide` (sonnet-med): `gui/README.md` plus a root README pointer; covers the new standard pages, `USERS_GUI_ROUTES`, and the backend path contracts.
- `002-verify-consumption-and-handoff` (sonnet-med): build, export checks, no-router-import check, no-`window` `react-dom/server` render (including `VerifyEmailPage`, `OidcConfigPage`, `OidcSetupGate`), consumer and backward-compat type-checks, README snippet type-check, finalize the hand-off note. After 001.

### Phase 3: `doc-updates` (1 task; after phase 2)

- `001-update-architecture-docs` (sonnet-high, architect-frontend): `docs/architecture.md`, `docs/mod-users-spec.md`, `docs/project-structure.md`, `AGENTS.md`, covering the new pages too. Registered because the plan modifies a public API/component boundary.

### Hand-off summary for wave 2

Wave 2 needs: the merge SHA of this plan on mod-users `main` (`bd469682826160a0084c4a1e8207ce84364c3202`, merged 2026-10-04), the aggregate checkout on `main` at or after it, `make pins.update REPOS="mod-users"` (plus a compatible mod-core pin) in app-mfmanager, and the app-side build wiring (second workspace member `../mod-users/gui`, named Docker context after core-gui, three-package single-React check, `@source` of users-gui's `dist/`). Wave 2 additionally mounts `/verify-email` and `/oidc-config` with the standard pages (no static notices). Wave 5 (app-mfdemo, a later plan) deletes `src/app/oidc-config/` and mounts `OidcConfigPage` with `onComplete={() => window.location.assign(USERS_GUI_ROUTES.login)}`; the migrated page is behavior-preserving so that is a delete-and-mount. Other than that, app-mftodo and app-mfdemo need nothing now; app-mftodo may later retire `configure-users-gui.ts` and its `/auth/login` alias (its followup ZyTU). Full list: [`notes/consumption-and-handoff.md`](./notes/consumption-and-handoff.md).

### Decisions made and open questions

Decisions the plan made (flagged so the manager/user can override before execution):

1. Module-level `configureUsersApi` (lazy), not a provider prop: the OIDC helpers are plain functions and cannot read context.
2. `""` in `NEXT_PUBLIC_API_BASE_URL` or `window.__USERS_API_URL__` now means same-origin (a behavior change; see also task 008).
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
| Existing `EmailCodePage`, `LoginForm` and `ResetPasswordPage` treat a wrong code, wrong password, or bad reset token (401) as an expired session and redirect | Fixed: task 008 (finding `ljyL`), `skipAuthRedirect` plus inline errors. |

Open questions (non-blocking; defaults above apply unless changed):

- Should the bundle gain a `"use client"` banner (tsup) so server components can import it? Not adopted (breaks calling `configureUsersApi`/`getStoredToken` from server modules, and Vite consumers warn); wave 2's MFManager shell is already a client component.
- Should hiding registration also have a backend switch? Not planned; the API register endpoint is ungated and a first-user hook grants admin (followup context in the MFManager investigation, `5KxC`).
- Whether wave 2 keeps `mfmanager_session_token` (recommended, zero migration) or adopts `auth_token` (one-time sign-out) is an app-mfmanager decision.
- Should the OIDC add/edit modals also be exported (`OidcProviderAddModal`/`OidcProviderEditModal`)? Default: no, only `OidcConfigPage`; smaller public surface, nothing in the apps needs them.
- Should `VerifyEmailPage` auto-send a code on mount? Default: no (registration already emailed one; a resend button with a 30 s cooldown covers the rest).
- Should wave 2 wrap MFManager in `OidcSetupGate`? Default: yes unless its deployments are guaranteed OIDC-confirmed or opted out; app-mfmanager's plan decides.
- Should `UserAccountSelf` expose `email_verified_at` so `VerifyEmailPage` can skip itself for verified users? Backend change, not in this plan; followup filed.

Followups filed by the revision pass: the `/step-up` page (not implemented here), the `EmailCodePage` 401-redirect behavior (now folded into task 008 as `ljyL`), and exposing `email_verified_at` on `/v1/self` (ids in the plan's findings store; the earlier project-level `u353` covers the missing step-up navigation wiring). Followups filed by the original planning pass: `sUuz` (mod-users: configurable password-reset email path and `/oidc-config` banner path), `TbfT` (app-mfdemo: `/auth/reset` vs emailed `/reset-password`). Existing mod-users followup `HPJi` is closed by this plan's merge (manager action).

## What shipped

### Phase 01 — users-gui Integration Seams

1. **Add configureUsersApi With Runtime Base URL and Token Key** (`001-runtime-config-base-url-and-token-key.md`, tier `sonnet-med`) — Added gui/src/lib/config.ts with a lazy runtime config covering base URL and token key. All API_BASE_URL and 'auth_token' consumers now go through it. The api singleton and returned client baseUrl resolve lazily. Deprecated API_BASE_URL export kept; new APIs exported from index.ts. Tests cover precedence, lazy evaluation, validation and the custom token key across auth flows.
   Commit `ac29af9`, merged at `6727e8e24a6dbd3602f8f36b3031e113e362f354`.

2. **Configurable Unauthenticated Handler With Return Path and Logout Target** (`002-unauthenticated-handler.md`, tier `sonnet-med`) — Added unauthenticatedRedirectUrl, unauthenticatedReturnParam and onUnauthenticated to configureUsersApi with validate-before-apply and reset support. The 401 branch of request() now calls a shared handleUnauthenticated() in config.ts, and default behavior is unchanged. AuthProvider gained a validated loginPath, and UnauthenticatedContext is exported from index.ts. Tests cover the default, redirect/return-param/guard, custom handler, validation, and provider cases.
   Commit `5b320af`, merged at `97001fca4464df4bad3556f73c0e8735ee05b59b`.

3. **AuthPage Registration Toggle, Forgot-Password Link, and Exported isSafeReturnPath** (`003-auth-component-props.md`, tier `sonnet-med`) — Moved isSafeReturnPath verbatim into lib/return-path.ts and exported it from index.ts. Added allowRegistration and onForgotPassword props to AuthPage and LoginForm, all default-preserving. Documented that returnPath feeds only the OIDC start URL. Added tests and stories; typecheck and the full suite pass.
   Commit `e2c2355`, merged at `ddf03dd3142060166518c09de1d1fff824431bdd`.

4. **Remove the Dangling ./styles.css Package Export** (`004-fix-styles-export.md`, tier `sonnet-low`) — Removed the dangling ./styles.css export from gui/package.json; no source imports it, and the build is unchanged.
   Commit `41a8f8b`, merged at `e5c6542d7c531d0ec4a28420f5cd91d8e8c2be08`.

5. **Standard VerifyEmailPage and Exported USERS_GUI_ROUTES** (`005-verify-email-page-and-routes.md`, tier `sonnet-high`) — Added api.auth.verifyEmail and the optional purpose and options arguments on the client. Added the USERS_GUI_ROUTES constant and a router-agnostic VerifyEmailPage with an email fallback, resend cooldown, 401 mapping, user refresh and a success state. Tests and stories were added; no surprises.
   Commit `5b235e9`, merged at `48e55f42d7e184a32fee62946a093553f83e38eb`.

6. **Migrate OidcConfigPage and OidcSetupGate Into users-gui** (`006-oidc-config-page-migration.md`, tier `opus-med`) — Moved app-mfdemo's /oidc-config page and its add/edit provider modals into users-gui. The copies differ from the originals only in imports, names, and the new props. The page is exported as OidcConfigPage; the hard-coded redirect to /auth/login is replaced by an onComplete prop that fires after redirectDelayMs (default 2000) and is cancelled if the page unmounts. Moved the OIDC readiness check out of ClientLayout into an exported OidcSetupGate with a ready/setup render-prop; ClientLayout now uses it and renders the same DOM. AuthProvider no longer navigates to an action-required path the browser is already on. Added 33 tests and a mocked-fetch Ladle story.
   Commit `d7a0aa4`, merged at `3173dc50179a8016a0cee4dce9df0dc595c09c05`.

7. **Standard Login Return-Path Handling in AuthPage and LoginForm** (`007-login-return-path-standard.md`, tier `sonnet-med`) — Added readReturnPath (opt-in via unauthenticatedReturnParam) and wired it into LoginForm and AuthPage, which now use returnPath ?? readReturnPath() for the OIDC start URL. The onSuccess and onAuthenticated callbacks now receive string | null. Tests cover the unconfigured case, the return and next param names, unsafe values, and prop precedence, for both components.
   Commit `38e2afc`, merged at `a1e9928e76bc8199acb4b2a448cbf85770f20c56`.

8. **Credential-Failure 401s Show Inline Errors Instead of Redirecting** (`008-credential-401-inline-errors.md`, tier `sonnet-med`) — Credential-failure 401s (login, email-code verify, reset-password confirm) now opt out of the redirect inside the api client and show fixed inline errors at the call sites. The request() 401 branch is untouched. Added credential-401.test.tsx covering the three components, the requestEmailCode non-401 path and two regression guards.
   Commit `5ae9597`, merged at `648c23984c07be563053da5e096a9f20e68382f4`.

### Phase 02 — Consumer Guide, Consumption Verification, and Hand-off

1. **Write the users-gui Consumer Integration Guide** (`001-consumer-integration-guide.md`, tier `sonnet-med`) — Wrote gui/README.md (consumer integration guide) from the current code: configureUsersApi field table, Next.js and React Router wiring, unauthenticated handling, AuthPage/LoginForm return-path handling, VerifyEmailPage and USERS_GUI_ROUTES, OidcSetupGate/OidcConfigPage, plus the six review trust notes and known limitations. Added a pointer sentence in the root README. Snippets are not yet type-checked against a built dist (task 002's paste-check).
   Commit `68976a7`, merged at `7a36bec3e263219637b7fa696fa20742f8bc45f3`.

2. **Verify Built Package Consumption and Finalize Wave-2 Hand-off** (`002-verify-consumption-and-handoff.md`, tier `sonnet-med`) — Built the package and verified exports (33 names), absence of router coupling and no use client, server-render safety, wave-2 and back-compat type-checks, and all README snippets with no defects found. Replaced the hand-off note's verification placeholder with the verified summary and added current-interface trust notes. No interface deviation. The merge SHA is left for the manager at finalization.
   Commit `b6dd3a8`, merged at `659290f40135b8058c71e8133a7aa995d765f774`.

### Phase 03 — Documentation Updates

1. **Update Architecture Docs** (`001-update-architecture-docs.md`, tier `sonnet-high`) — Updated four docs to reflect the users-gui seams (runtime config and 401 handling, standard screens and backend-to-path contract, router-agnostic spec wording, new file layout). All detail links to gui/README.md instead of duplicating it.
   Commit `ec02502`, merged at `ec5a4b96f2834073dac621fbaf78c0ef5c38ca19`.

### Phase 04 — Remediation Round 1

1. **Unify Return-Path Validation, Guard onUnauthenticated, and Document Config Trust** (`001-harden-unauthenticated-handler-config.md`, tier `sonnet-med`) — Unified the 401 return writer and reader on one isSafeReturnPath predicate defined in config.ts, with the unsafe-page fallback to /. A throwing onUnauthenticated is now logged and no longer masks the 401. JSDoc documents the trust assumptions of the base-URL fallbacks and the cross-origin redirect. Tests for both behaviours are added.
   Commit `3b2ee27`, merged at `deb0dccf5bbb945e9f85a65e30c477579fc86a6e`.

2. **Correct LoginForm onSuccess Return-Path Validation Doc** (`002-fix-loginform-onsuccess-doc.md`, tier `sonnet-low`) — Corrected the LoginForm onSuccess JSDoc (comments only). Typecheck and tests pass.
   Commit `4a54458`, merged at `632a3efc40c9ac125c353a1e21ae1ef302c37347`.

### Phase 05 — Remediation Round 2

1. **Correct AuthPage onAuthenticated Return-Path Validation Doc** (`001-fix-authpage-onauthenticated-doc.md`, tier `haiku-med`) — Updated the AuthPage onAuthenticated JSDoc to state that only the query-param fallback (readReturnPath) is validated by isSafeReturnPath, while an explicit returnPath prop is passed through as supplied so the app owns its validation; wording matches LoginForm's corrected onSuccess doc. Comments only. Manager re-ran bun test in the task worktree: 185 pass, 0 fail (the agent's reported 78 failures were not reproducible).
   Commit `5753f86`, merged at `613c1adadfd89d593d28a459806d9a471bc0e708`.

### Phase 06 — Remediation Round 3

1. **Correct AuthProvider 401 Navigation Wording In GUI README** (`001-fix-readme-authprovider-401-wording.md`, tier `haiku-med`) — Reworded two gui/README.md statements about AuthProvider 401 navigation (the loginPath bullet and the components-table row) to match the code: navigation via onNavigate on logout() and when refreshUser() receives a 401; the mount-time /v1/self check on a 401 only clears the stored token. No code was changed.
   Commit `df9a503`, merged at `f3f9cd03ea689797396753e2cad9df1cc144479e`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Findings

- **`Y7W0`** — **Standard /step-up page not implemented** — promoted — ref: `Y7W0` — 2026-10-04

- **`QCRD`** — **Expose email_verified_at on GET /v1/self** — promoted — ref: `QCRD` — 2026-10-04

- **`aQ2V`** — **AuthProvider path guard and render-time log** — promoted — ref: `aQ2V` — 2026-10-04

- **`0e3P`** — **OIDC config UI hardening nits** — promoted — ref: `0e3P` — 2026-10-04

- **`ljyL`** — **EmailCodePage wrong code hits 401 redirect** — fixed — ref: `phase-01-gui-seams/008-credential-401-inline-errors.md` — 2026-10-04

- **`uOkV`** — **LoginForm doc drift; ClientLayout loginPath** — fixed — ref: `phase-04-remediation-01/002-fix-loginform-onsuccess-doc.md` — 2026-10-04

- **`V6al`** — **Duplicate site-path safety check** — fixed — ref: `phase-04-remediation-01/001-harden-unauthenticated-handler-config.md` — 2026-10-04

- **`dYMj`** — **Throwing onUnauthenticated masks the 401** — fixed — ref: `phase-04-remediation-01/001-harden-unauthenticated-handler-config.md` — 2026-10-04

- **`X1dW`** — **Base URL fallbacks and redirect hardening** — fixed — ref: `phase-04-remediation-01/001-harden-unauthenticated-handler-config.md` — 2026-10-04

- **`ZNOd`** — **ENVIRONMENT: mod-core/gui/node_modules contai** — promoted — ref: `ZNOd` — 2026-10-04

- **`HNPY`** — **gui/src/components/auth-page.tsx onAuthentica** — fixed — ref: `phase-05-remediation-02/001-fix-authpage-onauthenticated-doc.md` — 2026-10-04

- **`aaEB`** — **README overstates AuthProvider 401 navigation** — fixed — ref: `phase-06-remediation-03/001-fix-readme-authprovider-401-wording.md` — 2026-10-04

## Remediation

- Rounds used: 3 (resolved max_rounds: 3).
- Remediation tasks added: 4 (resolved max_added_tasks: 10).
- Remediation phases:
  - `remediation-01` — 2 task(s)
  - `remediation-02` — 1 task(s)
  - `remediation-03` — 1 task(s)
- Security review required: yes (at least one remediation phase carries `security_review: required`).

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — users-gui Integration Seams

- [x] [001-runtime-config-base-url-and-token-key.md](./phase-01-gui-seams/001-runtime-config-base-url-and-token-key.md) — tier `sonnet-med` · branch `plan/users-gui-integration-seams-01-001` · commit `ac29af9` · merge `6727e8e24a6dbd3602f8f36b3031e113e362f354`
- [x] [002-unauthenticated-handler.md](./phase-01-gui-seams/002-unauthenticated-handler.md) — tier `sonnet-med` · branch `plan/users-gui-integration-seams-01-002` · commit `5b320af` · merge `97001fca4464df4bad3556f73c0e8735ee05b59b`
- [x] [003-auth-component-props.md](./phase-01-gui-seams/003-auth-component-props.md) — tier `sonnet-med` · branch `plan/users-gui-integration-seams-01-003` · commit `e2c2355` · merge `ddf03dd3142060166518c09de1d1fff824431bdd`
- [x] [004-fix-styles-export.md](./phase-01-gui-seams/004-fix-styles-export.md) — tier `sonnet-low` · branch `plan/users-gui-integration-seams-01-004` · commit `41a8f8b` · merge `e5c6542d7c531d0ec4a28420f5cd91d8e8c2be08`
- [x] [005-verify-email-page-and-routes.md](./phase-01-gui-seams/005-verify-email-page-and-routes.md) — tier `sonnet-high` · branch `plan/users-gui-integration-seams-01-005` · commit `5b235e9` · merge `48e55f42d7e184a32fee62946a093553f83e38eb`
- [x] [006-oidc-config-page-migration.md](./phase-01-gui-seams/006-oidc-config-page-migration.md) — tier `opus-med` · branch `plan/users-gui-integration-seams-01-006` · commit `d7a0aa4` · merge `3173dc50179a8016a0cee4dce9df0dc595c09c05`
- [x] [007-login-return-path-standard.md](./phase-01-gui-seams/007-login-return-path-standard.md) — tier `sonnet-med` · branch `plan/users-gui-integration-seams-01-007` · commit `38e2afc` · merge `a1e9928e76bc8199acb4b2a448cbf85770f20c56`
- [x] [008-credential-401-inline-errors.md](./phase-01-gui-seams/008-credential-401-inline-errors.md) — tier `sonnet-med` · branch `plan/users-gui-integration-seams-01-008` · commit `5ae9597` · merge `648c23984c07be563053da5e096a9f20e68382f4`

### Phase 02 — Consumer Guide, Consumption Verification, and Hand-off

- [x] [001-consumer-integration-guide.md](./phase-02-consumer-docs-and-handoff/001-consumer-integration-guide.md) — tier `sonnet-med` · branch `plan/users-gui-integration-seams-02-001` · commit `68976a7` · merge `7a36bec3e263219637b7fa696fa20742f8bc45f3`
- [x] [002-verify-consumption-and-handoff.md](./phase-02-consumer-docs-and-handoff/002-verify-consumption-and-handoff.md) — tier `sonnet-med` · branch `plan/users-gui-integration-seams-02-002` · commit `b6dd3a8` · merge `659290f40135b8058c71e8133a7aa995d765f774`

### Phase 03 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-03-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `plan/users-gui-integration-seams-03-001` · commit `ec02502` · merge `ec5a4b96f2834073dac621fbaf78c0ef5c38ca19`

### Phase 04 — Remediation Round 1

- [x] [001-harden-unauthenticated-handler-config.md](./phase-04-remediation-01/001-harden-unauthenticated-handler-config.md) — tier `sonnet-med` · branch `plan/users-gui-integration-seams-04-001` · commit `3b2ee27` · merge `deb0dccf5bbb945e9f85a65e30c477579fc86a6e`
- [x] [002-fix-loginform-onsuccess-doc.md](./phase-04-remediation-01/002-fix-loginform-onsuccess-doc.md) — tier `sonnet-low` · branch `plan/users-gui-integration-seams-04-002` · commit `4a54458` · merge `632a3efc40c9ac125c353a1e21ae1ef302c37347`

### Phase 05 — Remediation Round 2

- [x] [001-fix-authpage-onauthenticated-doc.md](./phase-05-remediation-02/001-fix-authpage-onauthenticated-doc.md) — tier `haiku-med` · branch `plan/users-gui-integration-seams-05-001` · commit `5753f86` · merge `613c1adadfd89d593d28a459806d9a471bc0e708`

### Phase 06 — Remediation Round 3

- [x] [001-fix-readme-authprovider-401-wording.md](./phase-06-remediation-03/001-fix-readme-authprovider-401-wording.md) — tier `haiku-med` · branch `plan/users-gui-integration-seams-06-001` · commit `df9a503` · merge `f3f9cd03ea689797396753e2cad9df1cc144479e`


## Appendix: Wave-2 hand-off note (preserved from plan/notes at finalization)

### Consuming the New users-gui and the Wave-2 Hand-off

## Purpose and scope

Records how consumers obtain `@moduleforge/users-gui`, what that means for delivering these seams, and exactly what wave 2 (`adopt-users-gui-auth` in app-mfmanager) needs from this plan. Mirrors mod-core's `shared-home-switcher` note of the same name. Facts below were read from `mod-users/AGENTS.md`, `gui/package.json`, `gui/tsup.config.ts`, `versions.lock.yaml`, `app-mftodo/versions.lock.yaml`, `app-mftodo/gui/src/lib/configure-users-gui.ts`, `app-mftodo/gui/src/App.tsx`, and mod-core's matching note. Phase 2 task 002 verified the built package and filled the verification entry; the merge-SHA slot was filled at finalization.

## How users-gui is versioned and distributed

- `gui/package.json` is `version 0.1.0` and is **never published** (README's `npm install` line is aspirational; consumers use bun workspaces). **No version bump is part of this plan.**
- The deliverable is a **commit on mod-users `main`** (aggregate checkout `/Users/zane/playground/moduleforge/mod-users`) from which `gui/dist` is rebuilt by `bun run build`; `dist` is not committed.
- users-gui imports `@moduleforge/core-gui` at module top level (declared an optional peer but effectively required), so any consumer must also resolve and build `mod-core/gui` first. These seams add **no new core-gui requirement** (no new core-gui symbol is imported) and do not depend on mod-core's wave-1 plan landing; the two plans are independent in both directions. `unauthenticatedRedirectUrl` is deliberately the same name and validation in both libraries.

## Consumers

- **app-mftodo** (workspace member `.gui-siblings/mod-users/gui`, refreshed from the aggregate checkout's working tree on `make gui.deps`; pin `mod-users` in `versions.lock.yaml` for CI). All seams are additive and default-preserving, so it needs **no change** and no pin bump. Optional later cleanup (separate, app-mftodo-owned): drop `gui/src/lib/configure-users-gui.ts` (use `configureUsersApi({ baseUrl: window.location.origin })`) and the `/auth/login` compat route (use `unauthenticatedRedirectUrl: '/login'`); closes its followup ZyTU.
- **app-mfdemo** (Next 15 via yalc): unchanged by this plan; additive API only. In **wave 5** (a later plan, which owns app-mfdemo) it deletes `src/app/oidc-config/` and mounts the standard `OidcConfigPage` (`onComplete` doing a full-page navigation to login) inside its existing `ClientLayout` (`onNavigateToConfig` unchanged); the migration is behavior-preserving, so no other change is expected. It may also pass the new `VerifyEmailPage` at `/verify-email` (it has no such route today). Its `/auth/reset` vs emailed `/reset-password` mismatch is pre-existing (followup filed in that repo).
- **app-mfmanager**: not a consumer yet; wave 2 adds it, mounting `VerifyEmailPage` at `/verify-email` and `OidcConfigPage` at `/oidc-config` (replacing the static notice pages it would otherwise build) and optionally wrapping the app in `OidcSetupGate`.

## Exact hand-off wave 2 needs

1. This plan's branch `plan/users-gui-integration-seams` is merged to `mod-users` `main`; the manager reports the **merge commit SHA**: `bd469682826160a0084c4a1e8207ce84364c3202` (merged into mod-users `main`, 2026-10-04).
2. The aggregate checkout `/Users/zane/playground/moduleforge/mod-users` is on `main` at or after that SHA when app-mfmanager builds its GUI deps (its `.gui-siblings`/workspace materialization copies the aggregate working tree, not a git ref).
3. `cd mod-users/gui && bun run build` succeeds after `mod-core/gui` is built, and `dist/index.d.ts` exports the symbols in the overview's "Interface wave 2 consumes". **Verified (phase-02 task 002, 2026-10-04): PASS**, no deviation from the overview interface. Summary:
   - **Build**: `bun run build` produced `dist/index.mjs`, `dist/index.js`, `dist/index.d.ts` (and `index.d.mts`). `package.json` has no `./styles.css` export (only `.`).
   - **Exports** (all present in `dist/index.d.ts`): `configureUsersApi`, `resetUsersApiConfig`, `getApiBaseUrl`, `getStoredToken`, `clearStoredToken`, `getTokenStorageKey`, `USERS_TOKEN_KEY`, `UsersApiConfig`, `UnauthenticatedContext`, `isSafeReturnPath`, `readReturnPath`, `USERS_GUI_ROUTES`, `AuthPage`, `LoginForm`, `RegisterForm`, `AuthProvider`, `RequireAuth`, `useAuth`, `useOptionalAuth`, `OidcCallbackPage`, `ResetPasswordPage`, `ForgotPasswordPage`, `EmailCodePage`, `ClientLayout`, `SidebarNav`, and the new `VerifyEmailPage`/`VerifyEmailPageProps`, `OidcConfigPage`/`OidcConfigPageProps`, `OidcSetupGate`/`OidcSetupGateProps`. Props visible: `allowRegistration`, `onForgotPassword`, `loginPath`, `onVerified`, `onComplete`, `redirectDelayMs`, and `returnPath: string | null` on `onAuthenticated`/`onSuccess`.
   - **No router coupling**: no `react-router` or `next/` string in `dist/index.mjs` or `dist/index.js`. External specifiers: `react`, `react/jsx-runtime`, `@moduleforge/core-gui`, `radix-ui`, `lucide-react`, `clsx`, `tailwind-merge` (`class-variance-authority` is not imported by the bundle; it stays a declared dependency). `"use client"` does **not** appear in the bundle, so Next consumers must import from their own client modules (the guide says so).
   - **Server-render safety**: a plain `bun run` script (no happy-dom, `typeof window === 'undefined'`) importing the built `dist/index.mjs` with `configureUsersApi({ baseUrl: '' })` rendered, via `react-dom/server` `renderToString`, `AuthPage` (default and `allowRegistration={false}`, the latter without "Create one"), `LoginForm`, `ResetPasswordPage`, `ForgotPasswordPage`, `EmailCodePage`, `OidcCallbackPage`, `RequireAuth`, `VerifyEmailPage` (in and out of `AuthProvider`), `OidcConfigPage` and `OidcSetupGate`: all PASS; `getApiBaseUrl() === ''` and `getStoredToken() === null` server-side.
   - **Consumer type-check**: a wave-2-shaped scratch consumer (module-top `configureUsersApi` with the app-mfmanager settings, `AuthProvider loginPath`/`onNavigate`, login/reset/OIDC-return/verify-email/oidc-config pages, `RequireAuth onUnauthenticated`, `OidcSetupGate`, all paths from `USERS_GUI_ROUTES`) plus every `tsx` snippet in `gui/README.md` (8 blocks, with stubs for `next/navigation` and `react-router-dom`) type-checked clean with `bunx tsc` against `dist/index.d.ts` (a deliberate bad prop was rejected, confirming the check bites). No README snippet needed fixing.
   - **Backward compatibility**: pre-existing suite `cd gui && bun run typecheck && bun test` PASS (185 tests, 0 fail). An app-mftodo/app-mfdemo-shaped scratch (`AuthProvider onNavigate`, `AuthPage onAuthenticated initialError returnPath`, `OidcCallbackPage`, `ClientLayout currentPath onNavigateToConfig onNavigate LinkComponent`, `window.__USERS_API_URL__` with no `configureUsersApi`) type-checks, and under happy-dom `getApiBaseUrl()` returns the `window.location.origin` set beforehand.
   - Scratch files lived under the gitignored `gui/node_modules/.scratch-smoke/` and were deleted afterwards.
4. app-mfmanager's `versions.lock.yaml`: `make pins.update REPOS="mod-users"` to the merge SHA (plus `mod-core` to a SHA that builds users-gui; users-gui's own lock pins an old mod-core `14fa8f5` which is harmless here, but app-mfmanager's single mod-core pin must satisfy both users-gui and core-gui consumers).
5. app-mfmanager build wiring (app-side, not mod-users work), extending its core-gui pattern: add `../mod-users/gui` as a second bun workspace member and a named Docker context after core-gui in the ordered `gui.deps`; extend the single-React check to three packages; install users-gui's runtime deps (`radix-ui`, `class-variance-authority`, `lucide-react`, `tailwind-merge`, `tw-animate-css`) via the workspace; `@source` users-gui `dist/` in its CSS.
6. App-side configuration (see the overview interface): `configureUsersApi({ baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8090', tokenStorageKey: 'mfmanager_session_token' (or accept the default and a one-time sign-out), unauthenticatedRedirectUrl: '/login', unauthenticatedReturnParam: 'return' })`, `AuthProvider loginPath="/login"`.
7. Route mounting in app-mfmanager: `USERS_GUI_ROUTES.verifyEmail` and `.oidcConfig` render the standard pages (props in the overview); `AuthProvider onNavigate` already receives these paths from action-required responses.
8. No npm publish, no `yalc`, no version bump.

## Current interface and trust notes wave 2 must know

The full integration guide is `gui/README.md`; these are the review-derived caveats the app side must respect:

- **Base-URL fallbacks are unvalidated and trusted.** Only the value passed to `configureUsersApi({ baseUrl })` is validated. The `NEXT_PUBLIC_API_BASE_URL` and `window.__USERS_API_URL__` fallbacks are used as-is and the bearer token goes to whatever resolves, so they must come only from deployment configuration. Wave 2 should pass its value to `configureUsersApi` explicitly.
- **Absolute redirect plus return param leaks the page.** An absolute `unauthenticatedRedirectUrl` with `unauthenticatedReturnParam` sends the current `pathname + search` to that origin; wave 2 uses the relative `/login`-style `USERS_GUI_ROUTES.login`, so this does not apply.
- **`isSafeReturnPath` is the one shared predicate** (401 handler writes with it; `readReturnPath` and `OidcCallbackPage` read with it). An **explicit `returnPath` prop** to `LoginForm`/`AuthPage` is passed through unvalidated: the app validates it. `onUnauthenticated`'s `ctx.returnPath` is only guaranteed a safe site path; run `isSafeReturnPath` before using it as a navigation target.
- **A throwing `onUnauthenticated` is caught and logged** (`console.error`); the request still rejects with the 401 `ApiRequestError`.
- **`AuthProvider`'s same-path guard uses exact `window.location.pathname` equality**: under a `basePath` or trailing-slash URLs it does not match and can still navigate to the page it is on (known limitation; wave 2 should not serve under a `basePath`).
- **`ClientLayout` has no `loginPath` prop and `SidebarNav` hard-codes `/auth/login`**; app-mfmanager should use `AuthProvider` and `OidcSetupGate` directly rather than `ClientLayout`.
- `allowRegistration={false}` is UI-only (the API register endpoint stays open); there is no `/step-up` page and `AuthPage` has no email-code link.

## Open points for the manager

- Close mod-users followup `HPJi` (users-gui hard-navigates on 401) when this plan merges; app-mftodo `ZyTU` stays open for its own cleanup (and its mod-core half is covered by mod-core wave 1).
- Known environment gap (AGENTS.md): building `mod-core/gui` through a worktree's `../mod-core` symlink can fail to resolve its devDependencies; build it once from the real checkout before building `gui/` in a task worktree.
