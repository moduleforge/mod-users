# Refuse Managed-User Creation And Credential Writes While Impersonating

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This remediation task fixes the finding YqJd, recorded in this plan's `plan/findings.yaml`. It covers managed-user creation under an impersonating context, which currently allows an impersonator to set a chosen password and write `credential_party_authorizations` rows.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Read the roles developer-go.md and developer.md before starting.
2. Decision K: ProvisionManagedUserInTx (api/internal/service/profile_service.go), and so ProfileService.CreateManagedUser (POST /v1/parties/{party_uuid}/managed-users), refuses an AuthContext with ImpersonatorPrincipalID != 0 with ErrImpersonationNotAllowed (403 users.impersonation_not_allowed). The refusal happens before any write and keeps the 401, 400, 403, 404/409 order.
3. Confirm that every credential and credential-authorization write path reachable from the HTTP API refuses an impersonating context. Add the same refusal to any path found missing it. Do not refactor paths that already refuse (credentialsTarget, party_policy writeCaller, selfWriter).
4. Add an integration test: an impersonation session cannot create a managed user, and no user, credential or credential_party_authorizations row is written.
5. Do NOT implement the general per-field impersonation access-class mechanism. It is tracked as project followup H8qi.
6. Integration tests must run, in the make -C model compose style flow: start a throwaway postgres:16 container with user 'users' as superuser, run the tests, then remove the container.
7. Set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users for every make target. Never run scripts/link-siblings.sh or bare 'make preflight'. Do not edit sibling projects.
8. No work beyond the linked finding.

## Validation

1. make build, make test and make lint pass.
2. The service unit tests and the integration suite pass against the throwaway postgres:16, including the new managed-user-under-impersonation refusal.
3. The report lists each credential and authorization write path checked and its refusal point.

## References

- Finding `YqJd` in this plan's `plan/findings.yaml` (`ProvisionManagedUserInTx`, reached from `CreateManagedUser`, accepts an impersonating context and lets an impersonator set a password and write credential party authorizations).

## Status

- **Outcome:** succeeded (2026-10-29).
- **Change:** `ProvisionManagedUserInTx` (`api/internal/service/profile_service.go`) refuses an `AuthContext` with `ImpersonatorPrincipalID != 0` with `ErrImpersonationNotAllowed` right after the 400 and 401 checks, before the authorizer and any read or write. `CreateManagedUser` (`api/internal/service/profile_admin.go`) refuses the same way after 401 and 400, before the party lookup (404) and the transaction, so the route order is 401, 400, 403, 404/409. `api/openapi.yaml` `createManagedUser` states the refusal.
- **Tests:** `TestManagedUserCreation_RefusesImpersonation` (`api/internal/service/profile_service_test.go`); `TestManagedUser_RefusedWhileImpersonating` (`api/internal/integration/managed_user_impersonation_integration_test.go`): an operator impersonating a party administrator who holds `update` on an `authorized`-rule party gets 403 `users.impersonation_not_allowed`, with no user, identifier, credential or `credential_party_authorizations` row written. A non-impersonated control call shows the same administrator can provision and authorize. Without the fix the integration test fails with 201.
- **Validation:** `make -C model compose/build/test.unit` and `make -C api build/test.unit/lint` passed, and so did `go vet -tags=integration`. The integration suites (`internal/authz`, `internal/config`, `internal/integration`) passed against a throwaway postgres:16, which was removed afterwards.
- **Write paths audited:** see the task report. The only unrefused credential/authorization writes left are the departure cleanup of an administrator's `PATCH /v1/profiles/{uuid}` to `ended`, which is raised to the manager and not changed here.
