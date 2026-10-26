# Serialize Credential Gate And Invitations With Profile Writers

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `oO0S` (bug, medium) and `5iFg` (bug, low). `oO0S` concerns the credential gate racing a concurrent profile ending; `5iFg` concerns a concurrent invitation and admission for the same holder. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. `gateCredentialWrite`, after taking the credential lock, reads the holder's open (non-ended) profile in the party with `SELECT ... FOR SHARE` (or `FOR UPDATE`) instead of the bare `EXISTS`, so it serializes with `EndInTx`/`departureCleanupInTx`, which lock the profile `FOR UPDATE`.
2. `InviteInTx` takes the same `GetUserByEntityIDForUpdate` lock on the holder as `AdmitInTx` before its duplicate check and insert, so concurrent invitation and admission for one holder and party serialize.
3. `AdmitInTx`'s doc comment states the guarantee accurately after the change.

## Validation

1. A concurrency test (two transactions on one pool): an ending committed while an authorize/confine passed its gate leaves no `credential_party_authorizations` row and no confinement for the departed holder.
2. A concurrency test: a concurrent `InviteInTx` and `AdmitInTx` for the same holder and party yield one profile and no `ErrProfileExists` from `AdmitInTx`.
3. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `oO0S` in this plan's `plan/findings.yaml` (bug, medium): `gateCredentialWrite` in `api/internal/service/party_policy.go` checks the held profile with a plain `SELECT EXISTS` and locks only the credential. A concurrent ending can leave an orphan authorization or an unclearable confinement.
- Finding `5iFg` in this plan's `plan/findings.yaml` (bug, low): `InviteInTx` in `api/internal/service/profile_service.go` takes no holder lock, so a concurrent invite and `AdmitInTx` for the same holder can make `AdmitInTx` return `ErrProfileExists` into the caller's transaction.
