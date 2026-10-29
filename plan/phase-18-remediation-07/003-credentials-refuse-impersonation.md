# Refuse Credential Administration And Personal-Party Creation While Impersonating

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the phase 8 gate findings ejk3, Mdgf and Vj21, recorded as findings in this plan's `plan/findings.yaml`. Together they cover an impersonating administrator being able to install persistent credentials (ejk3), the personal-party creation path authorizing through an impersonating context (Mdgf), and the self-service credential routes under `/v1/principals/{own_uuid}/credentials` bypassing the `/v1/me` last-credential check and `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (Vj21). The fix follows Decision H.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Sequencing: this is task 003 of four sequenced tasks (001 -> 002 -> 003 -> 004) that touch the same gate SQL and services. They must not run in parallel. It runs after task 002 (impersonated-actor-rules-apply) and before task 004 (impersonation-renew-recheck), which both touch `api/internal/service/impersonation.go`.

Scope is limited to fixing the linked findings. Anything beyond that is out of scope.

## Requirements

1. Order: runs after impersonated-actor-rules-apply and before impersonation-renew-recheck (both touch api/internal/service/impersonation.go).
2. Decision H: credentialsTarget (and so ListPrincipalCredentials, IssuePrincipalAPIKey, InstallPrincipalSSHKey, RevokePrincipalCredential) refuses an AuthContext with ImpersonatorPrincipalID != 0 with ErrImpersonationNotAllowed (users.impersonation_not_allowed, 403), matching selfWriter/writeCaller; keep the 401, 400, 403, 404/409 error order.
3. ImpersonationService.Start copies the parent session's auth_time into the child session instead of now().
4. EnsurePersonalParty refuses an impersonating context (ImpersonatorPrincipalID != 0) with ErrImpersonationNotAllowed.
5. Vj21: credentialsTarget refuses when the target principal is the caller's own principal (self-service stays on /v1/me with its last-credential check and AUTH_SELF_SERVICE_MAX_AUTH_AGE, decision U8); owner rotation of a self-owned service account (task 004 requirement 3) keeps working.
6. Tests: an impersonation session cannot list, install an SSH key, issue an API key or revoke a credential through /v1/principals/{uuid}/credentials*; the child session's auth_time equals the parent's; EnsurePersonalParty is refused while impersonating; a user cannot revoke its last credential through DELETE /v1/principals/{own_uuid}/credentials/{credential_uuid}.
7. Never run scripts/link-siblings.sh or make preflight in a worktree; set MODULEFORGE_SIBLINGS_DIR=/Users/zane/playground/.flow/siblings/mod-users and verify the sibling links afterwards. Do not edit mod-core or any sibling project.
8. The Go developer role (`plugins/flow/roles/developer-go.md`) applies to this task.

## Validation

1. make build, make test, make lint and cd model && make verify pass.
2. api/internal/service unit tests and the api/internal/integration suite (principal credentials, impersonation, personal party) pass against a throwaway Postgres.
3. Existing task 004 tests (operator, party admin, self-owned service account rotation) still pass.

## References

- Finding ejk3 in this plan's `plan/findings.yaml` (security, high): credentialsTarget uses adminCaller, which accepts an impersonating AuthContext, and Start sets the child's auth_time to now, so an impersonating administrator can install persistent credentials.
- Finding Mdgf in this plan's `plan/findings.yaml` (security, medium): EnsurePersonalParty authorizes through adminCaller, which accepts an impersonating context.
- Finding Vj21 in this plan's `plan/findings.yaml` (security, medium): the `/v1/principals/{own_uuid}/credentials` routes are a self-service path that bypasses the `/v1/me` last-credential check and `AUTH_SELF_SERVICE_MAX_AUTH_AGE`.
- [Decisions and resolutions](../notes/decisions-and-resolutions.md) — Decision H (credentials admin under impersonation; ejk3) and the Amendments (2026-10-27, phase 8 gate) section.

## Status

- Outcome: succeeded (2026-10-29).
- Changes: `api/internal/service/principal_credentials.go` (`credentialsTarget` refuses an impersonating context with `ErrImpersonationNotAllowed` before any lookup, and refuses the caller's own principal with 403 before the read check; 401/400/403/404/409 order kept), `api/internal/service/personal_party.go` (`EnsurePersonalParty` refuses an impersonating context), `api/internal/service/impersonation.go` (`Start` copies the parent's `auth_time`), `api/openapi.yaml` (docs).
- Tests: `api/internal/service/principal_credentials_test.go`, `api/internal/service/personal_party_test.go`, `api/internal/integration/principal_credentials_integration_test.go`, `api/internal/integration/impersonation_integration_test.go`, `api/internal/integration/personal_party_integration_test.go`.
- Validation: make build, make test, make lint and `make -C model verify` pass; unit and integration suites (authz, config, integration) pass against a throwaway postgres:16 container, including the task 004 owner-rotation and party-admin tests and `TestImpersonation_OperatorReachesMembersOfRestrictiveParties`.
- Decision: a service account addressing its own principal through the administration routes is also refused (same rule as a user).
