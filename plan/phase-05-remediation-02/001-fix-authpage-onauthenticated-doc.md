# Correct AuthPage onAuthenticated Return-Path Validation Doc

## Purpose and scope

Remediates finding HNPY: the `AuthPage` `onAuthenticated` JSDoc in `gui/src/components/auth-page.tsx` still says the returned path is "already validated by `isSafeReturnPath`", which is inaccurate when an explicit `returnPath` prop is supplied. The same inaccuracy was already corrected in `login-form.tsx` by an earlier remediation task. Work targets the plan branch `plan/users-gui-integration-seams` and lands through the ordinary per-task loop. Scope is limited to the comments in `gui/src/components/auth-page.tsx`.

## Requirements

1. The `AuthPage` `onAuthenticated` JSDoc says accurately that only the `?<unauthenticatedReturnParam>=` fallback (`readReturnPath`) is validated by `isSafeReturnPath`. It also says an explicit `returnPath` prop is passed through as supplied, and that the app owns validating it. The wording matches `LoginForm`'s corrected `onSuccess` doc.
2. Comments only. No behaviour change: the explicit-value-wins contract stays.

## Validation

1. Reading the `onAuthenticated` doc in `auth-page.tsx` next to the `onSuccess` doc in `login-form.tsx` shows the two are consistent with each other and with the `returnPath ?? readReturnPath()` resolution.
2. The diff touches comments only. `cd gui && bun run typecheck` and `bun test` still pass.

## References

- Finding HNPY in this plan's `plan/findings.yaml` (the `AuthPage` `onAuthenticated` doc).
