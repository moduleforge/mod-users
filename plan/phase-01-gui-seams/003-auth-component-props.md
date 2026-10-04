# AuthPage Registration Toggle, Forgot-Password Link, and Exported isSafeReturnPath

## Purpose and scope

Optional/likely seams from [`../notes/seam-design.md`](../notes/seam-design.md) (Components section): let an embedding app hide registration in `AuthPage`, expose a "Forgot password?" entry point so `ForgotPasswordPage` is reachable from the stock login UI, and export the existing return-path safety check. All additive and default-preserving. Depends on task `001` (it edits `login-form.tsx`, which 001 also touches). Scope: `gui/src/components/auth-page.tsx`, `login-form.tsx`, `oidc-callback-page.tsx`, new `gui/src/lib/return-path.ts`, `gui/src/index.ts`, component tests, stories. Parallel-eligible with `002` and `004`.

## Requirements

1. `AuthPage`: add `allowRegistration?: boolean` (default `true`). When `false`: do not render the "Create one" footer/button or the register panel (no `RegisterForm` mounted), and force login mode even when `initialMode="register"`. Update the JSDoc to state this is a UI affordance only (the API's register endpoint stays open).
2. `LoginForm` and `AuthPage`: add optional `onForgotPassword?: () => void`. When supplied, `LoginForm` renders a `type="button"` text-style "Forgot password?" control (styled like `AuthPage`'s existing inline text buttons) between the password field and the submit button; `AuthPage` forwards it. Absent: render nothing new (existing DOM unchanged).
3. Move `isSafeReturnPath` from `oidc-callback-page.tsx` to `gui/src/lib/return-path.ts` verbatim (same rules and comments), import it in `OidcCallbackPage` (behavior identical), and export it from `index.ts` with a JSDoc describing it for apps validating their own `?return=` values. Signature stays `(candidate: string | null) => candidate is string`. If task 002 already added an internal duplicate check, replace it with this import.
4. Document in `LoginFormProps.returnPath` JSDoc that it feeds only the OIDC `start` URL and that local-login navigation is the app's job via `onSuccess` (no behavior change).
5. Tests (`bun test`, testing-library, happy-dom; follow the style of `auth-context.test.tsx`; mock `fetch` for `fetchProviders` and wrap in `AuthProvider`): `allowRegistration` default shows "Create one" and toggles to the register panel; `allowRegistration={false}` shows no "Create one", has no register inputs/heading in the DOM, and `initialMode="register"` still renders login; the "Forgot password?" control is absent by default and calls the callback when present (and does not submit the form); `isSafeReturnPath` table test (accepts `/`, `/open/3`, `/a?b=c`; rejects `null`, `''`, `//evil.test`, `/\\evil`, `https://x`, `javascript:x`, `/a\tb`, `/x:y/z`). Add/adjust Ladle stories (`AuthPage.stories.tsx`, `LoginForm.stories.tsx`) for the new props.

## Validation

- `cd gui && bun run typecheck && bun test` pass; existing stories still compile (`bun run typecheck` covers `src/stories`).
- With none of the new props passed, rendered output of `AuthPage`/`LoginForm` is unchanged (assert the existing tests/DOM expectations, and a snapshot-style check that no "Forgot password?" text appears).
- `grep -n "isSafeReturnPath" gui/src` shows one definition (`lib/return-path.ts`), the import in `oidc-callback-page.tsx`, the export in `index.ts`, and tests.
- `git diff --stat` touches only `gui/src/` files.

## Metadata

architectural_impact: true

## References

- [`../notes/seam-design.md`](../notes/seam-design.md), [`../overview.md`](../overview.md)
- `gui/src/components/auth-page.tsx`, `login-form.tsx`, `oidc-callback-page.tsx`; app-mftodo `gui/src/routes/LoginPage.tsx` (documents the missing prop; read-only)

## Checkpoint hints

- After the `isSafeReturnPath` move and its tests
- After the `allowRegistration` and `onForgotPassword` props and tests
- After stories are updated

## Status

- Outcome: succeeded (2026-10-04).
- Validation: `bun run typecheck` and `bun test` (72 pass, 0 fail) green; `isSafeReturnPath` has one definition (`gui/src/lib/return-path.ts`), an import in `oidc-callback-page.tsx`, an export in `index.ts`, and tests; diff touches only `gui/src/`.
- Source: `gui/src/components/auth-page.tsx`, `gui/src/components/login-form.tsx`, `gui/src/components/oidc-callback-page.tsx`, `gui/src/lib/return-path.ts`, `gui/src/index.ts`, `gui/src/components/auth-page.test.tsx`, `gui/src/lib/return-path.test.ts`, `gui/src/stories/AuthPage.stories.tsx`, `gui/src/stories/LoginForm.stories.tsx`.
