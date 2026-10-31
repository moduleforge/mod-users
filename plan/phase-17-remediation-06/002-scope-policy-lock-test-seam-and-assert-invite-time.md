# Scope The Policy Lock Test Seam Per Test And Assert InviteTime Mode

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `Eqyx` and `inY7` in this plan's `plan/findings.yaml`.

- `Eqyx`: a package-level `init()` in `api/internal/service/party_policy_lock_seam_test.go` overrides `partyPolicySharedLocker` for the whole `service` test package, so tests that exercise the real locking path cannot run against the production fail-closed locker and any test that sets the seam can race with others.
- `inY7`: the `HolderPolicyMismatch` fakes `gateUsersQ` and `mismatchOnlyUsersQ` in `collaborator_gate_test.go` do not capture their `HolderPolicyMismatchParams`, so nothing asserts that `CollaboratorGate.CheckOutsideCollaborator` passes `InviteTime=false` or that the invitation path passes `InviteTime=true`.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. Remove the package-level `init()` override of `partyPolicySharedLocker` in `api/internal/service/party_policy_lock_seam_test.go`. Tests that need a no-op lock get it through an injectable field on the services or a per-test helper that sets and restores the seam (`t.Cleanup`). Tests in package `service` that do not opt in run the fail-closed production locker.
2. Production behaviour is unchanged: the real locker remains the default, and `lockPartyPolicyShared` still fails closed on a nil tx.
3. Tests that use a set-and-restore helper on a package variable are not `t.Parallel`, or the seam is made per-instance so parallel tests cannot race.
4. The `HolderPolicyMismatch` fakes (`gateUsersQ`, `mismatchOnlyUsersQ` in `collaborator_gate_test.go`) capture their `HolderPolicyMismatchParams`.
5. Unit tests assert that `CollaboratorGate.CheckOutsideCollaborator` passes `InviteTime=false` and that `checkInvitePolicyMismatch` (the invitation path) passes `InviteTime=true`.

## Validation

1. `grep` finds no `init()` assigning `partyPolicySharedLocker` in `api/internal/service`.
2. The two new `InviteTime` assertions fail when either caller's mode argument is flipped (verify locally, then restore).
3. `TestLockPartyPolicyShared_NilTxFailsClosed` and all collaborator, invite and provisioning unit tests pass; `make test.unit` (with `-race` where the Makefile supports it), `make build.api` and `make lint` pass.

## References

- Finding `Eqyx` in this plan's `plan/findings.yaml`: the package-level `init()` in `party_policy_lock_seam_test.go` overrides `partyPolicySharedLocker` for every test in package `service`, so the seam is not scoped per test and parallel tests can race on it. Requirements 1 to 3 address it.
- Finding `inY7` in this plan's `plan/findings.yaml`: the `HolderPolicyMismatch` fakes in `collaborator_gate_test.go` do not capture their params, so the `InviteTime` mode each caller passes is untested. Requirements 4 and 5 address it.

## Status

Outcome: succeeded (2026-10-26).

- The package-level `partyPolicySharedLocker` variable and the test `init()` are gone. The seam is now a per-instance `policyLock` field (type `policySharedLocker`, nil means `lockPartyPolicyShared`) on `CollaboratorGate` and `ProfileService`; tests opt in with `noopPolicyLock`. Production default is unchanged and still fails closed on a nil tx.
- `gateUsersQ` captures `HolderPolicyMismatchParams` in `got`; new/extended tests assert `InviteTime=false` (gate) and `InviteTime=true` (invite path). Flipping either caller's argument made the matching test fail; both were restored.
- Validation: grep clean; `go test ./...` and `go test -race ./internal/service`, `make build.api`, `make lint.api` pass. Integration tests were not run (unit-test-only change).
- Files: api/internal/service/party_policy_lock.go, collaborator_gate.go, profile_service.go, party_policy_lock_seam_test.go, collaborator_gate_test.go, profile_admit_test.go, profile_service_test.go.
