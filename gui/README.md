# @moduleforge/users-gui

Consumer integration guide for apps that embed the mod-users React client.

## What this library is

`@moduleforge/users-gui` is a set of router-agnostic client components plus a typed API client for the mod-users backend. It owns **no routes** and ships **no CSS file**: your app creates each route, mounts the component, and injects navigation through props (`onNavigate`, `onSuccess`, `onComplete`, and so on). Components never read a router; the few that read the URL (`LoginForm`, `AuthPage` through `readReturnPath`, `OidcCallbackPage`) read `window.location` directly and only when told to.

Everything below is exported from the package root (`gui/src/index.ts`).

## Consumption

The package is not published to any registry. An app consumes it as a member of a **bun workspace** it owns: a root `package.json` listing this repo's `gui/` (and the sibling `mod-core/gui`) in `workspaces`, with the app's own `package.json` referring to `@moduleforge/users-gui` as `workspace:*`. `@moduleforge/core-gui` is an optional peer dependency (`react` and `react-dom` `^19` are required peers).

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
| `tokenStorageKey` | `string` | `'auth_token'` (`USERS_TOKEN_KEY`) | non-empty string |
| `unauthenticatedRedirectUrl` | `string` | `'/auth/login'` | a path starting with exactly one `/` (no `//`, backslash or control characters), or an absolute `http(s)` URL |
| `unauthenticatedReturnParam` | `string \| null` | `null` (append nothing) | `null` or matching `/^[A-Za-z0-9_-]+$/` |
| `onUnauthenticated` | `((ctx: UnauthenticatedContext) => void) \| null` | `null` (default redirect) | function or `null`; `null` restores the default handler |

Related helpers:

- `resetUsersApiConfig()` restores every default. It exists mainly for tests.
- `getApiBaseUrl()` returns the resolved base URL (evaluated on every call).
- `getTokenStorageKey()`, `getStoredToken()` and `clearStoredToken()` read the key in effect, read the stored token (`null` during SSR), and remove it. `USERS_TOKEN_KEY` is the default key.
- `UsersApiConfig` and `UnauthenticatedContext` are the exported types.

**Token key semantics.** Configure `tokenStorageKey` before the `AuthProvider` mounts, and do not change it mid-session: the token already stored under the old key is not migrated.

### Base URL precedence

`getApiBaseUrl()` resolves, in order:

1. the value set through `configureUsersApi({ baseUrl })`;
2. `process.env.NEXT_PUBLIC_API_BASE_URL`, when it is defined (including `''`);
3. `window.__USERS_API_URL__`, when it is a string (including `''`);
4. `'http://localhost:8080'`.

`''` means same-origin at every step: requests go to `/v1/...` on the page's own origin. The exported `API_BASE_URL` constant is deprecated: it is evaluated once at import time; use `getApiBaseUrl()` instead.

**Trust note.** Only the value passed to `configureUsersApi` is validated. The `NEXT_PUBLIC_API_BASE_URL` and `window.__USERS_API_URL__` fallbacks are trusted deploy-time inputs, used as-is **without** that validation, and the bearer token is sent to whatever URL resolves. They must come only from your deployment configuration, never from user-controlled input (query strings, storage, or anything an attacker can influence). Prefer calling `configureUsersApi({ baseUrl })` explicitly so the value is validated.

## Unauthenticated handling

By default, any authenticated request that returns `401` clears the stored token and hard-redirects the browser (`window.location.href`) to `/auth/login`, and the request rejects with an `ApiRequestError` (`status: 401`). That behavior is unchanged unless you configure one of the settings below.

- **`unauthenticatedRedirectUrl`** changes the redirect target (a path or an absolute `http(s)` URL).
- **`unauthenticatedReturnParam`** appends the current page to the redirect target as a query parameter, for example `/login?return=%2Fteams%3Fpage%3D2`. The value is the current `pathname + search` (never the hash). It is always a value `isSafeReturnPath` accepts; a current page that predicate would reject (for example `/team:alpha/x`) is replaced by `/`. If the redirect target is the page you are already on (exact pathname match), the redirect is skipped to avoid a loop; the token is still cleared and the request still rejects.
- **`onUnauthenticated(ctx)`** replaces the redirect entirely. It is called after the stored token is cleared, with `ctx.returnPath`: `pathname + search` when that is a safe site path, otherwise `'/'`. Because `ctx.returnPath` is only checked as a site path, run it through `isSafeReturnPath` if you will use it as a navigation target. A handler that throws is caught and logged with `console.error`, and the request still rejects with the `401` `ApiRequestError`, so callers never see the handler's error in place of the 401. Passing `null` restores the default.
- **`AuthProvider loginPath`** is the path the provider navigates to (through `onNavigate`) on `logout()` and when its own `/v1/self` check returns `401`. Default `'/auth/login'`; an invalid value logs an error and falls back to the default.

Why both a fetch-layer setting and a provider setting? The fetch layer has no router, so it navigates through `window.location` (a full page load). The provider navigates through the `onNavigate` callback you inject (a soft client-side navigation). An app with a client-side router should set both to its login route.

Security note: an **absolute** `unauthenticatedRedirectUrl` combined with `unauthenticatedReturnParam` sends the current `pathname + search` to that origin as a query parameter. Point it only at an origin you trust with those values.

