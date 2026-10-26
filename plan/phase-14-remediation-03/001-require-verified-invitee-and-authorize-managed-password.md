# Require Verified Email For Invitations And Authorize Managed-User Passwords

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `T1E6` (security, high), `4ZP1` (security, high) and `2mUw` (bug, medium). `T1E6` and `4ZP1` cover email invitations resolving to an unverified identifier; `2mUw` covers a managed user's only password credential being left unauthorized for an `authorized` party. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. When an invitation names the holder by email, `resolveInvitee` resolves only an email identifier with `verified_at IS NOT NULL` (a verified-only sqlc query in `model/queries`, regenerated, or a `VerifiedAt` check right after the lookup). An unverified match returns the same `apiresp.ErrNotFound` as an unknown address.
2. Invitation by `holder_user_uuid` is unchanged.
3. `ProvisionManagedUserInTx`, when the party's `credential_rule` is `authorized`, inserts a `credential_party_authorizations` row for the new password credential in the same transaction (as Bootstrap does) and emits the `create credential_party_authorization` observation.

## Validation

1. An integration test in `api/internal/integration/profiles_integration_test.go`: inviting an address held only by an unverified identifier returns 404 and creates no profile, and inviting the same address after verification succeeds.
2. An integration test: a managed user of an `authorized` party signs in with the provisioned password and lands on the party profile (available, not `credential_not_authorized`).
3. `make build.api`, `make test.unit` and the integration suite pass; `make lint` shows no new issues.

## References

- Finding `T1E6` in this plan's `plan/findings.yaml` (security, high): an email invitation resolves to whichever account first registered the address, verified or not, and that account can accept through `/v1/me`.
- Finding `4ZP1` in this plan's `plan/findings.yaml` (security, high): `resolveInvitee` in `api/internal/service/profile_service.go` looks up the holder email without checking `verified_at`. Same issue as `T1E6`.
- Finding `2mUw` in this plan's `plan/findings.yaml` (bug, medium): `ProvisionManagedUserInTx` creates the managed user's only credential without authorizing it for an `authorized` party, so the user is locked out from first sign-in.
