# Make Identifier Removal Work With Nullable Credential FKs

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `aA5K` (bug, high). Removing an identifier fails while credentials are bound to it, because the credential foreign keys restrict deletion. Owner decision: no production release exists, so the baseline migration is edited in place. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-go.md`

## Requirements

1. In `model/migrations/sql/0100_baseline.sql`, make `cred_password.identifier_id` and `cred_email_otp.identifier_id` nullable with `ON DELETE SET NULL` in place of `ON DELETE RESTRICT`. Edit the baseline in place; add no new migration.
2. Add a CHECK so an active `email_otp` credential must keep its identifier (`identifier_id IS NOT NULL` when active).
3. Adjust any partial indexes, triggers, mirrors and sqlc queries that assumed these columns are non-null, and regenerate sqlc output.
4. `RemoveIdentifierInTx` (`api/internal/service/user_service.go`) revokes the credentials bound to the identifier first, so the CHECK is satisfied, then hard-deletes the identifier.
5. Keep the last-credential guard semantics.

## Validation

1. A real-database integration test removes a verified identifier that has a bound password and email_otp credential. The removal succeeds, the credentials are revoked, and their `identifier_id` is NULL.
2. A test shows the CHECK rejects an active email_otp credential with a NULL `identifier_id`.
3. `make build`, `make test` and `make lint` pass in `model/` and `api/`, and the integration suites pass.

## References

- Finding `aA5K` in this plan's `plan/findings.yaml` (bug, high): removing an identifier is blocked by RESTRICT foreign keys from `cred_password` and `cred_email_otp`.

## Status

Outcome: succeeded (2026-10-25). Validation: both new integration tests pass against a throwaway Postgres 16; `make build`, `make test` and `make lint` pass in `model/` and `api/`; the authz, config and integration suites pass.

- `cred_password.identifier_id` and `cred_email_otp.identifier_id` are nullable with `ON DELETE SET NULL`; `cred_email_otp` gains `cred_email_otp_active_needs_identifier` (`identifier_id IS NOT NULL OR NOT active`). Baseline edited in place, no new migration. The existing partial indexes and the subtable trigger already tolerated NULL.
- `GetActiveEmailOTPByIdentifier` and `CreateEmailOTPCredential` cast the identifier to `bigint` so the Go types stay `int64`; sqlc output regenerated.
- `RemoveIdentifierInTx` already revoked bound credentials before the delete; only its comments changed. `ErrIdentifierInUse` is kept for a restricting foreign key from outside this module.
- Tests: `api/internal/integration/credentials_integration_test.go`.
