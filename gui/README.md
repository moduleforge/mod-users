# @moduleforge/users-gui

Consumer integration guide for apps that embed the mod-users React client.

## What this library is

`@moduleforge/users-gui` is a set of router-agnostic client components plus a typed API client for the mod-users backend. It owns **no routes** and ships **no CSS file**: your app creates each route, mounts the component, and injects navigation through props (`onNavigate`, `onSuccess`, `onComplete`, and so on). Components never read a router; the few that read the URL (`LoginForm`, `AuthPage` through `readReturnPath`, `OidcCallbackPage`) read `window.location` directly and only when told to.

Everything below is exported from the package root (`gui/src/index.ts`).

## Consumption

The package is not published to any registry. An app consumes it as a member of a **bun workspace** it owns: a root `package.json` listing this repo's `gui/` (and the sibling `mod-core/gui`) in `workspaces`, with the app's own `package.json` referring to `@moduleforge/users-gui` as `workspace:*`. `@moduleforge/core-gui` is an optional peer dependency (`react` and `react-dom` `^19` are required peers).

Lockfile: `gui/` has no lockfile of its own. The single committed lockfile is the repo-root `bun.lock`, which covers the whole workspace (`gui/` plus the sibling `mod-core/gui`); `gui/yalc.lock` is gitignored and unused. From a clean clone with `mod-core` checked out beside `mod-users`, `bun install --frozen-lockfile` at the repo root succeeds, after which `bun run typecheck` and `bun run build` in `gui/` pass (once `mod-core/gui` is built, as below). Never commit `node_modules`.

