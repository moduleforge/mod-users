# Email Code Page

## Purpose and scope

Add a new `EmailCodePage` component to `gui/src/components/email-code-page.tsx`, generalizing `app-mfdemo/src/app/auth/email-code/page.tsx` (full source transcribed in `plan/overview.md`'s originating request) into a reusable, router-agnostic library component. Export it (component + props type) from `gui/src/index.ts`. Add a Ladle story at `gui/src/stories/EmailCodePage.stories.tsx`. No task-procedure skill is prescribed; follow the existing component conventions directly (see References).

## Requirements

1. **Component shape.** `EmailCodePage` is a page-level, self-contained component (owns its own `Card`, analogous to `AuthPage` — see `plan/overview.md`'s Key Decision 1) with an internal `Step = 'request' | 'verify'` state, mirroring the reference page exactly:
   - **Request view** (`step === 'request'`, default): `CardTitle` "Sign in with email code", `CardDescription` "We will send a one-time code to your email address.", a form with an `email` `Input` (`type="email"`, `autoComplete="email"`, `required`, placeholder `you@example.com`), an `ErrorMessage`, and a submit `Button` reading "Send code" / "Sending...". A `CardFooter` with a "Sign in with password instead" prompt.
   - **Verify view** (`step === 'verify'`): `CardTitle` "Enter your code", `CardDescription` echoing the submitted email in a `<span className="font-medium text-foreground">` plus "It expires in 5 minutes." (matching the reference exactly), a form with a `code` `Input` (`type="text"`, `inputMode="numeric"`, `pattern="[0-9]{6}"`, `maxLength={6}`, `required`, `onChange` stripping non-digits via `e.target.value.replace(/\D/g, '')`, placeholder `000000`, `className="tracking-widest text-center text-lg"`), an `ErrorMessage`, and a submit `Button` reading "Verify code" / "Verifying...". A `CardFooter` with a "Try a different email" button that resets `step` to `'request'`, clears `code`, and clears the error — this is **internal same-component state reset**, not a navigation callback (matches `AuthPage`'s internal mode-toggle precedent; no prop needed for this action).
2. **Props.**
   ```ts
   export interface EmailCodePageProps {
     /** Called after successful code verification and sign-in. Defaults to a no-op. */
     onSuccess?: () => void;
     /** Called when the user clicks "Sign in with password instead". Defaults to a no-op. */
     onNavigateToLogin?: () => void;
   }
   ```
   This component **does** call `useAuth()` for `setTokenAndUser` (unlike `ForgotPasswordPage`/`ResetPasswordPage`), consistent with how `LoginForm` calls `useAuth().login()` directly — see `plan/overview.md`'s Key Decision 3 and the originating request's explicit instruction. Do not import `next/navigation` or `next/link`, and do not call `useRouter` anywhere in this file.
3. **Request-code submission.** Call `api.auth.requestEmailCode({ email })`; on success, set `step` to `'verify'`. On `ApiRequestError`, surface `err.message`; otherwise `console.error('[email-code]', err)` plus the generic fallback message (mirroring `LoginForm`/`RegisterForm`/reference page exactly).
4. **Verify-code submission.** Call `api.auth.verifyEmailCode({ email, code })`; on success, call `useAuth().setTokenAndUser(response.token, response.user)` then `onSuccess?.()` (replacing the reference page's `router.push('/profile')`). Same `ApiRequestError`/generic-fallback error handling, logged as `console.error('[email-code]', err)`.
5. **"Sign in with password instead" rendering.** Same treatment as tasks 001/002 in this phase: a `<button type="button">` calling `onNavigateToLogin?.()`, not an anchor. Rendered only in the request view's `CardFooter` (matching the reference).
6. **Exports.** Add to `gui/src/index.ts`:
   ```ts
   export { EmailCodePage } from './components/email-code-page';
   export type { EmailCodePageProps } from './components/email-code-page';
   ```
7. **Ladle story.** Add `gui/src/stories/EmailCodePage.stories.tsx` with a `Default` story wrapped in `<AuthProvider>` (required — this component calls `useAuth()`), matching `LoginForm.stories.tsx`'s/`RegisterForm.stories.tsx`'s wrapper and top-of-file comment ("requires an `AuthProvider` in scope... submission will fail with a `network_error` in this story environment since there's no live API").

## Validation

- `cd gui && bun run typecheck` (or equivalent) passes, modulo the known pre-existing `.yalc` gap.
- `make lint.gui` passes.
- `grep -n "next/navigation\|next/link\|useRouter" gui/src/components/email-code-page.tsx` returns nothing.
- `EmailCodePage` and `EmailCodePageProps` are exported from `gui/src/index.ts`.
- `gui/src/stories/EmailCodePage.stories.tsx` exists, wraps in `<AuthProvider>`, and renders the request view without a console error at mount in `make preview`.
- Manual read-through confirms: (a) the code input strips non-digit characters on every keystroke; (b) `onSuccess` fires only after `setTokenAndUser` has been called following a successful `verifyEmailCode`; (c) "Try a different email" resets local state only and does not call `onNavigateToLogin`; (d) no `router.push` or other navigation call exists anywhere in the file.

## Assumptions

- `api.auth.requestEmailCode`, `api.auth.verifyEmailCode`, and their `EmailCodeRequest`/`EmailCodeVerifyRequest` types already exist in `gui/src/lib/api.ts` and are already exported from `gui/src/index.ts`; no API client changes are needed.
- `useAuth().setTokenAndUser` already exists in `gui/src/lib/auth-context.tsx` with the exact signature `(token: string, user: UserAccountSelf) => void` used by the reference page; no `auth-context.tsx` changes are needed.

## References

- `gui/src/components/auth-page.tsx` — internal multi-view/step-state page-level component pattern to mirror.
- `gui/src/components/login-form.tsx` — `useAuth()` usage pattern (calling context methods directly rather than injecting them as props) and error-handling convention to mirror.
- `gui/src/lib/auth-context.tsx` — `useAuth()` and `setTokenAndUser` signature.
- `gui/src/lib/api.ts` — `api.auth.requestEmailCode`/`verifyEmailCode` bindings and their request/response types.
- `gui/src/stories/LoginForm.stories.tsx` — `AuthProvider`-wrapped Ladle story convention to mirror.
- `plan/overview.md` — Key Decisions 1-3, 6, which this task implements.

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-05
- **Validation summary:**
  - `cd gui && bun run typecheck` — passed (clean, no errors).
  - `make lint.gui` (tsc-only; no eslint config for this library) — passed.
  - `grep -n "next/navigation\|next/link\|useRouter" gui/src/components/email-code-page.tsx` — no matches, as required.
  - `EmailCodePage` / `EmailCodePageProps` exported from `gui/src/index.ts` — confirmed.
  - `gui/src/stories/EmailCodePage.stories.tsx` created, wraps `<AuthProvider>`; `bun run preview:build` (ladle production build) succeeded and bundled the story with no build errors. A live-browser/console-error check at mount was not performed (no headless-browser tooling available in this environment); relied on the production build succeeding plus the manual read-through below instead.
  - Manual read-through confirmed: (a) code `onChange` strips non-digits via `e.target.value.replace(/\D/g, '')` on every keystroke; (b) `onSuccess?.()` is called only after `setTokenAndUser(response.token, response.user)` following a successful `verifyEmailCode`, both inside the same `try` block so a thrown error skips both; (c) "Try a different email" (`handleTryDifferentEmail`) only resets `step`/`code`/`error` local state and does not call `onNavigateToLogin`; (d) no `router.push` or other navigation call exists anywhere in the file (only `onSuccess?.()`/`onNavigateToLogin?.()` prop invocations).
- **Files touched (repo-relative, inside implementation worktree):**
  - `gui/src/components/email-code-page.tsx` (new)
  - `gui/src/stories/EmailCodePage.stories.tsx` (new)
  - `gui/src/index.ts` (added `EmailCodePage`/`EmailCodePageProps` export lines)
- **Notes:** A fresh `bun install` plus `yalc add @moduleforge/core-gui` (needed locally to resolve `@moduleforge/core-gui` for typecheck/build, per this repo's known yalc gotcha) re-added a `file:.yalc/@moduleforge/core-gui` entry to `gui/package.json`/`bun.lock`. That reverts an earlier intentional commit (`fa0923f`) that dropped the direct dependency in favor of the optional-peer-only declaration, so those two files were reverted back to their committed state (`git checkout -- gui/package.json bun.lock`) before finalizing; only the three files above are part of this task's commit.