Credential failures are not session expiry. A `401` from a wrong password, a wrong email code, or a bad or expired reset token shows an inline error in the form and never triggers the redirect or `onUnauthenticated`.

## Components and props

All components are client components (`'use client'` is not retained in `dist`; see [Next.js](#nextjs-app-router)). Callbacks default to no-ops unless noted.

| Component | Props | Notes |
|---|---|---|
| `AuthProvider` | `children`; `onNavigate?(path)`; `loginPath?` | Session context. Navigates through `onNavigate` on logout, on a `401` from `/v1/self`, and on an action-required response (see [Routes](#routes)). Skips a navigation to an action-required path the browser is already on, comparing `window.location.pathname` for exact equality; apps served under a `basePath`, or with trailing-slash URLs, do not match and can still navigate to the page they are on (known limitation). `useAuth()` throws outside it; `useOptionalAuth()` returns `null`. |
| `RequireAuth` | `children`; `requireAdmin?`; `onUnauthenticated?()`; `onUnauthorized?()` | Renders children only for a signed-in user (and an admin when `requireAdmin`). Build your own `?return=` redirect in `onUnauthenticated`. Needs `AuthProvider`. |
| `AuthPage` | `initialMode?: 'login' \| 'register'`; `onAuthenticated?(returnPath: string \| null)`; `initialError?`; `returnPath?`; `allowRegistration?` (default `true`); `onForgotPassword?()` | Login and register in one card. `allowRegistration={false}` removes the register control and panel and forces login mode. Needs `AuthProvider`. |
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

- **`onAuthenticated(returnPath)` / `onSuccess(returnPath)`** receive `string | null`. Callbacks that declare no parameter keep working unchanged.
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
}
```

Apps use the standard paths. A deviation needs a stated reason (for example an already-deployed URL) and goes through the configuration seams (`unauthenticatedRedirectUrl`, `AuthProvider loginPath`). Rows marked backend-fixed are not negotiable.

| Path | Fixed by | Component and behavior |
|---|---|---|
| `/auth/login` (standard) | App | `AuthPage` or `LoginForm`; navigate onward in `onAuthenticated` / `onSuccess`. Only the default `401` and logout target; configurable. |
| `/reset-password?token=<hex>` | Backend (the password-reset email links here) | `ResetPasswordPage token={...}`. The component never reads the URL; the page passes the token. |
| `/auth/oidc/return` | Deployment env `AUTH_FRONTEND_RETURN_URL` | `OidcCallbackPage`. The token and `return` arrive in the URL **fragment** (`#token=...&return=...`); a provider error arrives as `?error=` in the query. The page strips the hash. Forward `onError(message)` to the login route as `?error=<encodeURIComponent(message)>`, which feeds `initialError`. |
| `/forgot-password` | App | `ForgotPasswordPage`. Reachable from the login form only if you pass `onForgotPassword` or render your own link. |
| `/auth/email-code` | App | `EmailCodePage`. |
| `/verify-email` | Backend (`403` action-required `users.email_unverified`) | `VerifyEmailPage`. |
| `/oidc-config` | Backend (`503` action-required `users.oidc_not_confirmed`; the setup-token banner in the server log prints this path) | `OidcConfigPage`, normally wrapped by `OidcSetupGate`. |

**Action-required contract.** When an API response is an action-required envelope, the client throws `ApiActionRequiredError` carrying the envelope's `path`. `AuthProvider` handles it on its own calls (the mount-time `/v1/self` check, `refreshUser`, `completeExternalLogin`): it keeps the session intact and calls `onNavigate(err.path)`, unless the browser is already on exactly that path (exact `pathname` equality; see the limitation noted under `AuthProvider`). So `/verify-email` and `/oidc-config` must exist and `onNavigate` must perform a real navigation. Unsafe envelope paths are rejected and replaced with a safe fallback.

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

`OidcSetupGate` is for apps that do not use `ClientLayout`. While OIDC is unconfirmed even the login page's provider call fails, so the gate probes first: it renders `children('ready')` once OIDC is confirmed (any probe failure, including a network error, is treated as needs-setup so an operator can still reach the config page), calls `onNavigateToConfig` when the user is on any other route while OIDC is unconfirmed, and renders `children('setup')` when `currentPath` is the config page. Render the `'ready'` branch inside your `AuthProvider`; render the `'setup'` branch **without** one (`/v1/self` would answer `503`). `OidcConfigPage` itself works inside or outside an `AuthProvider`, and an `AuthProvider` above it will not loop because of the same-path guard.

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
- There is no `/step-up` page. The backend's `/step-up` action-required path is not mounted by this library, and `AuthProvider` has no step-up navigation; do not link to it.
- `VerifyEmailPage` does not detect an already-verified email.
- `ClientLayout` and `SidebarNav` are mod-users admin chrome. `ClientLayout` has no `loginPath` prop and `SidebarNav` hard-codes `/auth/login`; apps with their own shell should use `AuthProvider` and `OidcSetupGate` directly.
- `allowRegistration={false}` is UI-only: the API's register endpoint stays open.
- An explicit `returnPath` prop is not validated (see [Return-path handling](#return-path-handling)).

## Migration notes

Everything in this guide is additive: existing apps keep working with no changes. Two behaviors changed:

- `NEXT_PUBLIC_API_BASE_URL=""` now means same-origin instead of falling through to the localhost default.
- Wrong credentials (login, email code, reset token) now show an inline error instead of redirecting to the login page.
