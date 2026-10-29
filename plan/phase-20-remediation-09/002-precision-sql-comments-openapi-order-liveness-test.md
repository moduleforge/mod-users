# Align SQL Exemption Comments, Credential Route Error Order And Decision L Liveness Assertion

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates findings `u1NG`, `lGXo` and `wRQn` recorded in this plan's `plan/findings.yaml`. It targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Scope is limited to comment wording in the baseline SQL migration, the stated refusal order in the OpenAPI principal-credential operations, and one tightened liveness assertion in an existing integration test. No behavior change is included.

## Requirements

1. Finding `u1NG`: in `model/migrations/sql/0100_baseline.sql`, reword the `profile_availability` header (around lines 1548-1553) and the `session_meets_party_policy` header (around lines 1688-1694) to state that the credential rule is skipped only for an impersonation started through an active operator profile, identified by the recorded `impersonator_profile_id` (see `session_policy_factors`). In the `profile_availability` header, also state that a non-operator impersonation's parent factors are held to the confinement check (`profile_gate_for`, Decision L). Comments only; no SQL change.
2. Finding `lGXo`: in `api/openapi.yaml`, correct the stated refusal order on the four principal-credential operations (`listPrincipalCredentials`, `issuePrincipalAPIKey`, the ssh-key install operation, and credential revoke) so that it matches `credentialsTarget`: 401, 400, 403 (impersonating), 404 (unknown principal), 403 (own principal), 404 (a principal the caller cannot read), 403 (credentials refused) or 409.
3. Finding `wRQn`: in `TestImpersonation_ConfinedParentFactorsBindNonOperators`, replace the liveness assertion that only checks for a non-200 status on the pre-confinement impersonation. Either assert the specific refusal (status and error code), or query `mod_users.profile_availability` for the child session id and `mY.profileID` and require `confined`. Also assert that the same probe returns 200 before `confineAll` runs.
4. Make no behavior change and do no work beyond these findings.

## Validation

1. `TestImpersonation_ConfinedParentFactorsBindNonOperators` passes with the tightened assertion.
2. `grep` finds no "operator administrator" or "administrator is a platform operator" wording in the two SQL headers, and finds the recorded `impersonator_profile_id` wording.
3. `grep` finds the corrected refusal order in all four operations in `api/openapi.yaml`.
4. `make build` and `make lint` pass. Task agents set `MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users`. Never run `scripts/link-siblings.sh` or bare `make preflight`. Integration tests pass after the comment edit.

## References

- Finding `u1NG` in this plan's `plan/findings.yaml` (SQL exemption header wording for impersonation).
- Finding `lGXo` in this plan's `plan/findings.yaml` (refusal order stated for principal-credential operations in `api/openapi.yaml`).
- Finding `wRQn` in this plan's `plan/findings.yaml` (liveness assertion in `TestImpersonation_ConfinedParentFactorsBindNonOperators`).

## Status

Outcome: succeeded (2026-10-29). Validation: target test, full authz/config/integration suites, `make build` and `make lint` all pass; SQL grep clean. Files: `model/migrations/sql/0100_baseline.sql`, `api/openapi.yaml`, `api/internal/integration/impersonation_integration_test.go`.
