# Fix Auth Context User Loading, Hydrate Failures and Logout Revocation

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses findings zwCX, oFfM, and A6aq. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. After login, register, email-code verify, handoff and sibling fork, RequireAuth never treats 'token present, user not yet loaded' as unauthenticated: either track a user-loading state that RequireAuth treats as loading, or resolve those calls and keep isLoading true until loadUser settles.
2. hydrate clears the stored session only for authentication failures (401-class, already handled by the request pipeline); on network_error (status 0) and 5xx it keeps the tokens and exposes a retryable error state.
3. logout revokes the server session when the access token has expired or is absent but a refresh token exists (refresh once before the logout call), or, if that is not possible, the code comment states that logout is then local-only; local state is cleared in all cases.

## Validation

1. auth-context tests: after a successful login with a delayed GET /v1/me, a RequireAuth child does not call onUnauthenticated before the user loads; same for a forked tab.
2. auth-context tests: hydrate with session.get failing with status 0 and with 503 keeps the stored refresh token; a 401 path still signs out.
3. auth-context test: logout with an expired access token issues a refresh then the logout request (or the comment is corrected, if that option is taken).
4. cd gui && bun test, bun run typecheck; make build.gui and make lint.gui pass.

## References

- Finding `zwCX` in this plan's `plan/findings.yaml`.
- Finding `oFfM` in this plan's `plan/findings.yaml`.
- Finding `A6aq` in this plan's `plan/findings.yaml`.

## Status

Outcome: succeeded (2026-10-31). Validation: gui `bun test` (426 pass), `bun run typecheck`, `make build.gui`, `make lint.gui` all pass.

- `loadUser` now raises a user-loading flag (counter-backed) in the same batch as the token commit; the context's `isLoading` folds it in, so RequireAuth waits instead of redirecting (login, register, email-code, handoff, fork).
- Hydrate keeps tokens on status 0 and 5xx and exposes `sessionError` plus `retrySession`; RequireAuth shows a retry notice and does not call onUnauthenticated for it. Other non-auth failures still clear the session.
- `logout` retries once after a 401 when a refresh token exists (`session.get` refreshes through the shared lineage, then logout again); if that fails the logout is local-only, documented in comments. Local state is always cleared.

Files: `gui/src/lib/auth-context.tsx`, `gui/src/components/require-auth.tsx`, `gui/src/lib/auth-context.test.tsx`.
