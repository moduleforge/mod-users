# Require Party Update Before Auto-Authorizing A Managed User Password

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `1cWY` (security, priority low). Under an `authorized` party, `ProvisionManagedUserInTx` writes an authorization for the new password after checking only `create` on the party's profile type, while the dedicated authorize endpoint requires `update` on the party. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Under an `authorized` party, `ProvisionManagedUserInTx` in `api/internal/service/profile_service.go` writes the `credential_party_authorizations` row for the new password only when the caller holds `update` on the party, checked with the same authorization operation the administrator door uses (`s.az.Authorize(ctx, opSlugUpdate, &party)`).
2. When the caller lacks `update` on the party, the managed user is still provisioned, the auto-authorization is skipped, and the password stays unauthorized until an administrator authorizes it. The missing update authority is not reported as a provisioning failure.
3. The existing `create` authority check on the profile type remains the gate for provisioning itself and is unchanged.

## Validation

1. An integration test: a caller with `create` on party profiles but without `update` on the party provisions a managed user under an `authorized` party; the new password is left unauthorized, and sign-in with it is refused as `credential_not_authorized`.
2. The existing integration test in which a caller with `update` provisions a managed user under an `authorized` party and that user signs in still passes.
3. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `1cWY` in this plan's `plan/findings.yaml` (type:security, priority:low): `ProvisionManagedUserInTx` in `api/internal/service/profile_service.go` auto-authorizes a new password under an `authorized` party on `create` authority alone, so a principal delegated only profile creation can mint a credential that satisfies the party gate.

## Status

- Outcome: succeeded (2026-10-26).
- `ProvisionManagedUserInTx` now authorizes the new password under an `authorized` party only when the caller holds `update` on the party (`canAuthorizeForParty`, mirroring `isPartyAdmin`); a refusal skips the authorization without error, other errors propagate. Source: `api/internal/service/profile_service.go`.
- Test added: `TestAuthorizedManagedUser_AutoAuthorizationNeedsUpdateOnTheParty` in `api/internal/integration/authorized_invite_integration_test.go`.
- Validation: `make build.api`, `make test.unit`, `make lint.api` passed; integration suite (authz, config, integration packages) passed against a throwaway postgres:16 container, including the existing "an authorized party's managed user can sign in" subtest.
