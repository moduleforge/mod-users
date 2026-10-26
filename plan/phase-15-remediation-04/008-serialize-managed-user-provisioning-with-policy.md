# Serialize Managed User Provisioning With Authn Policy Writes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `vHBJ` (bug, priority low). `ProvisionManagedUserInTx` reads the party's authn policy without a lock, so a concurrent `PutAuthnPolicy` that switches the rule to `authorized` can commit after the read and leave the new managed user's password unauthorized. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop. This task runs after tasks 005 and 006: it reuses the per-party lock primitive introduced by task 005 and keeps the authority check added by task 006.

## Requirements

1. `ProvisionManagedUserInTx` in `api/internal/service/profile_service.go` reads the party's authn policy under a lock: `FOR SHARE` on the policy row when it exists, or the same per-party lock `PutAuthnPolicy` takes when no row exists. The read therefore serializes with a concurrent `PutAuthnPolicy`.
2. The lock is the primitive task 005 introduces; no new locking mechanism is added.
3. The auto-authorization decision is made from the locked read: the password is authorized only when the rule is `authorized` at that read, and the `update` authority check from task 006 still gates the authorization unchanged.

## Validation

1. A concurrency test: a `PutAuthnPolicy` switching the party to `authorized` races a managed-user provisioning. The outcome is either an authorized password (for a caller with `update` authority) or a provisioning that completes entirely before the switch; no committed `authorized` rule is left with an unauthorized password created after the read.
2. The task 006 integration test still passes.
3. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `vHBJ` in this plan's `plan/findings.yaml` (type:bug, priority:low): `ProvisionManagedUserInTx` in `api/internal/service/profile_service.go` reads the authn policy without a lock, so a concurrent switch to `authorized` can leave the new managed user's password unauthorized; the recommendation is to read the policy `FOR SHARE`.
