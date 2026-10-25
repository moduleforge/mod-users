# Copy Party Authorizations When A Password Is Superseded

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `1xmT` (ambiguity, decided by owner: copy). When a password credential is superseded, its `credential_party_authorizations` rows are not carried to the replacement, so party profiles with credential rule `authorized` lose availability until re-authorization. This task must land before phase 7 task 004 (setup-token bootstrap). The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. Add a shared supersede helper in `CredentialService`. Within the same transaction that revokes the old credential and creates its replacement, the helper copies the old credential's `credential_party_authorizations` rows to the new credential.
2. Every password supersede path (password reset, change or replace password) uses the helper.
3. The helper's place in the sequence is stated here: this must land before phase 7 task 004 (setup-token bootstrap).

## Validation

1. An integration test shows that after a password reset, a party profile with credential rule `authorized` stays available without re-authorization.
2. A test shows the copy and the supersede are atomic: a failure rolls back both.
3. `make build`, `make test` and `make lint` pass in `api/`.

## References

- Finding `1xmT` in this plan's `plan/findings.yaml` (ambiguity): owner decided that party authorizations are copied when a password is superseded.

## Status

Outcome: succeeded (2026-10-25). Added `CredentialService.SupersedePasswordInTx` (revoke old as superseded, create replacement, copy party authorizations via new sqlc query `CopyCredentialPartyAuthorizations`) in `api/internal/service/credential_service.go`; password reset in `api/internal/handlers/auth/reset.go` now uses it (the only password supersede path; self add-password refuses a second password and is not a supersede). Query in `model/queries/party_policies.sql`, regenerated `model/db`. Tests in `api/internal/integration/supersede_integration_test.go`. Validation: make build, lint, test pass in `api/`; integration suites (authz, config, integration) pass against a throwaway Postgres.
