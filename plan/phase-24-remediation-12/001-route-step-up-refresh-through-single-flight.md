# Route the OIDC Step-Up Refresh Through the Single-Flight Refresh

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses finding `6qD2`. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. The post-step-up token rotation in OidcCallbackPage goes through the same per-tab single-flight as refreshTabToken (for example an exported helper in api.ts that forces a rotation but joins any in-flight one), so it never runs concurrently with a hydrate-driven refresh of the same refresh token.
2. A rotation that hydrate already completed after the step-up is not repeated with the spent refresh token.
3. A failed refresh still reports through onError, as today. The linked=1 behavior is unchanged.

## Validation

1. oidc-callback-page test: with a concurrent session.get-triggered refresh in flight, stepped_up=1 issues exactly one POST /v1/auth/token and does not sign the tab out.
2. Existing linked/stepped_up tests still pass.
3. `cd gui && bun test`, `bun run typecheck`; `make build.gui` and `make lint.gui` pass.

## References

- Finding `6qD2` in this plan's `plan/findings.yaml`.

## Status

succeeded, 2026-10-31. Added `api.auth.refreshAfterStepUp(sentToken)` in `gui/src/lib/api.ts` (routes through `refreshTabToken` single-flight; no-op while impersonating) and switched `gui/src/components/oidc-callback-page.tsx` to it. New test in `gui/src/components/oidc-callback-page.test.tsx`. bun test (447 pass), typecheck, build.gui, lint.gui all pass.
