# Embedding users-gui: Routes and Next.js Expectations

## Purpose and scope

States the route and path expectations an embedding app (wave 2: app-mfmanager's Next.js app-router GUI) must satisfy for mod-users' backend links and redirects to land on a rendered users-gui component. Facts verified against `api/` and `gui/src`; the consumer guide task turns this into `gui/README.md`.

## Route table

users-gui owns **no routes**; every path below is an app decision except where the backend hard-codes it.

| Path | Who fixes it | Component / behavior | Notes |
|---|---|---|---|
| Login route (MFManager: `/login`) | App | `AuthPage` or `LoginForm`; navigate in `onAuthenticated`/`onSuccess` | `/auth/login` is only the *default* 401/logout target; configurable via `unauthenticatedRedirectUrl` and `AuthProvider loginPath`. |
| `/reset-password?token=<hex>` | **Backend, hard-coded** (`api/internal/handlers/auth/reset.go` line ~78: `<GUI_BASE_URL>/reset-password?token=`) | `ResetPasswordPage token={searchParams.get('token') ?? ''} onSuccess/onNavigateToLogin` | The component never reads the URL; the page passes the token. app-mfdemo serves `/auth/reset` instead (mismatch, filed as a followup in app-mfdemo). Followup filed here for a configurable path. |
| `/auth/oidc/return` | Deployment env `AUTH_FRONTEND_RETURN_URL` (`<gui>/auth/oidc/return` in the deploy compose/`.env.example` files) | `OidcCallbackPage onComplete onError` | Token and `return` arrive in the URL **fragment** (`#token=...&return=...`), `?error=` in the query. The page strips the hash. `onError(message)` should navigate to the login route with `?error=<encodeURIComponent(message)>`, which feeds `AuthPage initialError`. Needs `AuthProvider` mounted above it. |
| `/forgot-password` | App | `ForgotPasswordPage onNavigateToLogin` | Reachable from login only if the app passes `onForgotPassword` (new) or renders its own link. |
| `/auth/email-code` | App | `EmailCodePage onSuccess onNavigateToLogin` | No built-in link from `AuthPage`; app renders one if wanted. |
| `/oidc-config` | Backend (onboarding banner `api/handlers/onboarding_boot.go` line ~54) | No page component exported (data helpers `fetchOIDCStatus`, `postOIDCConfirm`, ... only) | Deployments default to `NO_OIDC_ACCOUNTS=1`; out of scope for MFManager adoption unless it wants the setup UI. |

## Mounting requirements

- `AuthProvider` must wrap every page using `LoginForm`, `AuthPage`, `RegisterForm`, `EmailCodePage`, `OidcCallbackPage`, `RequireAuth` (`useAuth()` throws without it). `ForgotPasswordPage` and `ResetPasswordPage` call the API client directly and do not need it.
- `AuthProvider onNavigate` receives site paths (`'/auth/login'` or the configured `loginPath` on logout, and `ApiActionRequiredError.path` such as `/verify-email`). In Next: `onNavigate={(p) => router.replace(p)}`.
- `RequireAuth onUnauthenticated` is where an app with a router builds its own `?return=` redirect (`router.replace('/login?return=' + encodeURIComponent(pathname))`); the 401 handler covers the mid-session expiry case at the fetch layer.

## Next.js notes

- Imports: only from `'use client'` modules (no directive in `dist`).
- Single React: users-gui, core-gui and the app must share one `react` instance (users-gui peer `^19`).
- Styling: core-gui's `styles.css`/`tokens.css` plus a Tailwind v4 `@source` for both packages' `dist/`; no users-gui CSS file exists after this plan (the dangling export is removed).
- SSR: the components render without `window` during the server pass; phase 2 verifies with `react-dom/server`.
- Runtime base URL: `configureUsersApi({ baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8090' })` at module top of the app's client shell. Pass the literal `process.env.NEXT_PUBLIC_API_BASE_URL` expression in the *app's* code so Next inlines it; `""` is honored as same-origin.
