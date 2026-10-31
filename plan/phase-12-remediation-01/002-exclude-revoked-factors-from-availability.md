# Exclude Revoked Factors From Profile Availability And Pair Bulk Revocations With Session Revocation

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `WpsB` (security). `profile_availability` counts revoked credentials as factors, so a revoked factor can satisfy an `authorized` or `provider` rule. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-sql.md`

## Requirements

1. `mod_users.profile_availability` builds the factor array only from unrevoked credentials (join `mod_users.credentials` with `revoked_at IS NULL`), the same way `session_acr` does, edited in place in `0100_baseline.sql`.
2. `RevokeCredentialsOfPrincipal` and `RevokeCredentialsConfinedToParty` either revoke the sessions holding each revoked credential (`revoke_reason` `credential_revoked`) in the same statement or transaction, or are removed if they have no caller; sqlc output is regenerated.

## Validation

1. An integration test: a session whose only party-authorizing factor is revoked without its session being revoked reports `credential_not_authorized` (or `provider_required`) for the party profile.
2. `cd model && make verify` passes; `make build` and `make test` pass; the authz integration suite passes.

## References

- Finding `WpsB` in this plan's `plan/findings.yaml` (security, medium): `profile_availability` passes revoked credential ids to `profile_availability_for`, and the bulk revocation queries revoke without revoking sessions.

## Status

Succeeded (2026-10-25). `profile_availability` now counts only unrevoked credentials (edited in place in `model/migrations/sql/0100_baseline.sql`). The uncalled `RevokeCredentialsOfPrincipal` and `RevokeCredentialsConfinedToParty` queries were removed from `model/queries/credentials.sql` and sqlc regenerated (`model/db/`). Added `TestInteg_Availability_RevokedFactorDoesNotCount` in `api/internal/authz/availability_integration_test.go`. Validation: `make verify`, `make build`, `make test.unit` and the authz/config/integration suites pass.
