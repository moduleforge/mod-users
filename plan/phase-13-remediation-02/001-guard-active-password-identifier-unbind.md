# Guard Active Passwords Against Identifier Unbinding

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope
This task addresses finding fDFi, a finding recorded in this plan's plan/findings.yaml. The work targets the plan branch plan/identity-model-impl and lands through the ordinary per-task loop. It is limited to preventing an active password from being unbound from its identifier while the credential is still in use.

## Requirements
1. In mod_users.cred_subtable_check() (0100_baseline.sql, rewritten in place), refuse an UPDATE on cred_password that changes identifier_id from non-NULL to NULL while the credential is not revoked. Raise with ERRCODE '23503' so an ON DELETE SET NULL cascade from user_identifiers onto an active password fails as a foreign-key violation. Confirm that the cascade's referencing-row UPDATE fires the trigger; if it does not, use an equivalent row-level trigger that does.
2. In UserService.removeIdentifier (or RemoveIdentifierInTx before it), lock the identifier row (SELECT ... FOR UPDATE via a new sqlc query in model/queries/identifiers.sql; regenerate model/db) before listing bound credentials, so a concurrent password reset or create for that identifier serializes behind the removal.
3. The existing foreign-key mapping in removeIdentifier keeps returning ErrIdentifierInUse (409 users.identifier_in_use) for the refused case; no 500 path remains for the active-unbound unique-index collision.
4. Revoked passwords bound to the identifier are still unbound (SET NULL) on removal, so normal removal of verified identifiers keeps returning 204.

## Validation
1. Integration test: deleting a user_identifiers row directly while an active cred_password is still bound to it fails with SQLSTATE 23503 and leaves the password bound and active.
2. Integration test: DELETE /v1/me/identifiers/{uuid} on a verified identifier with a bound password still returns 204, and the password row is revoked with identifier_id NULL.
3. Unit test or reviewed query: removeIdentifier acquires the FOR UPDATE lock before ListActivePasswordsByPrincipal.
4. make test.unit, the integration suites and make lint pass; sqlc generate leaves model/db consistent.

## References
- fDFi: a finding in this plan's plan/findings.yaml.

## Status
- Outcome: succeeded (2026-10-25).
- Trigger: `cred_subtable_check()` in `model/migrations/sql/0100_baseline.sql` now refuses a non-NULL to NULL `identifier_id` change on an unrevoked `cred_password` row (ERRCODE 23503). The `ON DELETE SET NULL` cascade fires the BEFORE UPDATE trigger (confirmed by the integration test).
- Lock: new sqlc query `LockIdentifierForUpdate` (`model/queries/identifiers.sql`, `model/db` regenerated), called at the start of `removeIdentifier` in `api/internal/service/user_service.go`, before `ListActivePasswordsByPrincipal`.
- Tests: `TestIdentifiers_DirectDeleteWhileActivePasswordBoundIsRefused` (integration), lock-before-list unit subtest in `TestRemoveIdentifierInTx`; the existing `TestIdentifiers_RemoveRevokesBoundCredentialsAndNullsTheirIdentifier` covers the 204 and revoked/NULL case.
- Validation: unit, lint, and integration suites (authz, config, integration) pass.
