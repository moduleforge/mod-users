# Credential-Failure 401s Show Inline Errors Instead of Redirecting

## Purpose and scope

Folds finding `ljyL` into this plan (principle: make everything standard unless there is a design reason not to). `request()` in `gui/src/lib/api.ts` turns every 401 into `ApiRequestError('unauthenticated', 'Authentication required', 401)` and, unless `skipAuthRedirect` is set, clears the token and navigates to the login URL. The backend also answers a credential failure with 401, so a wrong password on login or a wrong/expired code on email-code verify reloads the login page (or fires the configured `onUnauthenticated`) instead of showing the error. This is a real bug in components this plan already reworks (`request()`/handler in task `002`, login return handling in `007`, `EmailCodePage`, whose latent behavior the overview lists as an out-of-scope followup; this task supersedes that followup and the overview row below is updated).

Dependencies (the TODO tool has no dependency field): after `002` (it reworks the 401 branch and `skipAuthRedirect` semantics this task relies on; do not edit that branch concurrently) and after `005` (which adds `api.auth.verifyEmail` already passing `skipAuthRedirect`; reuse its pattern and tests). Run it near `007`: both edit `login-form.tsx`/`auth-page.tsx`, so run after `007` or rebase onto it; the conflict is small.

Verified against the backend handlers (`api/internal/handlers/auth/`, `api/internal/auth/middleware.go`), not guessed:

| Endpoint | 401 meaning | Handler |
|---|---|---|
| `POST /v1/auth/login` | wrong email or password ("invalid email or password", both unknown-user and bad-hash paths) | `login.go:62,73` |
| `POST /v1/auth/email-code/verify` | wrong, expired, or unknown-user code ("invalid or expired code") | `emailcode.go:140,154,164` |
| `POST /v1/auth/password-reset/confirm` | invalid or expired reset token ("invalid or expired reset token") | `reset.go:116` |
| `POST /v1/auth/email-code/request`, `password-reset/request`, `register` | never 401 (204/validation errors) | `emailcode.go`, `reset.go` |
| Authenticated routes behind the auth middleware (`/v1/self` etc.) | expired or invalid session token, missing header, deleted user (code `unauthorized`) | `middleware.go:80-87` |

So the credential-failure calls are `login`, `verifyEmailCode`, and `resetPassword` (the third was not in the finding but has the identical bug in `ResetPasswordPage`). The request-code calls need no change. Because `request()` replaces the body message with the fixed string "Authentication required", the inline text must come from the call sites, not from `err.message`.

Scope: `gui/src/lib/api.ts` (auth call options), `gui/src/components/{login-form,email-code-page,reset-password-page}.tsx`, tests. No backend change. Outside scope: the mount-time `GET /v1/self` behavior (already `skipAuthRedirect`; decision 6 stands) and the genuine-expired-session redirect, which stays as is.

## Requirements

1. In `api.auth`, pass `skipAuthRedirect: true` on `login`, `verifyEmailCode`, and `resetPassword` (inside the client, so every caller, including apps using `api.auth.*` directly, gets it). Leave `requestEmailCode`, `forgotPassword`, `register` unchanged (no 401 path). Add a one-line comment at each citing the handler behavior above. Confirm `api.auth.verifyEmail` (task `005`) does the same; fix there if not.
2. Inline errors at the call sites, catching `ApiRequestError` with `status === 401`:
   - `LoginForm` (login call): `'Invalid email or password.'`
   - `EmailCodePage` (verify call): `'Invalid or expired code.'`
   - `ResetPasswordPage` (confirm call): `'Invalid or expired reset link.'`
   Other errors keep the existing handling (`err.message`, generic fallback). The token is not touched and no navigation or `onUnauthenticated` call happens on these paths.
3. Do not change the 401 branch of `request()` itself, `ApiRequestError`, or the default redirect for any other call: a 401 on an authenticated call (for example `api.self.update`) still clears the token and redirects/invokes `onUnauthenticated` exactly as task `002` defines.
4. Tests (use the harness from `002`/`005`: stubbed `fetch`, happy-dom location spy):
   - Login with a 401 response: `LoginForm` shows the inline error; the stored token is unchanged (seed one and assert it survives); `window.location` unchanged; a configured `onUnauthenticated` spy not called.
   - Email-code verify with a 401: `EmailCodePage` shows the inline error; same three negative assertions. A 401 reset-password confirm: `ResetPasswordPage` likewise.
   - Regression guard: a 401 from an authenticated call without `skipAuthRedirect` (for example `api.self.update`) still clears the token and redirects (or calls `onUnauthenticated`).
   - `requestEmailCode` network/other errors still surface through the existing path (no change expected; one assertion).

## Validation

- `cd gui && bun run typecheck && bun test` pass.
- The new tests fail on the pre-change code (stash the `api.ts` edit to confirm) and pass after.
- `git diff --stat` touches only `gui/src/`.

## Metadata

architectural_impact: false

## References

- Finding `ljyL` (plan findings store); [`../overview.md`](../overview.md); tasks `002`, `005`, `007`
- `gui/src/lib/api.ts` (`request()`, `auth` client), `api/internal/handlers/auth/{login,emailcode,reset}.go`, `api/internal/auth/middleware.go`

## Checkpoint hints

- After the `api.ts` option changes and the regression guard test
- After the three component error paths and their tests

## Status

- Outcome: succeeded (2026-10-04).
- `api.auth.login`, `verifyEmailCode`, `resetPassword` now pass `skipAuthRedirect: true` (`verifyEmail` already did). Inline 401 errors added in `LoginForm`, `EmailCodePage`, `ResetPasswordPage`.
- Validation: `cd gui && bun run typecheck && bun test` pass (145 pass); the three credential tests fail with the `api.ts` edit stashed and pass after.
- Files: `gui/src/lib/api.ts`, `gui/src/components/{login-form,email-code-page,reset-password-page}.tsx`, `gui/src/components/credential-401.test.tsx`.
