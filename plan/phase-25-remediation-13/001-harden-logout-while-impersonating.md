# Revoke the Administrator Session on Every Impersonated Logout and Guard Late State Commits

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses findings `H5Po`, `NfAS`, and `D3Q9`. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. When the tab is impersonating (session snapshot impersonating, or getStoredImpersonating() true) and the first session.logout succeeds, logout restores the administrator token through the retained administrator lineage (as impersonations.end does) and calls session.logout once more under it before clearing local state. If that extra attempt fails, logout logs it as local-only and still clears local state.
2. Logout awaits any in-flight restoreAdministrator (restoreRef) before its own restore/revoke, so the restore and logout share one refresh and do not race.
3. After logout begins, a late loadUser / token_response / restore commit cannot repopulate user, snapshot or token state (for example a signed-out or logout-generation guard checked before setUser and setSnapshot).
4. Existing behavior on the expired-impersonation-token (impersonation_ended) path and the non-impersonating path is unchanged.

## Validation

1. auth-context test: with a valid impersonation token and a valid administrator refresh token, logout issues POST /v1/session/logout under the impersonation token, then a refresh, then POST /v1/session/logout under the administrator token, then clears local state.
2. auth-context test: on the impersonation_ended path with a delayed restore and a delayed /v1/me response, user state is null after logout settles.
3. `cd gui && bun test` and `bun run typecheck` pass; `make build.gui` and `make lint.gui` pass.

## References

- Finding `H5Po` in this plan's `plan/findings.yaml`.
- Finding `NfAS` in this plan's `plan/findings.yaml`.
- Finding `D3Q9` in this plan's `plan/findings.yaml`.

## Status

- Outcome: succeeded (2026-10-31).
- Validation: auth-context tests (impersonating logout order, local-only fallback, late restore/user load guard) pass; `bun test` (453), `bun run typecheck`, `make build.gui`, `make lint.gui` pass.
- Files: `gui/src/lib/auth-context.tsx`, `gui/src/lib/auth-context.test.tsx`.
- Decisions: logout is single-flight; a logout-generation counter plus a logging-out flag drop late `loadUser`/hydration results and session events; the impersonating check uses stored state only when a restore was in flight (snapshot is stale then).
