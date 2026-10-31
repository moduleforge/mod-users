# Revoke the Administrator Session When Logging Out While Impersonating

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses finding `Bq75`. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. When logout's 401 retry fails with users.impersonation_ended, logout restores the administrator session once (a further session.get, which now refreshes through the administrator lineage) and calls session.logout again before clearing local state.
2. Local state is still cleared in every outcome, and a failure of the extra attempt is logged as local-only.

## Validation

1. auth-context test: with an expired impersonation token and a valid administrator refresh token, logout issues the refresh and a DELETE/logout request that succeeds under the administrator token, then clears local state.
2. `cd gui && bun test`, `bun run typecheck`; `make build.gui` and `make lint.gui` pass.

## References

- Finding `Bq75` in this plan's `plan/findings.yaml`.

## Status

- Outcome: succeeded (2026-10-31).
- Validation: new auth-context test passes; `cd gui && bun test` (450 pass), `bun run typecheck`, `make build.gui`, `make lint.gui` all pass.
- Files: `gui/src/lib/auth-context.tsx`, `gui/src/lib/auth-context.test.tsx`.
- Note: the logout request is `POST /v1/session/logout` (the task text says DELETE/logout); the test asserts the POST.
