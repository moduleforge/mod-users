# Stop Refresh-and-Replay of Impersonated Requests as the Administrator

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses finding 4EHb. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. A request sent with an impersonation access token that receives a non-revocation 401 is not refreshed and replayed with the administrator's token; the client ends the impersonation locally (restoring the administrator session), emits an event, and fails the request with a clear error.
2. ImpersonationBanner 'End' (DELETE /v1/impersonations/current) tolerates an already-expired impersonation token without replaying the DELETE under the administrator's token.

## Validation

1. api tests: with an impersonation token committed, a 401 on a write does not call the refresh grant and does not replay the request; the error surfaced to the caller identifies the ended impersonation.
2. impersonation-banner test: End with an expired impersonation token restores the administrator session without a replayed DELETE.
3. cd gui && bun test, bun run typecheck; make build.gui and make lint.gui pass.

## References

- Finding `4EHb` in this plan's `plan/findings.yaml`.

## Status

Outcome: succeeded (2026-10-31).

- A per-tab impersonation marker (`getStoredImpersonating` / `setStoredImpersonating` in `gui/src/lib/config.ts`) is set by `commitTokenResponse` and `session.get`, and cleared with the session.
- `execute` in `gui/src/lib/api.ts` captures at send time whether a request carried an impersonation token. On a non-revocation 401 it neither refreshes nor replays: it drops the dead token and marker (only when still the stored token), emits `impersonation_ended`, and throws `users.impersonation_ended` (401).
- `impersonations.end` treats that error as already ended (no replayed DELETE), then restores the administrator through the refresh grant.
- `gui/src/lib/auth-context.tsx` restores the administrator on the event and on a reload that hits the error.
- Tests: `gui/src/lib/api.test.ts`, `gui/src/components/impersonation-banner.test.tsx`.
- Validation: `bun test` (440 pass), `bun run typecheck`, `make build.gui`, `make lint.gui` all pass.
