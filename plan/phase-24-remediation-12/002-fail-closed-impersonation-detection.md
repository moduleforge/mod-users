# Make Impersonated-Request Detection Fail Closed

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses finding `vAo0`. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. A request sent with an impersonation access token is never refreshed and replayed as the administrator, even when the per-tab marker is absent. For example, derive impersonation from the carrier token's `act` claim (decoded, not verified, for this routing decision only), or treat the marker as authoritative only together with such a check.
2. The step-up replay path that stores a handler-supplied token (api.ts, the `setStoredToken(fresh)` call) leaves the impersonation marker consistent with the stored token.
3. The comment on getStoredImpersonating in config.ts describes the delivered fail-closed behavior.

## Validation

1. api test: with an impersonation token stored and no marker, a non-revocation 401 does not call the refresh grant and does not replay the request.
2. api test: a step-up handler returning a token during an impersonation leaves a later 401 unreplayed.
3. `cd gui && bun test`, `bun run typecheck`; `make build.gui` and `make lint.gui` pass.

## References

- Finding `vAo0` in this plan's `plan/findings.yaml`.

## Status

Outcome: succeeded (2026-10-31). `getStoredImpersonating` in `gui/src/lib/config.ts` now fails closed (marker OR unverified `act` claim on the stored token, via new `tokenActClaim`). The step-up replay in `gui/src/lib/api.ts` keeps the marker consistent with the stored token. Tests added in `gui/src/lib/api.test.ts`. `bun test` (449 pass), `bun run typecheck`, `make build.gui`, `make lint.gui` pass.
