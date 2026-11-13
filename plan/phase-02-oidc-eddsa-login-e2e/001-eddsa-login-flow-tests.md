# EdDSA OIDC Login Flow Tests Including Postgres Integration

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Cover the OIDC browser login path with an EdDSA provider end to end: discovery, authorization URL with nonce, code exchange, ID token verification, claim mapping, and the HTTP callback and handoff against a real database. Test-only changes in `api/internal/auth` and `api/internal/integration`.

## Requirements

- Unit-level (no database): add an EdDSA variant of `TestOAuth_EndToEnd` in `oauth_test.go` (or a new file) using the Ed25519 issuer harness: provider with claim style matching the MFManager mapping (`generic`; confirm `email`/`email_verified`/`sub` map), token endpoint returning an `aud+jwt` EdDSA `id_token` that echoes the nonce. Assert success, the mapped identity, and nonce-mismatch rejection; add the case of a token with no `nonce` claim and record in the task report that this fails by design (nonce enforcement kept), as input to the consumer-side follow-on.
- Integration-level (`//go:build integration`, `api/internal/integration/`): the existing harness replaces `OAuth.ExchangeFn`; add a test that leaves `ExchangeFn` unset and builds the provider state through the real `initProvider` (or `NewOAuth` with the harness config) against the Ed25519 `httptest` issuer, with provider id `mfidp`, `jit_mode none`, and a user whose OIDC credential (issuer, sub) is pre-linked. Drive `/v1/auth/oidc/mfidp/start` and `/callback` then `/v1/auth/handoff`, asserting a session is issued for the pre-linked user, and that an unlinked (issuer, sub) gets the `no_account` outcome under `jit_mode none`. Add the negative case of an HS256-signed `id_token` from the same issuer being refused at the callback.
- Run with the AGENTS.md throwaway-Postgres recipe: container name `mod-users-integ-$(openssl rand -hex 4)`, `-p 127.0.0.1::5432`, `AUTHZ_DEV_PG_HOST/PORT`; never port 5433 or `users-module-postgres`; remove the container afterwards even on failure. Do not add scripts or Makefile targets.

## Validation

- Unit tests: `cd api && go test ./internal/auth/... -count=1`.
- Integration: the AGENTS.md recipe, `go test -tags=integration -p 1 -count=1 ./internal/integration/...` passes including the new tests; report the container name used and confirm removal.
- `make test.unit` passes; `docker ps` shows no leftover container from this task.

## Assumptions

Phase 1 is merged. If the integration harness cannot build a provider through `initProvider` without large refactoring, use `NewOAuth` with a config pointing at the httptest issuer and report the approach.

## Checkpoint hints

- After the unit-level end-to-end test.
- After the integration test scaffolding.

## Status

Outcome: succeeded (2026-11-13). Validation: unit tests for `./internal/auth/...`, the integration suite against a throwaway postgres:16 container (`mod-users-integ-955e4228`, removed afterwards), and `make test.unit` all pass; no leftover container.

- Added `api/internal/auth/oauth_eddsa_test.go`: EdDSA end-to-end login, nonce mismatch, and the generic-style mapping failure. The no-nonce case already exists as `TestOAuth_Exchange_EdDSAIDTokenWithoutNonce` (fails with `oauth: nonce mismatch` by design).
- Added `api/internal/integration/oidc_eddsa_integration_test.go`: real `/start`, `/callback`, `/v1/auth/handoff` with `ExchangeFn` unset, provider built via `OAuth.Rebuild` (which runs `initProvider`); covers linked sign-in, unlinked `no_account` under `jit_mode none`, nonce mismatch and HS256 refusal.
- Deviation: provider claim style is `google`, not `generic`, because `initProvider` gives the generic mapper no claim paths and it cannot map (filed as a finding).
