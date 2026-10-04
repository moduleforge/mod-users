# Standard VerifyEmailPage and Exported USERS_GUI_ROUTES

## Purpose and scope

The backend sends an authenticated-but-unverified user to `/verify-email` (action-required envelope `users.email_unverified`, `api/internal/auth/require_verified.go:34-37`), and `AuthProvider` already navigates there (`lib/auth-context.tsx`), but no frontend renders it. Add a standard, router-agnostic `VerifyEmailPage` to users-gui so every app mounts the same screen, plus a small exported constant of the standard paths. Facts and the backend contract are in [`../notes/embedding-and-routes.md`](../notes/embedding-and-routes.md). Scope: `gui/src/components/verify-email-page.tsx` (new), `gui/src/lib/routes.ts` (new), `gui/src/lib/api.ts` (client additions), `gui/src/index.ts`, tests, one story. Depends on task `001` (lazy base URL/token key, `config.ts`). Parallel-eligible with `002`-`004`; `006` depends on this task (it consumes `routes.ts`). `index.ts` is also edited by other tasks; keep the new export block self-contained to ease merging.

## Backend contract (verified, do not guess)

- Registration (`handlers/auth/register.go:253-262`) emails a 6-digit code with `purpose: "verify_email"` (5-minute expiry; `emailcode.go`), and returns `email_verification_required: true`. There is no emailed link: verification is **code entry only**.
- `POST /v1/auth/email-code/request` body `{email, purpose}`; `purpose` defaults to `"login"`, so the page MUST send `"verify_email"`. Always `204` after ~200 ms (no enumeration signal).
- `POST /v1/auth/email-code/verify` body `{email, code, purpose}`: `purpose: "verify_email"` marks the email verified and returns **`204 No Content`** (no token); `purpose: "login"` returns `{token, user}` (that is `EmailCodePage`'s flow, unchanged). Wrong/expired code: `401 unauthenticated "invalid or expired code"`; missing fields: `400 invalid_input`.
- Both endpoints are public (no bearer needed), so the email must be known client-side. `GET /v1/self` is exempt from the verified-email gate (`api/cmd/server/main.go` ~line 563), so an authenticated user's `useAuth().user.email` is available on this page. `UserAccountSelf` has no `email_verified_at` field, so the page cannot detect "already verified" on its own (see Assumptions).
- The current client types lack `purpose` and type `verifyEmailCode` as returning `LoginResponse`; this task adds the missing pieces rather than reusing the login-typed call.

## Requirements

1. `gui/src/lib/api.ts`: add optional `purpose?: 'login' | 'verify_email'` to `EmailCodeRequest` and `EmailCodeVerifyRequest` (omitted keeps today's wire body, so `EmailCodePage` is unchanged), and add `api.auth.verifyEmail({ email, code }): Promise<void>` that POSTs `{email, code, purpose: 'verify_email'}` to `/v1/auth/email-code/verify` (204 handled by the existing `request()` path) with `skipAuthRedirect: true` (see Errors below). Use the lazy base URL and token seams from task 001 (no new config).
2. `gui/src/lib/routes.ts`: export `USERS_GUI_ROUTES` (`as const`) with the standard frontend paths every app mounts: `login: '/auth/login'`, `oidcReturn: '/auth/oidc/return'`, `forgotPassword: '/forgot-password'`, `resetPassword: '/reset-password'`, `emailCode: '/auth/email-code'`, `verifyEmail: '/verify-email'`, `oidcConfig: '/oidc-config'`. Add JSDoc marking which are backend-fixed (`resetPassword`, `verifyEmail`, `oidcConfig`, and `oidcReturn` via deploy env) and which are the library's recommended defaults. Export it from `index.ts` with its type. Where existing code hard-codes one of these literals in a non-test source (for example the `'/auth/login'` defaults introduced by tasks 001/002 once they land, and `ClientLayout`'s `CONFIG_PATH`, left to task 006), reference the constant only if doing so is a no-op; do not widen the diff otherwise.
3. `gui/src/components/verify-email-page.tsx` exporting `VerifyEmailPage` and `VerifyEmailPageProps` (file naming matches `email-code-page.tsx`; `'use client'` banner like siblings; uses core-gui `Card`/`Input`/`Label`/`Button` and the local `ErrorMessage`). Props, all optional:
   - `onVerified?: () => void`: called once after a successful verification (and after the user refresh below). The app owns the navigation (usually to its own sanitized `?return=` or home). No router imports.
   - `onNavigateToLogin?: () => void`: renders a "Sign in with a different account" text button (app-owned navigation; the page itself never signs the user out).
   - `email?: string`: the address to verify. Default: `useOptionalAuth()?.user?.email`. When neither is available the page first shows an email field (so a signed-out user who followed an emailed hint can still verify).
   - `message?: string`: the lead sentence. Default `"Verify your email address before continuing."` (the backend's own action-required text; the navigation path carries no message).
   - `resendCooldownSeconds?: number` (default `30`): client-side throttle on "Send a new code" (the API has no visible rate limit; this prevents accidental spam). `0` disables.
4. Behavior: show the message and the target email; a "Send a new code" button calling `api.auth.requestEmailCode({ email, purpose: 'verify_email' })` (success: polite status "A new code was sent to <email>. It expires in 5 minutes."; the 204 never reveals whether the account exists, so word it neutrally) and a code form (6 digits, same input rules as `EmailCodePage`: numeric, `maxLength` 6, strip non-digits) calling `api.auth.verifyEmail`. Do not auto-send a code on mount (registration already sent one). On success: call `refreshUser()` when an `AuthProvider` is present (swallow its errors; `AuthProvider` already handles action-required navigation), then `onVerified?.()`, and render a success state ("Email verified") in case the app delays navigation. Errors: `api.ts`'s `request()` turns EVERY `401` into `ApiRequestError('unauthenticated', 'Authentication required', 401)` and, unless `skipAuthRedirect` is set, clears the token and hard-redirects to the login URL. A wrong or expired code is a `401` from the backend, so both new calls (`verifyEmail`, and `requestEmailCode` when called from this page) MUST pass `skipAuthRedirect: true` (add the option to `api.auth.verifyEmail`; for the request call add an optional second `options` argument, default unchanged), and the page maps `status === 401` to the fixed message "That code is invalid or has expired. Request a new one." rather than showing the generic text. Other `ApiRequestError`s show their `message` through `ErrorMessage`; unexpected errors get the generic console-logged message used by siblings. (The existing `EmailCodePage` has the same latent 401 behavior; out of scope, filed as a followup.)
5. Accessibility: every input has an associated `Label`; the code input has `autoComplete="one-time-code"`, `inputMode="numeric"`; errors are announced (reuse `ErrorMessage`/`ErrorBanner`, confirm it exposes `role="alert"`); the "code sent" and "verified" confirmations are in an `aria-live="polite"` region; focus moves to the code input after a successful request; buttons expose busy/disabled state while submitting and during the resend cooldown (with remaining seconds in the label or an `aria-describedby` hint).
6. Works with and without an `AuthProvider` (use `useOptionalAuth`, not `useAuth`), honors `configureUsersApi` base URL and token key (via the shared client), and renders without `window` (no browser API during render; timers only in effects).
7. Export `VerifyEmailPage` and `VerifyEmailPageProps` from `gui/src/index.ts`. Add `gui/src/stories/VerifyEmailPage.stories.tsx` (default, no-session email field, error).
8. Tests (`bun test`, testing-library, happy-dom, style of `auth-context.test.tsx`/existing page tests; mock `fetch`, restore with `resetUsersApiConfig()` and the original `fetch` in `afterEach`; use fake timers or a prop-zeroed cooldown for the throttle): request-code posts `{email, purpose:'verify_email'}` to `<configured base>/v1/auth/email-code/request`; verify posts `{email, code, purpose:'verify_email'}` and a `204` calls `onVerified` once; wrong code (`401`) shows the fixed invalid/expired message, does not call `onVerified` and does NOT navigate/clear the token; email taken from the provider's user, from the `email` prop (prop wins), and from the fallback field; resend cooldown disables then re-enables; `configureUsersApi({ baseUrl: '' })` yields relative `/v1/...` URLs; renders inside and outside `AuthProvider`; an `axe`-style assertion is not required, but assert labels, `role="alert"` on error, and the live region. Add a `USERS_GUI_ROUTES` value test (exact strings) and a `verifyEmail` unit test in `api.test.ts` style.

## Validation

- `cd gui && bun run typecheck && bun test` pass; existing `EmailCodePage` tests unchanged and green (no wire-body change when `purpose` is omitted).
- `grep -rn "react-router\|next/" gui/src` finds nothing new.
- `grep -n "VerifyEmailPage\|USERS_GUI_ROUTES" gui/src/index.ts` shows both exports.
- `git diff --stat` touches only `gui/src/`.

## Metadata

architectural_impact: true

## Assumptions

- A signed-in unverified user is the primary case; the page cannot detect an already-verified account (no `email_verified_at` on `/v1/self`). A followup is filed to expose it; until then an already-verified user who lands here simply verifies again or follows their app's navigation.
- Users who are signed out (for example after clicking an emailed hint) use the email-field fallback.

## References

- [`../notes/embedding-and-routes.md`](../notes/embedding-and-routes.md), [`../overview.md`](../overview.md), [`../notes/seam-design.md`](../notes/seam-design.md)
- `api/internal/auth/require_verified.go`, `api/internal/handlers/auth/{register.go,emailcode.go,routes.go}` (read-only)
- `gui/src/components/email-code-page.tsx` (sibling; the login-by-code flow, not changed), `gui/src/lib/api.ts`, `gui/src/lib/auth-context.tsx`

## Checkpoint hints

- After the `api.ts` additions and `routes.ts` with tests
- After the component and its tests
- After stories and exports
