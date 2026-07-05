# Forgot Password Page

## Purpose and scope

Add a new `ForgotPasswordPage` component to `gui/src/components/forgot-password-page.tsx`, generalizing `app-mfdemo/src/app/auth/forgot-password/page.tsx` (full source transcribed in `plan/overview.md`'s originating request) into a reusable, router-agnostic library component. Export it (component + props type) from `gui/src/index.ts`. Add a Ladle story at `gui/src/stories/ForgotPasswordPage.stories.tsx`. No task-procedure skill is prescribed; follow the existing component conventions directly (see References).

## Requirements

1. **Component shape.** `ForgotPasswordPage` is a page-level, self-contained component (owns its own `Card` from `@moduleforge/core-gui`, analogous to `AuthPage`/`OidcCallbackPage` — see `plan/overview.md`'s Key Decision 1). It owns two internal view states, exactly mirroring the reference page:
   - **Request view** (default): `CardTitle` "Forgot your password?", `CardDescription` "Enter your email and we will send you a reset link.", a form with a single `email` `Input` (`type="email"`, `autoComplete="email"`, `required`, placeholder `you@example.com`), an `ErrorMessage` (from `./error-message`) above the fields, and a submit `Button` reading "Send reset link" / "Sending..." while submitting.
   - **Submitted view**: rendered after a successful `api.auth.forgotPassword({ email })` call. `CardTitle` "Check your email", `CardDescription` echoing the submitted email in a `<span className="font-medium text-foreground">` exactly as the reference does ("If an account exists for **{email}**, you will receive a password reset link shortly.").
   - Both views render a `CardFooter` with a "Back to sign in" prompt.
2. **Props.**
   ```ts
   export interface ForgotPasswordPageProps {
     /** Called when the user clicks "Back to sign in". Defaults to a no-op. */
     onNavigateToLogin?: () => void;
   }
   ```
   No other props. This component does not call `useAuth()` — `api.auth.forgotPassword` is a public, unauthenticated endpoint (see `docs/mod-users-spec.md` use case 4 and `gui/src/lib/api.ts`'s `forgotPassword` binding). Do not import `next/navigation` or `next/link` anywhere in this file.
3. **"Back to sign in" rendering.** Render as a `<button type="button">` (not an anchor), styled identically to `AuthPage`'s existing mode-toggle footer buttons (`text-foreground hover:underline` for the primary action styling, or `text-sm text-muted-foreground hover:text-foreground` matching the reference page's link styling — match the reference page's plain-link visual treatment, since this is a "go elsewhere" prompt rather than a same-page mode toggle). `onClick` calls `onNavigateToLogin?.()`.
4. **Submission handling.** Mirror `LoginForm`/`RegisterForm`'s existing error-handling pattern exactly: catch `ApiRequestError` and surface `err.message` via `ErrorMessage`; on any other thrown error, `console.error('[forgot-password]', err)` and show the generic "Something went wrong. Check the browser console for details." message. Track `isSubmitting` and disable the submit button while true.
5. **Exports.** Add to `gui/src/index.ts`, following the existing block's ordering and shape:
   ```ts
   export { ForgotPasswordPage } from './components/forgot-password-page';
   export type { ForgotPasswordPageProps } from './components/forgot-password-page';
   ```
6. **Ladle story.** Add `gui/src/stories/ForgotPasswordPage.stories.tsx` with at least a `Default` story. This component does not need `useAuth()`, so no `AuthProvider` wrapper is required (unlike `LoginForm`/`RegisterForm`/`AuthPage`'s stories) — confirm this by checking the component does not import `useAuth`. Match the existing stories' file shape (a short top-of-file comment, `export const Default: Story = () => (...)`), wrapping in a `<div className="w-full max-w-sm p-6">` container matching `LoginForm.stories.tsx`'s container div (or omit the wrapper if the component's own `Card` already provides equivalent layout — check `AuthPage.stories.tsx`, which renders the component with no extra wrapper div since `AuthPage` already provides the `flex min-h-full items-center justify-center p-6` layout itself).

## Validation

- `cd gui && bun run typecheck` (or the project's equivalent lint/typecheck command per `AGENTS.md`) passes, modulo the known pre-existing `@moduleforge/core-gui` `.yalc` gap documented in `AGENTS.md`/`.claude/CLAUDE.md` (if `.yalc/` is not populated in this worktree, note that as a pre-existing condition, not a new failure).
- `make lint.gui` (or `cd gui && bun run lint`) passes.
- `gui/src/components/forgot-password-page.tsx` does not import `next/navigation` or `next/link` (`grep -n "next/navigation\|next/link" gui/src/components/forgot-password-page.tsx` returns nothing).
- `ForgotPasswordPage` and `ForgotPasswordPageProps` are exported from `gui/src/index.ts`.
- `gui/src/stories/ForgotPasswordPage.stories.tsx` exists and exports at least a `Default` story; `make preview` (Ladle) renders it without a console error at mount (submission failing with a `network_error` is expected and fine, matching the existing stories' documented behavior).
- Manual read-through confirms the request-view and submitted-view copy matches the reference page's text exactly (title/description wording), and that the "Back to sign in" affordance calls `onNavigateToLogin?.()` rather than performing navigation itself.

## Assumptions

- The current server-side password rule (12-character minimum, `api/internal/service/user_accounts.go` line 157) is irrelevant to this task — `ForgotPasswordPage` never collects a password.
- `api.auth.forgotPassword` and its `ForgotPasswordRequest` type already exist in `gui/src/lib/api.ts` and are already exported from `gui/src/index.ts` (confirmed present as of this plan's authoring); no API client changes are needed.

## References

- `gui/src/components/auth-page.tsx` — page-level component pattern (owns `Card`, internal state, router-agnostic callback props) to mirror.
- `gui/src/components/login-form.tsx`, `gui/src/components/register-form.tsx` — existing error-handling and submit-button conventions to mirror.
- `gui/src/components/error-message.tsx` — the `ErrorMessage` component used for surfacing errors.
- `gui/src/lib/api.ts` — `api.auth.forgotPassword` binding and `ForgotPasswordRequest` type.
- `gui/src/stories/AuthPage.stories.tsx`, `gui/src/stories/LoginForm.stories.tsx` — Ladle story conventions to mirror.
- `plan/overview.md` — Key Decisions 1-6, which this task implements.

## Status

- **Outcome:** succeeded
- **Date:** 2026-07-05
- **Summary:** Added `ForgotPasswordPage` to `gui/src/components/forgot-password-page.tsx`, mirroring `AuthPage`'s page-level pattern (owns its own `Card`, internal request/submitted view state). Exported the component and its props type from `gui/src/index.ts`. Added `gui/src/stories/ForgotPasswordPage.stories.tsx` with a `Default` story (no `AuthProvider` wrapper needed — the component never calls `useAuth()`). The "Back to sign in" affordance in both views is a `<button type="button">` calling `onNavigateToLogin?.()`, styled `text-sm text-muted-foreground hover:text-foreground` per the task's plain-link-treatment instruction.
- **Validation:**
  - `cd gui && bun run typecheck` — passed (the worktree's `.yalc/` gap was resolved for this session by copying `.yalc/` from the main checkout per `AGENTS.md`'s "Working in worktrees" step, then `yalc add @moduleforge/core-gui && bun install`; `gui/package.json`/`bun.lock` were reverted afterward since the main checkout does not commit the yalc-added dependency line either — see decision below).
  - `make lint.gui` — passed (same basis as above).
  - `grep -n "next/navigation\|next/link" gui/src/components/forgot-password-page.tsx` — no matches.
  - `ForgotPasswordPage`/`ForgotPasswordPageProps` confirmed exported from `gui/src/index.ts`.
  - `gui/src/stories/ForgotPasswordPage.stories.tsx` exists, exports `Default`; `cd gui && bun run preview:build` (Ladle static build, used as a proxy for `make preview` since no browser/headless tooling was available in this environment to observe console errors at mount) completed with 0 errors and bundled the new story.
  - Manual read-through: request/submitted view copy matches the task doc's specified text verbatim; "Back to sign in" calls `onNavigateToLogin?.()` only, no navigation performed by the component itself.
- **Assumptions applied:** the two documented in `## Assumptions` above (server-side password rule irrelevant; `api.auth.forgotPassword`/`ForgotPasswordRequest` already exist and needed no changes) both held as expected.
- **Decisions made:** used `yalc add`+`bun install` transiently to get a real typecheck/lint/build signal instead of only asserting the pre-existing gap, then reverted `gui/package.json` and `bun.lock` to their prior committed state (the main checkout's `gui/package.json` does not carry the yalc-added `"@moduleforge/core-gui": "file:.yalc/..."` dependency line either, so committing it here would diverge from the existing convention); the `.yalc/`-linked `node_modules` symlink remains in the worktree (gitignored) for any follow-up validation. The CardFooter's "Back to sign in" prompt is rendered as a standalone centered button with no surrounding lead sentence, since the task doc's Requirement 3 specifies only the button, its styling, and its `onClick`, without prescribing accompanying prose (the source `app-mfdemo` page's exact surrounding text was not available in this project's plan artifacts to copy verbatim).