Build order: build `mod-core/gui` first (`bun run build` there, not bare `tsup`, so `dist/index.css` is produced), then build `gui/` here. The exact steps are in [AGENTS.md First-time setup](../AGENTS.md#first-time-setup).

Styling uses Tailwind v4 with the core-gui tokens. In your app's global CSS, import core-gui's `styles.css` and `tokens.css` and add an `@source` for both packages' `dist/` directories so Tailwind sees their class names (adjust the relative paths to your workspace layout):

```css
@import 'tailwindcss';
@import '@moduleforge/core-gui/styles.css';
@import '@moduleforge/core-gui/tokens.css';
@source '../../node_modules/@moduleforge/core-gui/dist';
@source '../../node_modules/@moduleforge/users-gui/dist';
```

There is no `@moduleforge/users-gui/styles.css` export; do not import one.

## Runtime configuration

Configure the API client once, before anything renders, with `configureUsersApi`. Values are read lazily on every request, so you may call it at module top of your app shell. An omitted or `undefined` field leaves the current value unchanged. All fields are validated first; on a validation failure a `TypeError` is thrown and nothing changes.

| Field | Type | Default | Validation |
|---|---|---|---|
| `baseUrl` | `string` | see [precedence](#base-url-precedence) | `''` (same-origin), an absolute `http(s)` URL, or a path starting with a single `/`; no control characters or backslashes; trailing slashes stripped |
| `tokenStorageKey` | `string` | `'auth_token'` (`USERS_TOKEN_KEY`) | non-empty string; the `sessionStorage` key of the tab's access token (see [Frontend contract](#frontend-contract)) |
| `forkTimeoutMs` | `number` | `1500` | positive integer no larger than `30000`; how long a tab with no tokens waits for a sibling tab to answer its fork request |
| `unauthenticatedRedirectUrl` | `string` | `'/auth/login'` (`USERS_GUI_ROUTES.login`) | a path starting with exactly one `/` (no `//`, backslash or control characters), or an absolute `http(s)` URL |
| `unauthenticatedReturnParam` | `string \| null` | `null` (append nothing) | `null` or matching `/^[A-Za-z0-9_-]+$/` |
| `onUnauthenticated` | `((ctx: UnauthenticatedContext) => void) \| null` | `null` (default redirect) | function or `null`; `null` restores the default handler |

Related helpers:

- `resetUsersApiConfig()` restores every default. It exists mainly for tests.
- `getApiBaseUrl()` returns the resolved base URL (evaluated on every call).
- `getTokenStorageKey()`, `getStoredToken()` and `clearStoredToken()` read the key in effect, read the tab's stored access token (`null` during SSR), and remove it. `USERS_TOKEN_KEY` is the default key.
- `UsersApiConfig` and `UnauthenticatedContext` are the exported types.

**Token key semantics.** Configure `tokenStorageKey` before the `AuthProvider` mounts, and do not change it mid-session: the tokens already stored under the old key are not migrated. The refresh token, active profile and handoff verifier are stored under `<key>.refresh`, `<key>.profile` and `<key>.handoff_verifier`, and the fork channel name includes the key, so two embedded apps with different keys never share tokens.

### Base URL precedence

`getApiBaseUrl()` resolves, in order:

1. the value set through `configureUsersApi({ baseUrl })`;
2. `process.env.NEXT_PUBLIC_API_BASE_URL`, when it is defined (including `''`);
3. `window.__USERS_API_URL__`, when it is a string (including `''`);
4. `'http://localhost:8080'`.

`''` means same-origin at every step: requests go to `/v1/...` on the page's own origin. The exported `API_BASE_URL` constant is deprecated: it is evaluated once at import time; use `getApiBaseUrl()` instead.

**Trust note.** Only the value passed to `configureUsersApi` is validated. The `NEXT_PUBLIC_API_BASE_URL` and `window.__USERS_API_URL__` fallbacks are trusted deploy-time inputs, used as-is **without** that validation, and the bearer token is sent to whatever URL resolves. They must come only from your deployment configuration, never from user-controlled input (query strings, storage, or anything an attacker can influence). Prefer calling `configureUsersApi({ baseUrl })` explicitly so the value is validated.

## Frontend contract

The library implements the mod-users frontend contract: every tab is its own session client, and the only credential on the wire is a Bearer access token.

### Storage and requests

- **Per-tab storage.** The access token, the refresh token and the active profile uuid live in `sessionStorage` (one set per tab), never in `localStorage` and never in cookies. Where `sessionStorage` is unavailable or full, an in-memory map stands in for the page lifetime; with no `window` (SSR) nothing is stored. The access token sits under `tokenStorageKey` (default `auth_token`), which is the key `@moduleforge/core-gui`'s api-client reads, so the other module clients in your app send the same tab's token.
- **Bearer only.** Every request sends `Authorization: Bearer <this tab's access token>` and `credentials: 'omit'`. The one deliberate exception is the pair of OIDC authorize-URL start calls (`me.credentials.startOidcLink` and `session.stepUp.startOidc`): they use `credentials: 'include'` because the browser would otherwise discard the signed `oidc_state` cookie that the redirect callback needs. That cookie is an anti-forgery state, not an authentication cookie, and both calls still carry the Bearer token.

### Refresh lineage

Each tab owns exactly one refresh lineage. On access-token expiry (a `401` that is not a revocation) the client calls `POST /v1/auth/token` with the tab's refresh token and active profile, stores the rotated refresh token, and replays the request once. Concurrent requests in a tab share one in-flight refresh, because a second rotation of the same token would trip the server's reuse detection. A `401` that still fails after the replay signs the tab out. `401 users.session_revoked` and `401 users.refresh_reuse` clear the tab's tokens and sign it out immediately, without a refresh attempt: the access, refresh and profile entries are removed, `useAuth()` returns to signed-out, and the unauthenticated handler runs once.

### New tabs: fork

A tab that starts with no tokens asks its same-origin sibling tabs for an access token over a `BroadcastChannel` and calls `POST /v1/session/fork` with the first one the server accepts, which gives the new tab its own lineage. Only access tokens cross the channel, never refresh tokens. If nobody answers within `forkTimeoutMs`, the tab stays signed out. Where `BroadcastChannel` is undefined (tests, SSR) the tab simply stays signed out, and a signed-in tab with an impersonation session does not offer its token (the server refuses to fork an impersonation).

### Profiles

The active profile is the profile named by the tab's access token.

- **Landing.** A sign-in lands on the credential's default profile, then the user's default profile, then the personal profile, then the only available profile. If none applies, the sign-in answers `selection_required: true` with no access token; `useAuth().selectionRequired` is then `true` and `selectProfile(uuid)` (the refresh grant with the chosen profile) completes the sign-in. Mount a page at `USERS_GUI_ROUTES.selectProfile` that lists `useAuth().profiles`.
- **Switching.** `switchProfile(uuid)` calls `POST /v1/session/switch` and replaces only this tab's access token and active profile; the refresh token and every other tab are untouched.
- **Locks.** A `401 users.profile_unavailable` keeps the tokens and sets `useAuth().profileLock` to `{ profileUuid, reason, remedy }`. The `reason` is the server's lock reason (for example `profile_suspended`, `acr_too_low`, `credential_not_authorized`); the `remedy` (`sign_in_with_provider`, `step_up`, `sign_in_with_authorized_credential`, `accept_invitation`) comes from that profile's entry in `profiles` and is `null` when the lock appeared after the list was fetched (for example after a reload). A locked access token cannot call the API, so recover by choosing another profile with `selectProfile` (the lock clears on the next successful token response), by following the remedy, or by signing in again.
- **Impersonation.** `useAuth().impersonating` is `true` while the tab's session is an impersonation. `api.impersonations.start` swaps in the impersonation access token but keeps the administrator's refresh token and stored profile; `end()` deletes the impersonation and restores the administrator's token through the refresh grant.

### Step-up

A `409 users.step_up_required` response runs the registered step-up handler and replays the original request once with the fresh access token the handler resolves with. Register the handler with the `AuthProvider` prop `stepUpHandler`, the `useStepUpHandler(handler)` hook, or `useAuth().registerStepUpHandler(handler)` (memoize the function). Its interface:

```ts
interface StepUpHandler {
  (request: { error: ApiActionRequiredError }): Promise<string>; // resolves with the fresh access token
}
```

The prompt UI calls `api.session.stepUp.password(...)`, `requestEmailCode` / `verifyEmailCode(...)`, or `startOidc(provider)`; the password and email-code calls return a token response, which the client has already stored for the tab, so the handler only returns `response.access_token`. Concurrent requests that hit the 409 share one handler call. Without a registered handler, or when the handler rejects (for example the user cancels), the original `ApiActionRequiredError` propagates. The step-up routes never invoke the handler themselves.

### OIDC sign-in and handoff

OIDC sign-in returns through `#handoff=<code>` in the URL fragment (`#error=<code>` on a refusal), exchanged once at `POST /v1/auth/handoff`. The code is bound to the browser tab that started the sign-in, PKCE style:

1. `api.auth.oidcStart(provider, { returnPath? })` generates a random verifier (32 random bytes, 43 base64url characters), keeps it in `sessionStorage`, and resolves with the start URL carrying only the verifier's SHA-256 challenge as `handoff_challenge`. Navigate the browser to that URL (`window.location.assign(url)`).
2. On the return page, `useAuth().completeHandoff(code)` sends the code and the stored verifier in a single `POST /v1/auth/handoff`, stores the token response like a login, and clears the verifier whether or not the exchange succeeded. Repeated calls with the same code share that one exchange, so a double-invoked effect is harmless.
3. A tab that started no sign-in (no stored verifier) fails with `ApiRequestError` code `handoff_verifier_missing` and a readable message, and sends nothing.

### The auth context

`useAuth()` (inside an `AuthProvider`) returns:

| Member | Meaning |
|---|---|
| `token` | This tab's access token; `null` when signed out or awaiting profile selection |
| `session` | The current `SessionView` (`uuid`, `acr`, `amr`, `auth_time`, `impersonating`) |
| `profile`, `profiles` | The active `ProfileView` and all of the principal's profiles (with `available` and `lock`) |
| `user`, `emailVerified` | The `GET /v1/me` record and whether any email is verified |
| `isLoading` | `true` until the mount-time session check or fork finishes |
| `selectionRequired`, `profileLock`, `impersonating` | See above |
| `login(email, password, options?)`, `register(...)` | Sign in or register; both resolve with the `TokenResponse` |
| `selectProfile(uuid)`, `switchProfile(uuid)` | See [Profiles](#profiles) |
| `completeHandoff(code)` | See [OIDC sign-in and handoff](#oidc-sign-in-and-handoff) |
| `logout()` | Revokes the server session (best effort), clears this tab, and navigates to `loginPath` through `onNavigate` |
| `refreshUser()` | Re-reads the session and user record |
| `registerStepUpHandler(handler)` | See [Step-up](#step-up) |

There is no `isAdmin`: platform-administrator authority is an operator-party profile plus grants and cannot be derived on the client, so the server's `403` is the authority. Every client method that returns a token response (`api.auth.login`, `register`, `verifyEmailCode`, `handoff`, `token`, `session.switch`, `session.fork`, `session.stepUp.*`, `impersonations.*`, `bootstrap.bootstrap`) stores it in the tab itself; `useAuth()` follows through `subscribeSessionEvents`, so calling the client directly (for example `EmailCodePage` calling `api.auth.verifyEmailCode`) signs the context in without any extra call.

## Unauthenticated handling

A `401` first tries one token refresh (see [Refresh lineage](#refresh-lineage)). When the session cannot be repaired (no refresh token, a revoked or reused lineage, or a replay that still fails), the client clears the tab's access token, refresh token and profile and runs the unauthenticated handler, and the request rejects with an `ApiRequestError` (`status: 401`, carrying the response's own error code). The handler is the **only** place the library navigates on loss of authentication: by default it sets `window.location.href` to the configured login route (`unauthenticatedRedirectUrl`, default `USERS_GUI_ROUTES.login`), and `onUnauthenticated` replaces it entirely. No other code path navigates to a hard-coded login path.

- **`unauthenticatedRedirectUrl`** changes the redirect target (a path or an absolute `http(s)` URL).
- **`unauthenticatedReturnParam`** appends the current page to the redirect target as a query parameter, for example `/login?return=%2Fteams%3Fpage%3D2`. The value is the current `pathname + search` (never the hash). It is always a value `isSafeReturnPath` accepts; a current page that predicate would reject (for example `/team:alpha/x`) is replaced by `/`. If the redirect target is the page you are already on (exact pathname match), the redirect is skipped to avoid a loop; the token is still cleared and the request still rejects.
- **`onUnauthenticated(ctx)`** replaces the redirect entirely. It is called after the tab's tokens are cleared, with `ctx.returnPath`: `pathname + search` when that is a safe site path, otherwise `'/'`. Because `ctx.returnPath` is only checked as a site path, run it through `isSafeReturnPath` if you will use it as a navigation target. A handler that throws is caught and logged with `console.error`, and the request still rejects with the `401` `ApiRequestError`, so callers never see the handler's error in place of the 401. Passing `null` restores the default.
- **`useAuth().emailVerified`** is `true` when the signed-in user holds at least one verified email (from `GET /v1/me`), and `false` while unverified or signed out. The context loads the user record after every sign-in, so it is correct without a follow-up `refreshUser()`.
- **`AuthProvider loginPath`** is the path the provider navigates to (through `onNavigate`) on `logout()`. The fetch-layer handler above handles sign-outs the server forced (revocation, expiry), so the provider does not navigate for those. Default `'/auth/login'`; an invalid value logs an error and falls back to the default.

Why both a fetch-layer setting and a provider setting? The fetch layer has no router, so it navigates through `window.location` (a full page load). The provider navigates through the `onNavigate` callback you inject (a soft client-side navigation). An app with a client-side router should set both to its login route.

Security note: an **absolute** `unauthenticatedRedirectUrl` combined with `unauthenticatedReturnParam` sends the current `pathname + search` to that origin as a query parameter. Point it only at an origin you trust with those values.

Credential failures are not session expiry. A `401` from a wrong password, a wrong email code, a bad or expired reset token, or a refused handoff shows an inline error and never triggers a refresh, the redirect or `onUnauthenticated`.

## Components and props

All components are client components (`'use client'` is not retained in `dist`; see [Next.js](#nextjs-app-router)). Callbacks default to no-ops unless noted.

| Component | Props | Notes |
|---|---|---|
| `AuthProvider` | `children`; `onNavigate?(path)`; `loginPath?`; `stepUpHandler?` | Session context (see [Frontend contract](#frontend-contract)). Navigates through `onNavigate` on logout and on an action-required response (see [Routes](#routes)). Skips a navigation to an action-required path the browser is already on, comparing `window.location.pathname` for exact equality; apps served under a `basePath`, or with trailing-slash URLs, do not match and can still navigate to the page they are on (known limitation). `useAuth()` throws outside it; `useOptionalAuth()` returns `null`. |
| `RequireAuth` | `children`; `requireAdmin?`; `onUnauthenticated?()`; `onUnauthorized?()` | Renders children only for a signed-in user (and an admin when `requireAdmin`). Build your own `?return=` redirect in `onUnauthenticated`. Needs `AuthProvider`. |
| `AuthPage` | `initialMode?: 'login' \| 'register'`; `onAuthenticated?(returnPath: string \| null, mode: 'login' \| 'register')`; `initialError?`; `returnPath?`; `allowRegistration?` (default `true`); `onForgotPassword?()` | Login and register in one card. `allowRegistration={false}` removes the register control and panel and forces login mode. Needs `AuthProvider`. |
| `LoginForm` | `onSuccess?(returnPath: string \| null)`; `initialError?`; `returnPath?`; `idPrefix?`; `onForgotPassword?()` | Email and password plus OIDC provider buttons. `onForgotPassword` renders a "Forgot password?" button. Needs `AuthProvider`. |
| `RegisterForm` | `onSuccess?()`; `idPrefix?` | Registers, then signs in. Needs `AuthProvider`. |
| `OidcCallbackPage` | `onComplete(returnPath: string)`; `onError(message: string)`; `defaultReturnPath?` (default `'/'`) | Handles the OIDC return. Needs `AuthProvider`. |
| `ForgotPasswordPage` | `onNavigateToLogin?()` | Requests a reset email. No `AuthProvider` needed. |
| `ResetPasswordPage` | `token: string`; `onSuccess?()`; `onNavigateToLogin?()` | You read the token from the URL and pass it in. No `AuthProvider` needed. |
| `EmailCodePage` | `onSuccess?()`; `onNavigateToLogin?()` | Sign in with an emailed code. Needs `AuthProvider`. |
| `VerifyEmailPage` | `onVerified?()`; `onNavigateToLogin?()`; `email?`; `message?`; `resendCooldownSeconds?` (default `30`, `0` disables) | See [Standard pages](#standard-pages). |
| `OidcConfigPage` | `onComplete?()`; `redirectDelayMs?` (default `2000`) | See [Standard pages](#standard-pages). |
| `OidcSetupGate` | `currentPath: string`; `onNavigateToConfig?()`; `children: (state: 'ready' \| 'setup') => ReactNode` | Render-prop gate for apps without the admin chrome. |
| `ClientLayout` | `children`; `currentPath: string`; `onNavigateToConfig?()`; `onNavigate?(path)`; `LinkComponent` | mod-users admin chrome (sidebar shell plus `AuthProvider` plus `OidcSetupGate`). |
| `USERS_GUI_ROUTES` | constant | The standard path set; see [Routes](#routes). |

### Return-path handling

- **`onAuthenticated(returnPath, mode)` / `onSuccess(returnPath)`** receive `string | null`; `AuthPage` additionally passes `mode` (`'login'` or `'register'`) as a second argument. Callbacks that declare fewer parameters keep working unchanged.
- The value is the explicit `returnPath` prop if given, else the value of the `?<unauthenticatedReturnParam>=` query parameter, else `null`. The query fallback only applies when `unauthenticatedReturnParam` is configured; without it, the app reads its own `?return=` as before.
- **`isSafeReturnPath(candidate)`** is the single shared predicate: the 401 handler uses it when writing the return value and `readReturnPath()` (and `OidcCallbackPage`) use it when reading one. It accepts a site-relative path with one leading `/`, and rejects `//`, backslashes, control characters, and a `:` in the first segment. It is exported so your own code can validate values with the same rules.
- **Only the query-parameter fallback is validated.** An explicit `returnPath` prop to `LoginForm` or `AuthPage` is passed through as supplied, so the app owns validating it (call `isSafeReturnPath` before passing a value derived from user input).
- `readReturnPath()` returns the validated value or `null`; it also returns `null` when `unauthenticatedReturnParam` is unset or there is no window.

## Routes

The library exports the standard path set as `USERS_GUI_ROUTES`:

```ts
{
  login: '/auth/login',
  oidcReturn: '/auth/oidc/return',
  forgotPassword: '/forgot-password',
  resetPassword: '/reset-password',
  emailCode: '/auth/email-code',
  verifyEmail: '/verify-email',
  oidcConfig: '/oidc-config',
  selectProfile: '/auth/select-profile',
  invitations: '/invitations',
  account: '/account',
  accountCredentials: '/account/credentials',
  accountIdentifiers: '/account/identifiers',
  accountSessions: '/account/sessions',
  bootstrap: '/bootstrap',
}
```

Apps use the standard paths. A deviation needs a stated reason (for example an already-deployed URL) and goes through the configuration seams (`unauthenticatedRedirectUrl`, `AuthProvider loginPath`). Rows marked backend-fixed are not negotiable.

| Path | Fixed by | Component and behavior |
|---|---|---|
| `/auth/login` (standard) | App | `AuthPage` or `LoginForm`; navigate onward in `onAuthenticated` / `onSuccess`. Only the default `401` and logout target; configurable. |
| `/reset-password?token=<hex>` | Backend (the password-reset email links here) | `ResetPasswordPage token={...}`. The component never reads the URL; the page passes the token. |
| `/auth/oidc/return` | Deployment env `AUTH_FRONTEND_RETURN_URL` | `OidcCallbackPage`. The one-time code arrives in the URL **fragment** as `#handoff=<code>` (a refusal as `#error=<code>`), exchanged once with `completeHandoff`; see [OIDC sign-in and handoff](#oidc-sign-in-and-handoff). The page strips the hash. Forward `onError(message)` to the login route as `?error=<encodeURIComponent(message)>`, which feeds `initialError`. |
| `/forgot-password` | App | `ForgotPasswordPage`. Reachable from the login form only if you pass `onForgotPassword` or render your own link. |
| `/auth/email-code` | App | `EmailCodePage`. |
| `/verify-email` | Backend (`403` action-required `users.email_unverified`) | `VerifyEmailPage`. |
| `/auth/select-profile`, `/invitations`, `/account`, `/account/credentials`, `/account/identifiers`, `/account/sessions`, `/bootstrap` (standard) | App | Recommended defaults for landing profile selection, party invitations, account settings, and first-run bootstrap. |
| `/oidc-config` | Backend (`503` action-required `users.oidc_not_confirmed`; the setup-token banner in the server log prints this path) | `OidcConfigPage`, normally wrapped by `OidcSetupGate`. |

**Action-required contract.** When an API response is an action-required envelope, the client throws `ApiActionRequiredError` carrying the envelope's `path`. `AuthProvider` handles it on its own calls (the mount-time session and `GET /v1/me` check and `refreshUser`): it keeps the session intact and calls `onNavigate(err.path)`, unless the browser is already on exactly that path (exact `pathname` equality; see the limitation noted under `AuthProvider`). So `/verify-email` and `/oidc-config` must exist and `onNavigate` must perform a real navigation. Unsafe envelope paths are rejected and replaced with a safe fallback.

## Standard pages

### `VerifyEmailPage`

A code-entry flow for the `/verify-email` route: the user requests a six-digit emailed code and enters it. There is no emailed link.

- **Email source.** It uses the `email` prop if given, else the signed-in user's email from a surrounding `AuthProvider`, else shows an email field first.
- **Props.** `message` overrides the lead sentence (default: the backend's own text). `resendCooldownSeconds` throttles "Send a new code" on the client (default `30`, `0` disables). `onNavigateToLogin`, when given, renders a "Sign in with a different account" button; the page never signs the user out itself.
- **`onVerified`** runs once after successful verification, after the signed-in user is refreshed when an `AuthProvider` is present. The app owns the navigation onward.
- Works inside or outside an `AuthProvider`. It does not detect an already-verified email on its own.

### `OidcConfigPage` and `OidcSetupGate`

`OidcConfigPage` is the dual-mode page for the `/oidc-config` route:

- **Setup-token mode** (no admin session): the operator pastes the setup token printed in the server log and chooses providers. On success it calls `onComplete` after `redirectDelayMs`.
- **Admin mode** (signed-in admin, detected through `useOptionalAuth`): reconfigure providers; the page stays put and `onComplete` is not called.

`onComplete` should perform a **full-page navigation** to the login route (for example `window.location.assign(USERS_GUI_ROUTES.login)`). `OidcSetupGate` and `ClientLayout` probe OIDC status only on mount, so a soft client-side navigation would leave them in the stale needs-setup state.

`OidcSetupGate` is for apps that do not use `ClientLayout`. While OIDC is unconfirmed even the login page's provider call fails, so the gate probes first: it renders `children('ready')` once OIDC is confirmed (any probe failure, including a network error, is treated as needs-setup so an operator can still reach the config page), calls `onNavigateToConfig` when the user is on any other route while OIDC is unconfirmed, and renders `children('setup')` when `currentPath` is the config page. Render the `'ready'` branch inside your `AuthProvider`; render the `'setup'` branch **without** one (`/v1/me` would answer `503`). `OidcConfigPage` itself works inside or outside an `AuthProvider`, and an `AuthProvider` above it will not loop because of the same-path guard.

`ClientLayout` already composes `OidcSetupGate` and `AuthProvider`; use one or the other, not both.

## Next.js (app router)

- Import only from client components (`'use client'` at the top of each file that imports the package; the built `dist` does not carry the directive).
- Wrap `useSearchParams()` consumers in `<Suspense>`.
- One React instance: the app, `users-gui` and `core-gui` must share a single `react` (`^19`).
- Call `configureUsersApi` at module top of the client shell. Pass the literal `process.env.NEXT_PUBLIC_API_BASE_URL` expression in **your** code so Next inlines it; `''` is honored as same-origin.
- The components render without `window` during the server pass.

```tsx
// app/app-shell.tsx
'use client';

import type { ReactNode } from 'react';
import { usePathname, useRouter } from 'next/navigation';
import {
  AuthProvider,
  OidcSetupGate,
  USERS_GUI_ROUTES,
  configureUsersApi,
} from '@moduleforge/users-gui';

configureUsersApi({
  baseUrl: process.env.NEXT_PUBLIC_API_BASE_URL ?? 'http://localhost:8080',
  unauthenticatedReturnParam: 'return',
});

export function AppShell({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname() ?? '/';
  return (
    <OidcSetupGate
      currentPath={pathname}
      onNavigateToConfig={() => router.replace(USERS_GUI_ROUTES.oidcConfig)}
    >
      {(state) =>
        state === 'setup' ? (
          children
        ) : (
          <AuthProvider onNavigate={(path) => router.replace(path)}>
            {children}
          </AuthProvider>
        )
      }
    </OidcSetupGate>
  );
}
```

```tsx
// app/layout.tsx (server component)
import type { ReactNode } from 'react';
import { AppShell } from './app-shell';

export default function RootLayout({ children }: { children: ReactNode }) {
  return (
    <html lang="en">
      <body>
        <AppShell>{children}</AppShell>
      </body>
    </html>
  );
}
```

```tsx
// app/auth/login/page.tsx
'use client';

import { Suspense } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { AuthPage, USERS_GUI_ROUTES } from '@moduleforge/users-gui';

function LoginContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  return (
    <AuthPage
      initialError={searchParams?.get('error') ?? null}
      onAuthenticated={(returnPath) => router.replace(returnPath ?? '/')}
      onForgotPassword={() => router.push(USERS_GUI_ROUTES.forgotPassword)}
    />
  );
}

export default function LoginPage() {
  return (
    <Suspense fallback={null}>
      <LoginContent />
    </Suspense>
  );
}
```

```tsx
// app/reset-password/page.tsx
'use client';

import { Suspense } from 'react';
import { useRouter, useSearchParams } from 'next/navigation';
import { ResetPasswordPage, USERS_GUI_ROUTES } from '@moduleforge/users-gui';

function ResetContent() {
  const router = useRouter();
  const searchParams = useSearchParams();
  return (
    <ResetPasswordPage
      token={searchParams?.get('token') ?? ''}
      onSuccess={() => router.replace(USERS_GUI_ROUTES.login)}
      onNavigateToLogin={() => router.replace(USERS_GUI_ROUTES.login)}
    />
  );
}

export default function ResetPage() {
  return (
    <Suspense fallback={null}>
      <ResetContent />
    </Suspense>
  );
}
```

```tsx
// app/auth/oidc/return/page.tsx
'use client';

import { useRouter } from 'next/navigation';
import { OidcCallbackPage, USERS_GUI_ROUTES } from '@moduleforge/users-gui';

export default function OidcReturnPage() {
  const router = useRouter();
  return (
    <OidcCallbackPage
      onComplete={(returnPath) => router.replace(returnPath)}
      onError={(message) =>
        router.replace(
          `${USERS_GUI_ROUTES.login}?error=${encodeURIComponent(message)}`,
        )
      }
    />
  );
}
```

```tsx
// app/verify-email/page.tsx
'use client';

import { useRouter } from 'next/navigation';
import { VerifyEmailPage, USERS_GUI_ROUTES } from '@moduleforge/users-gui';

export default function VerifyPage() {
  const router = useRouter();
  return (
    <VerifyEmailPage
      onVerified={() => router.replace('/')}
      onNavigateToLogin={() => router.replace(USERS_GUI_ROUTES.login)}
    />
  );
}
```

```tsx
// app/oidc-config/page.tsx
'use client';

import { OidcConfigPage, USERS_GUI_ROUTES } from '@moduleforge/users-gui';

export default function OidcConfigRoute() {
  return (
    <OidcConfigPage
      onComplete={() => {
        // Full-page navigation: the setup gate only probes status on mount.
        window.location.assign(USERS_GUI_ROUTES.login);
      }}
    />
  );
}
```

`unauthenticatedReturnParam: 'return'` makes a mid-session `401` redirect to `/auth/login?return=<path>`, and `AuthPage` hands that validated value to `onAuthenticated`. Because the standard login path is used, no `unauthenticatedRedirectUrl` or `loginPath` is needed.

## React Router

A single-page app can mirror app-mftodo. With a non-standard login route, set both seams:

```tsx
import { useNavigate } from 'react-router-dom';
import type { ReactNode } from 'react';
import { AuthProvider, configureUsersApi } from '@moduleforge/users-gui';

configureUsersApi({
  baseUrl: window.location.origin,
  unauthenticatedRedirectUrl: '/login',
});

export function Providers({ children }: { children: ReactNode }) {
  const navigate = useNavigate();
  return (
    <AuthProvider loginPath="/login" onNavigate={(path) => navigate(path)}>
      {children}
    </AuthProvider>
  );
}
```

`configureUsersApi({ baseUrl: window.location.origin })` replaces the import-order trick of setting a global before the package loads, and `unauthenticatedRedirectUrl: '/login'` replaces an `/auth/login` alias route. Everything else (route components, callbacks) is the same as the Next.js examples with `useNavigate` in place of `useRouter` and `useSearchParams` from `react-router-dom`.

## Not provided

So integrators do not assume otherwise:

- Local-login return handling is opt-in via `unauthenticatedReturnParam`. The callback receives the validated return path or `null`; without that setting the app reads its own `?return=` as before.
- `AuthPage` has no link to the email-code flow; render your own link to `EmailCodePage`'s route if you want one.
- There is no built-in step-up prompt in the lib layer: register a `StepUpHandler` (see [Step-up](#step-up)) whose UI calls `api.session.stepUp.*`.
- `VerifyEmailPage` does not detect an already-verified email.
- `ClientLayout` and `SidebarNav` are mod-users admin chrome. `ClientLayout` has no `loginPath` prop and `SidebarNav` hard-codes `/auth/login`; apps with their own shell should use `AuthProvider` and `OidcSetupGate` directly.
- `allowRegistration={false}` is UI-only: the API's register endpoint stays open.
- An explicit `returnPath` prop is not validated (see [Return-path handling](#return-path-handling)).

## Migration notes

The client was rebuilt on the final mod-users API; these changes break earlier integrations:

- Tokens moved from `localStorage` to per-tab `sessionStorage` (plus a refresh token and active profile), and requests use `credentials: 'omit'`. Existing signed-in browsers must sign in again.
- `useAuth()` lost `isAdmin`, `emailVerifiedAt`, `setTokenAndUser` and `completeExternalLogin`; it gained the members listed under [The auth context](#the-auth-context). `completeHandoff(code)` replaces `completeExternalLogin(token)`: OIDC returns a one-time `#handoff=` code, never a token.
- The client dropped `self`, `userAccounts` (including `grantAdmin`, `revokeAdmin`, `assume`), `sshKeys`, `stepUp` (the old `X-Step-Up-Token` flow), `apps` and `auth.verifyEmail`, and the types `UserAccount*`, `UserAccountSelf`, `SessionUser`, `LoginResponse`, `StepUpOptions`, `StepUpVerifyResponse`, `SSHKey*`, `RegisterSSHKeyRequest` and the app types. Use `me.*`, `session.*`, `impersonations.*` and `bootstrap.*`.
- `register` answers a session directly (`201` with a token response), so there is no follow-up login.
- `NEXT_PUBLIC_API_BASE_URL=""` means same-origin instead of falling through to the localhost default.
- Wrong credentials (login, email code, reset token, handoff) show an inline error instead of redirecting to the login page.
