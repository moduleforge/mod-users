# Reset Password Page

## Purpose and scope

Add a new `ResetPasswordPage` component to `gui/src/components/reset-password-page.tsx`, generalizing `app-mfdemo/src/app/auth/reset/page.tsx` (full source transcribed in `plan/overview.md`'s originating request) into a reusable, router-agnostic library component. Export it (component + props type) from `gui/src/index.ts`. Add a Ladle story at `gui/src/stories/ResetPasswordPage.stories.tsx`. No task-procedure skill is prescribed; follow the existing component conventions directly (see References).

## Requirements

1. **Component shape.** `ResetPasswordPage` is a page-level, self-contained component (owns its own `Card`, analogous to `OidcCallbackPage` — see `plan/overview.md`'s Key Decision 1). Unlike the reference page, it has **no `Suspense` wrapper and does not call `useSearchParams`** — the reset token is a required prop supplied by the caller (Key Decision 4). Single view: `CardTitle` "Reset your password", `CardDescription` "Enter a new password for your account.", a form with:
   - `new-password` `Input` (`type="password"`, `autoComplete="new-password"`, `required`, `minLength={12}`), labeled "New password (min 12 chars)" exactly matching `RegisterForm`'s existing label markup pattern (muted, non-bold parenthetical).
   - `confirm-password` `Input` (`type="password"`, `autoComplete="new-password"`, `required`), labeled "Confirm new password".
   - An `ErrorMessage` above the fields.
   - A submit `Button` reading "Reset password" / "Resetting..." while submitting.
   - A `CardFooter` with a "Back to sign in" prompt.
2. **Props.**
   ```ts
   export interface ResetPasswordPageProps {
     /**
      * The password-reset token, e.g. read from the URL by the consuming
      * app's own page (via `useSearchParams` or equivalent) and passed in.
      * This component never reads the URL or a router itself.
      */
     token: string;
     /** Called after the password is successfully reset. Defaults to a no-op. */
     onSuccess?: () => void;
     /** Called when the user clicks "Back to sign in". Defaults to a no-op. */
     onNavigateToLogin?: () => void;
   }
   ```
   Do not import `next/navigation` or `next/link`, and do not call `useSearchParams`/`useRouter` anywhere in this file. This component does not call `useAuth()` — `api.auth.resetPassword` does not establish a session (see `docs/mod-users-spec.md` use case 4).
3. **Validation, in this order, mirroring the reference exactly:**
   - If `token` is falsy (empty string) at submit time, set the error to "Invalid or missing reset token. Please request a new reset link." and return without calling the API. (This is a defensive runtime check — a required TypeScript prop does not stop a caller from passing an empty string when its own URL is missing the query param.)
   - If `newPassword.length < 12`, set the error to "Password must be at least 12 characters." and return. **Confirm this figure against the live server rule** at `api/internal/service/user_accounts.go` (currently line 157: `if in.Password != nil && len(*in.Password) < 12`) rather than trusting this task document — if the server rule has changed since this task document was written, use the current value and note the discrepancy in your report.
   - If `newPassword !== confirmPassword`, set the error to "Passwords do not match." and return.
   - Otherwise call `api.auth.resetPassword({ token, new_password: newPassword })`; on success call `onSuccess?.()` (replacing the reference page's `router.push('/auth/login')`).
4. **Error handling.** Mirror `LoginForm`/`RegisterForm`'s existing pattern: catch `ApiRequestError` and surface `err.message`; otherwise `console.error('[reset-password]', err)` plus the generic fallback message. Track `isSubmitting` and disable the submit button while true.
5. **"Back to sign in" rendering.** Same treatment as `ForgotPasswordPage` (task 001 in this phase): a `<button type="button">` calling `onNavigateToLogin?.()`, not an anchor.
6. **Exports.** Add to `gui/src/index.ts`:
   ```ts
   export { ResetPasswordPage } from './components/reset-password-page';
   export type { ResetPasswordPageProps } from './components/reset-password-page';
   ```
7. **Ladle story.** Add `gui/src/stories/ResetPasswordPage.stories.tsx` with a `Default` story that supplies a placeholder `token` prop (e.g. `token="dummy-reset-token"`). No `AuthProvider` wrapper needed (component does not call `useAuth()`).

## Validation

- `cd gui && bun run typecheck` (or equivalent) passes, modulo the known pre-existing `.yalc` gap.
- `make lint.gui` passes.
- `grep -n "next/navigation\|next/link\|useSearchParams\|useRouter" gui/src/components/reset-password-page.tsx` returns nothing.
- `ResetPasswordPage` and `ResetPasswordPageProps` are exported from `gui/src/index.ts`.
- `gui/src/stories/ResetPasswordPage.stories.tsx` exists, supplies a `token` prop, and renders without a console error at mount in `make preview`.
- Manual read-through confirms the password-minimum-length figure used matches the live value in `api/internal/service/user_accounts.go` at the time of implementation (re-check it; do not assume 12 is still current without looking).
- Manual read-through confirms `onSuccess` fires only after a successful `resetPassword` call, and that no `router.push`/navigation call exists anywhere in the file.

## Assumptions

- `api.auth.resetPassword` and its `ResetPasswordRequest` type already exist in `gui/src/lib/api.ts` and are already exported from `gui/src/index.ts`; no API client changes are needed.
- The server-side minimum password length was 12 characters at plan-authoring time (confirmed via `grep -n password api/internal/service/user_accounts.go`); re-verify at implementation time per Requirement 3.

## References

- `gui/src/components/register-form.tsx` — existing password-length validation and label-markup pattern to mirror.
- `gui/src/components/oidc-callback-page.tsx` — page-level, single-view component pattern (owns `Card`, no internal step state, required-vs-optional callback prop distinction) to mirror structurally.
- `gui/src/lib/api.ts` — `api.auth.resetPassword` binding and `ResetPasswordRequest` type.
- `api/internal/service/user_accounts.go` — authoritative source for the current password-minimum-length rule (grep for `password` near line 157).
- `plan/overview.md` — Key Decisions 1-5, which this task implements.

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-05
- **Files:** `gui/src/components/reset-password-page.tsx` (new), `gui/src/stories/ResetPasswordPage.stories.tsx` (new), `gui/src/index.ts` (added `ResetPasswordPage`/`ResetPasswordPageProps` exports).
- **Validation summary:**
  - `cd gui && bun run typecheck` — passed (required temporarily linking `@moduleforge/core-gui` via `yalc add` per the documented `.yalc` gap; `gui/package.json`/`bun.lock` were reverted afterward to leave the dependency as the documented optional peer, unchanged from before this task).
  - `make lint.gui` — passed (this target is `tsc --noEmit`; the library has no eslint config).
  - `grep -n "next/navigation\|next/link\|useSearchParams\|useRouter" gui/src/components/reset-password-page.tsx` — returns nothing.
  - `ResetPasswordPage`/`ResetPasswordPageProps` exports confirmed in `gui/src/index.ts`.
  - `gui/src/stories/ResetPasswordPage.stories.tsx` exists with a `Default` story supplying `token="dummy-reset-token"`. Verified via a headless Playwright check against a locally-run `ladle serve` instance: the story renders at mount with zero console messages beyond Vite/React-DevTools boilerplate (no errors). Also exercised the mismatched-password path (shows "Passwords do not match."), the valid-submission path (logs the expected `[api] Network error` / "Could not reach the API server" `network_error`, matching the existing stories' documented no-live-API behavior), and the "Back to sign in" button click — no unexpected errors in any case.
  - Re-verified the server-side password-minimum-length rule at implementation time: `api/internal/service/user_accounts.go:157` (`if in.Password != nil && len(*in.Password) < 12`) and, more directly relevant to this endpoint, `api/internal/handlers/auth/reset.go:103` (`PasswordResetConfirm`'s `if len(req.Password) < 12`) — both still 12 characters, matching this task doc's figure. No discrepancy found.
  - Confirmed by read-through: `onSuccess` fires only after a successful `api.auth.resetPassword` call (inside the `try` block, immediately following the `await`), and no `router.push`/navigation call exists anywhere in the file.
- **Assumptions applied:** `api.auth.resetPassword`/`ResetPasswordRequest` already existed in `gui/src/lib/api.ts` and were already exported from `gui/src/index.ts` — confirmed true, no API client changes made.
- **Decisions made:** Added a "Remembered your password?" lead-in phrase before the "Back to sign in" button in the `CardFooter` (task doc did not specify exact prefix copy; followed `AuthPage`'s footer-prompt pattern of a short lead-in sentence before the action button). Used `api.auth.resetPassword` called directly from the module-level `api` singleton (not via `useAuth()`), consistent with this task's Requirement 2 and `auth-context.tsx`'s own use of the same singleton import pattern.
