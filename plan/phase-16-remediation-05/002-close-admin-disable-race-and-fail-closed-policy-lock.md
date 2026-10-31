# Serialize Principal Disable With Last-Admin Guard And Fail Closed On Nil Policy Lock Tx

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `6dyh` (type:security, priority:medium) and `8bH2` (type:security, priority:medium).

`6dyh`: `disablePrincipalInTx` sets `disabled_at` and revokes sessions without taking the `platform_settings` row lock that `requireNotLastOperatorAdmin` uses, so a concurrent disable of one admin and suspension of another can both pass and leave zero effective operator administrators.

`8bH2`: `lockPartyPolicyShared` returns nil on a nil tx, a fail-open test accommodation in production code that silently skips the confirmation-vs-policy-change lock.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. In disablePrincipalInTx (api/internal/service/principal_admin.go), when the principal holds an active operator-party profile with the admin grant, take the same platform_settings row lock (lockPlatformSettingsForEndSQL) before setting disabled_at, honouring the documented lock order (platform_settings before principal/profile rows), so a concurrent disable and an ending/suspension of another admin serialize and cannot leave zero effective operator administrators.
2. Under that lock, refuse disabling the last effective operator administrator with the existing ErrLastOperatorAdmin / 409 users.last_operator_admin, consistent with the ending/suspension guard (or, if the plan's decisions forbid that refusal, document the race in the guard's boundary list instead and say so in the report).
3. Make lockPartyPolicyShared (api/internal/service/party_policy_lock.go) fail closed on a nil tx (return an error) instead of returning nil; move the test accommodation into a test-only seam (an injectable locker/fake) so unit tests needing no lock do not rely on a nil tx.

## Validation

1. Unit test: lockPartyPolicyShared with a nil tx returns an error; existing collaborator/invite/provisioning unit tests pass via the seam.
2. Integration test (throwaway Postgres 16): with two operator admins, disabling one while suspending/ending the other in concurrent transactions leaves at least one effective admin (the second operation is refused with 409).
3. make build.api, make test.unit, make lint pass.

## References

- Finding `6dyh` in this plan's plan/findings.yaml (type:security, priority:medium): disablePrincipalInTx (api/internal/service/principal_admin.go) does not take the platform_settings lock that the last-admin guard uses, so concurrent disable and suspension can leave zero effective operator administrators.
- Finding `8bH2` in this plan's plan/findings.yaml (type:security, priority:medium): lockPartyPolicyShared (api/internal/service/party_policy_lock.go) returns nil on a nil tx, silently skipping the confirmation-vs-policy-change lock in production code. The recommendation is fail-closed behavior with a test seam.

## Status

Outcome: succeeded (2026-10-26). `disablePrincipalInTx` now calls `requireNotLastOperatorAdminForDisable` (api/internal/service/profile_last_admin.go), which takes the platform_settings row lock when the principal holds an active operator-party profile and refuses with ErrLastOperatorAdmin when no other effective administrator remains. `lockPartyPolicyShared` fails closed on a nil tx; callers go through the `partyPolicySharedLocker` seam, which test code overrides (api/internal/service/party_policy_lock_seam_test.go). Validation: make build.api, make test.unit, make lint passed; the new integration test (leave_gate_integration_test.go) compiles and vets but the HTTP integration suite self-skipped here (docker inspect failed), so it was not observed to run.
