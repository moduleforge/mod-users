# Make the Impersonation Marker Fail Closed for Undecodable Tokens and Handler-Supplied Step-Up Tokens

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses findings `moDM` and `m3Jr`. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. getStoredImpersonating treats a non-null stored access token whose payload does not decode to a JSON object (tokenActClaim null), with the marker absent, as impersonating for the refresh-and-replay routing decision (fail closed). An absent token still reads as not impersonating.
2. The step-up replay path in api.ts sets the marker to (tokenActClaim(fresh) === true || wasImpersonating). It never clears a previously true marker on a decodable token lacking act; only an authoritative signal (session.get, a token response, impersonations.end, sign-out) unmarks.
3. The getStoredImpersonating and tokenActClaim doc comments describe the delivered behavior exactly.
4. GUI test fixtures that exercise non-impersonating refresh-and-replay use JWT-shaped access tokens (for example a test helper), so existing behavior stays covered.

## Validation

1. api test: a stored undecodable token with no marker gets a non-revocation 401 that neither calls the refresh grant nor replays.
2. api test: with the marker set, a step-up handler returning a decodable token without act leaves the marker set and a later 401 unreplayed.
3. Existing api and auth-context tests pass with the updated fixtures; `cd gui && bun test` and `bun run typecheck`; `make build.gui` and `make lint.gui` pass.

## References

- Finding `moDM` in this plan's `plan/findings.yaml`.
- Finding `m3Jr` in this plan's `plan/findings.yaml`.

## Status

Outcome: succeeded (2026-10-31). `getStoredImpersonating` now fails closed for a present token whose payload does not decode to a JSON object; the step-up replay path in `gui/src/lib/api.ts` sets the marker to `tokenActClaim(fresh) === true || wasImpersonating` and never clears it. Doc comments in `gui/src/lib/config.ts` updated. Test fixtures migrated to JWT-shaped tokens via `gui/src/test-support/jwt.ts`. Validation: `bun test` (456 pass), `bun run typecheck`, `make build.gui`, `make lint.gui` all pass; new api tests cover both validation scenarios.
