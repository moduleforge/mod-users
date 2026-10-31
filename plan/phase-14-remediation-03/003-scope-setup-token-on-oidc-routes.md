# Scope The Setup Token On OIDC Configuration Routes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `cvMO` (security, medium). The OIDC configuration routes accept the setup token from a cached hash and a cached bootstrapped flag, and keep accepting it after OIDC is confirmed while bootstrap is pending. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. On the OIDC configuration routes, the `X-Setup-Token` (header or body) is accepted only while OIDC is unconfirmed. Once OIDC is confirmed, those routes require an operator admin session, even while bootstrap is pending. `POST /v1/bootstrap` keeps accepting the token per decision U9.
2. `verifySetupToken` checks the database hash and the bootstrapped/confirmed state fresh (reuse `VerifySetupTokenFresh` or refresh before checking), so a replica with a stale cache does not accept a cleared token.
3. `api/openapi.yaml` and the `oidc_config.go` comments describe the narrowed rule.

## Validation

1. Handler tests: with OIDC confirmed and `operator_party_id` NULL, a valid setup token is refused (401) on the OIDC config routes and accepted on `POST /v1/bootstrap`.
2. A test where the DB hash is cleared after the cache was loaded: `verifySetupToken` rejects the old token.
3. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `cvMO` in this plan's `plan/findings.yaml` (security, medium): `verifySetupToken` in `api/internal/handlers/oidc_config.go` uses the cached hash and cached bootstrapped flag, and `tokenActiveLocked` keeps the token live on the OIDC routes while bootstrap is pending even after OIDC is confirmed.

## Status

Outcome: succeeded (2026-10-25). `verifySetupToken` in `api/internal/handlers/oidc_config.go` now refreshes state from the database (`RefreshState`) and accepts the token only while OIDC is unconfirmed; a refresh failure surfaces as 500. `POST /v1/bootstrap` is unchanged (`VerifySetupTokenFresh`). Comments in `oidc_config.go`, `oidc_providers.go` and descriptions in `api/openapi.yaml` describe the narrowed rule. Tests added to `api/internal/handlers/setup_token_lifecycle_test.go`.

Validation: handler tests pass; `make build.api` and `make test.unit` pass. `make test.integration` fails at the model step (no `users` role on local Postgres, environmental); `go test -tags=integration` for api packages passes but ran in ~0.2s, so it likely skipped without a database.
