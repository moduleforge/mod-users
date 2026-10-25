# Port Auth Pages to Session API

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Port the public sign-in pages of `@moduleforge/users-gui` to the new `/v1/auth` routes and the
token response of task 001: login, registration, email-code sign-in, password reset, email
verification, and the OIDC return page, which now exchanges a one-time `#handoff=` code instead of
reading a token from the redirect. Suggested role: `plugins/flow/roles/developer-react.md`. No
standard skill covers this task.

Depends on task 001 (`rebuild-api-client-and-auth-context`). Parallel-eligible with tasks 002, 004
and 005.

## Requirements

1. **`gui/src/components/oidc-callback-page.tsx`**: read `#handoff=<code>` or `#error=<code>` from
   the fragment, strip the fragment from the URL immediately, call the auth context's
   `completeHandoff(code)` exactly once (guarded against effect re-runs), then follow the
   `selection_required`/return-path flow. Remove the JWT-shape check and `completeExternalLogin`.
   Render readable messages for the OIDC redirect error codes `account_exists_sign_in_and_link`
   (explain signing in with the existing method and linking from account settings), `no_account`,
   `authentication_failed` and `missing_email`. `completeHandoff(code)` supplies the verifier stored
   by the task 001 client (finding `z443`); a missing or mismatched verifier shows a readable
   "sign-in must be completed in the same browser tab" message.
2. **`login-form.tsx`, `register-form.tsx`, `auth-page.tsx`**: use the new login and register
   calls; when the token response has `selection_required: true`, navigate to the selection route
   from task 001 instead of the return path; map `users.invalid_credentials`,
   `users.byo_identity_disabled`, `users.app_login_not_granted` and
   `users.profile_unavailable` to readable messages; keep the OIDC provider buttons on
   `GET /v1/auth/providers` and `GET /v1/auth/oidc/{provider}/start`. The OIDC provider buttons
   start the flow through the task 001 client helper that generates the verifier and passes the
   challenge on `GET /v1/auth/oidc/{provider}/start`. If the app is configured with
   an app slug, pass it as `app` on login.
3. **`email-code-page.tsx`**: request and verify through `/v1/auth/email-code/*`; establish the
   session through the auth context (replacing `setTokenAndUser`).
4. **`forgot-password-page.tsx`, `reset-password-page.tsx`**: point at
   `/v1/auth/password-reset/request` and `/confirm`; keep the backend-fixed reset route.
5. **`verify-email-page.tsx`**: keep the backend-fixed `users.email_unverified` landing; verify the
   address through the route the merged API provides (identifier verification under `/v1/me` for a
   signed-in user, or the email-code purpose the handlers still accept), coded against
   `api/openapi.yaml`.
6. **Guest sign-in**: if the GUI exposes a "continue as guest" entry, it calls
   `POST /v1/auth/anonymous`; otherwise no new UI is required here (guest upgrade lives in task 004).
7. **Return path**: `gui/src/lib/return-path.ts` behavior is preserved across the handoff (the
   return path survives the provider round trip as it does today).
8. **Tests and stories**: update `auth-page.test.tsx`, `login-return-path.test.tsx`,
   `credential-401.test.tsx`, `verify-email-page.test.tsx`, add an OIDC callback test (handoff
   success, single exchange, error codes, fragment stripped, verifier supplied, and missing-verifier
   error), and update the `LoginForm`,
   `RegisterForm`, `AuthPage`, `EmailCodePage`, `ForgotPasswordPage`, `ResetPasswordPage` and
   `VerifyEmailPage` stories. Update this task's component exports in `gui/src/index.ts` (own
   section only).
9. **`gui/README.md`**: update the sign-in page and OIDC return documentation (handoff, error codes,
   changed props).

## Validation

- `make -C gui test` passes for the updated and new tests.
- `make -C gui lint` reports no errors in the files this task owns (the pages above, their tests and
  stories).
- `make -C gui build` succeeds, or fails only in files owned by tasks 002, 004 or 005 (list them).
- `grep -rnE "completeExternalLogin|setTokenAndUser|looksLikeJwt|hashParams.get\('token'\)" gui/src`
  returns nothing.

## Metadata

architectural_impact: false

## Assumptions

- The sibling `../mod-core/gui` (bun workspace member, pointed at mod-core's plan worktree by
  `scripts/link-siblings.sh`) is built with `bun run build` before `make -C gui build`/`lint` (see
  `.claude/CLAUDE.md`).
- Task 001 is merged; use its client, auth-context API and route constants. If a needed value is
  missing, add it in a minimal, additive way and state it in the report.
- The merged `/v1/auth` handlers and `api/openapi.yaml` are authoritative for request and error
  shapes where they differ from the API surface note; record differences in the report.

## References

- [API surface section 7](../notes/mod-users-api-surface.md#7-http-api) — `/v1/auth` routes, token
  response, error codes, OIDC redirect error codes.
- [Decision U7](../notes/decisions-and-resolutions.md) — the `#handoff=` code.
- [Work packages, phase `users-gui-and-frontend-contract` item 3](../notes/work-packages.md).
- Finding `z443` and remediation task
  [phase-12-remediation-01/013-bind-handoff-code-to-verifier.md](../phase-12-remediation-01/013-bind-handoff-code-to-verifier.md)
  (server side) — handoff code bound to a client verifier.

## Checkpoint hints

- After the OIDC callback page and its test.
- After login, register and auth page.
- After email-code, password-reset and verify-email pages.
- After stories, exports and `gui/README.md`.
