# Embedding users-gui: Routes and Next.js Expectations

## Purpose and scope

States the route and path expectations an embedding app (wave 2: app-mfmanager's Next.js app-router GUI) must satisfy for mod-users' backend links and redirects to land on a rendered users-gui component. Facts verified against `api/` and `gui/src`; the consumer guide task turns this into `gui/README.md`.

## Route table

users-gui owns **no route files** (it is router-agnostic; the app creates the route and mounts the component), but it ships a page component for every backend-fixed path and exports the standard path set as `USERS_GUI_ROUTES` (`login: '/auth/login'`, `oidcReturn: '/auth/oidc/return'`, `forgotPassword: '/forgot-password'`, `resetPassword: '/reset-password'`, `emailCode: '/auth/email-code'`, `verifyEmail: '/verify-email'`, `oidcConfig: '/oidc-config'`). Principle: apps use the standard paths; a deviation needs a stated design reason (an existing deployed URL, for instance) and uses the configuration seams (`unauthenticatedRedirectUrl`, `AuthProvider loginPath`). Rows marked Backend are not negotiable.

| Path | Who fixes it | Component / behavior | Notes |
|---|---|---|---|
| Login route (standard `/auth/login`; MFManager's existing `/login` is an allowed, documented deviation) | App, standard default | `AuthPage` or `LoginForm`; navigate in `onAuthenticated`/`onSuccess` | `/auth/login` is only the *default* 401/logout target; configurable via `unauthenticatedRedirectUrl` and `AuthProvider loginPath`. |
| `/reset-password?token=<hex>` | **Backend, hard-coded** (`api/internal/handlers/auth/reset.go` line ~78: `<GUI_BASE_URL>/reset-password?token=`) | `ResetPasswordPage token={searchParams.get('token') ?? ''} onSuccess/onNavigateToLogin` | The component never reads the URL; the page passes the token. app-mfdemo serves `/auth/reset` instead (mismatch, filed as a followup in app-mfdemo). Followup filed here for a configurable path. |
| `/auth/oidc/return` | Deployment env `AUTH_FRONTEND_RETURN_URL` (`<gui>/auth/oidc/return` in the deploy compose/`.env.example` files) | `OidcCallbackPage onComplete onError` | Token and `return` arrive in the URL **fragment** (`#token=...&return=...`), `?error=` in the query. The page strips the hash. `onError(message)` should navigate to the login route with `?error=<encodeURIComponent(message)>`, which feeds `AuthPage initialError`. Needs `AuthProvider` mounted above it. |
| `/forgot-password` | App | `ForgotPasswordPage onNavigateToLogin` | Reachable from login only if the app passes `onForgotPassword` (new) or renders its own link. |
| `/auth/email-code` | App | `EmailCodePage onSuccess onNavigateToLogin` | No built-in link from `AuthPage`; app renders one if wanted. |
| `/verify-email` | **Backend, hard-coded** (`api/internal/auth/require_verified.go:34-37`: `403` action-required `users.email_unverified`, `action.path: "/verify-email"`; registration emails a 6-digit `verify_email` code, `handlers/auth/register.go:253-262`) | `VerifyEmailPage onVerified onNavigateToLogin [email] [message] [resendCooldownSeconds]` | Code entry only (no emailed link). `AuthProvider` navigates here through `onNavigate(err.path)` on an action-required response, session kept. Works inside or outside `AuthProvider`; `onVerified` is where the app navigates onward. Uses `POST /v1/auth/email-code/{request,verify}` with `purpose: "verify_email"` (verify returns `204`). |
| `/oidc-config` | **Backend, hard-coded** (`require_confirmed.go:38`: `503` action-required `users.oidc_not_confirmed`, `action.path: "/oidc-config"`, extra `{state}`; setup-token stderr banner `api/cmd/server/main.go:251` prints `GUI_BASE_URL + "/oidc-config"`) | `OidcConfigPage onComplete [redirectDelayMs]`, wrapped by `OidcSetupGate` | Dual mode: setup-token (no session; success calls `onComplete` after `redirectDelayMs`, apps do a full-page navigation to login) and admin (session, stays on page). Drives `/v1/oidc-config/*` via the exported helpers. Migrated from app-mfdemo's local page; wave 5 deletes the local copy. |
| `/step-up` | **Backend, hard-coded** (`api/internal/handlers/identities.go:663-668`, `409` `users.step_up_required`) | None (not implemented by this plan) | `AuthProvider` has no step-up navigation either; tracked as a followup. Apps must not link to it yet. |

## Mounting requirements

- `AuthProvider` must wrap every page using `LoginForm`, `AuthPage`, `RegisterForm`, `EmailCodePage`, `OidcCallbackPage`, `RequireAuth` (`useAuth()` throws without it). `ForgotPasswordPage`, `ResetPasswordPage`, `VerifyEmailPage` and `OidcConfigPage` do not require it (the last two use it when present).
- `AuthProvider onNavigate` receives site paths (`'/auth/login'` or the configured `loginPath` on logout, and `ApiActionRequiredError.path`: `/verify-email`, `/oidc-config`, and `/step-up`; the first two have standard pages, the third does not yet). It skips the call when the browser is already on that path. In Next: `onNavigate={(p) => router.replace(p)}`.
- `RequireAuth onUnauthenticated` is where an app with a router builds its own `?return=` redirect (`router.replace('/login?return=' + encodeURIComponent(pathname))`); the 401 handler covers the mid-session expiry case at the fetch layer.

## ClientLayout, OidcSetupGate, and what MFManager needs

- `ClientLayout onNavigateToConfig` **remains** the way an app that uses mod-users' admin chrome (`SidebarNav` + sidebar shell, app-mfdemo) routes to `/oidc-config`; its props are unchanged. Internally it now composes `OidcSetupGate` (task 006), and its path literal is `USERS_GUI_ROUTES.oidcConfig`.
- **MFManager does not need `ClientLayout`**: it has its own shell and the sidebar is mod-users admin UI. What it does need for `/oidc-config` and `/verify-email` is (a) `AuthProvider onNavigate` (already wired for `err.path`, covers both backend action-required responses on any authenticated call, including `GET /v1/self` while OIDC is unconfirmed) and (b) the two page routes. The pre-login case (unconfirmed OIDC makes even the login page's `/v1/auth/providers` call fail with the same `503`) is covered by wrapping the app in `OidcSetupGate` with `onNavigateToConfig={() => router.replace(USERS_GUI_ROUTES.oidcConfig)}`; wave 2 should include it unless MFManager's deployments are guaranteed to have OIDC confirmed or opted out (`NO_OIDC_ACCOUNTS=1`), which is an app-mfmanager decision recorded in its plan.
- `OidcConfigPage` may be mounted inside or outside `AuthProvider` (it uses `useOptionalAuth`); `AuthProvider` skips navigating to a path the browser is already on, so an `AuthProvider` above `/oidc-config` does not loop. Without a session the page uses the setup token; with an admin session it shows admin mode.

## Next.js notes

- Imports: only from `'use client'` modules (no directive in `dist`).
- Single React: users-gui, core-gui and the app must share one `react` instance (users-gui peer `^19`).
- Styling: core-gui's `styles.css`/`tokens.css` plus a Tailwind v4 `@source` for both packages' `dist/`; no users-gui CSS file exists after this plan (the dangling export is removed).
- SSR: the components render without `window` during the server pass; phase 2 verifies with `react-dom/server`.
- Runtime base URL: `configureUsersApi({ baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8090' })` at module top of the app's client shell. Pass the literal `process.env.NEXT_PUBLIC_API_BASE_URL` expression in the *app's* code so Next inlines it; `""` is honored as same-origin.
