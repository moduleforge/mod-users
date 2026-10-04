# Correct LoginForm onSuccess Return-Path Validation Doc

## Purpose and scope

Remediates the first part of finding uOkV: the `LoginForm` `onSuccess` documentation claims the returned path is already validated by `isSafeReturnPath`, but an explicit `returnPath` prop is echoed unvalidated (`returnPath ?? readReturnPath()`). Work targets the plan branch `plan/users-gui-integration-seams` and lands through the ordinary per-task loop. Scope is limited to `gui/src/components/login-form.tsx` comments. The second part of the finding (a `loginPath` prop for `ClientLayout`/`SidebarNav`) is deliberately out of scope for this plan.

## Requirements

1. The `LoginForm` `onSuccess` JSDoc states accurately that only the query-param fallback (`readReturnPath`) is validated by `isSafeReturnPath`, and that an explicit `returnPath` prop is passed through as supplied (the app owns its validation). This matches `AuthPage`'s `onAuthenticated` wording.
2. No behaviour change: the explicit-value-wins contract stays. `ClientLayout` and `SidebarNav` are not touched, because the plan overview puts them out of scope.

## Validation

1. Reading the `onSuccess` doc in `login-form.tsx` and the `onAuthenticated` doc in `auth-page.tsx` shows consistent, accurate statements against `handleSubmit`'s `returnPath ?? readReturnPath()`.
2. The gui typecheck and `bun test` still pass, and the diff touches comments only.

## References

- Finding uOkV in this plan's `plan/findings.yaml` ("LoginForm doc drift; ClientLayout loginPath").

## Status

Succeeded 2026-10-04. Rewrote the `onSuccess` JSDoc in `gui/src/components/login-form.tsx` (comments only): only the `readReturnPath` fallback is validated by `isSafeReturnPath`; an explicit `returnPath` is passed through unvalidated (app owns validation). Gui typecheck passes; `bun test` 180 pass, 0 fail. Note: `auth-page.tsx`'s `onAuthenticated` doc still says "Already validated by `isSafeReturnPath`" (out of scope here).
