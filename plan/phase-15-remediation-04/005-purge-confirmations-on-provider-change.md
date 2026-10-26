# Purge Policy Confirmations On Provider Change And Serialize Them With Policy Writes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `iFeg` (security, priority medium). `PutAuthnPolicy` purges a party's `profile_policy_confirmations` only when `credential_rule` changes, so a change of the required OIDC provider under the `provider` rule leaves stale confirmations in place. The prior-policy read is also unlocked, so a confirmation written concurrently with a rule change can survive the purge. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop. Task 008 reuses the per-party lock introduced here, so this task runs before task 008.

## Requirements

1. `PutAuthnPolicy` in `api/internal/service/party_policy.go` treats a change of either `credential_rule` or, under the `provider` rule, `required_oidc_provider_id` as a rule change, and purges the party's `profile_policy_confirmations` on that change. The comparison is made on the pair (`credential_rule`, `required_oidc_provider_id`).
2. `PutAuthnPolicy` reads the prior policy row `FOR UPDATE` so that the prior-policy read and the purge happen under the same lock.
3. A per-party lock primitive is introduced for the case where no policy row exists yet (for example a transaction-scoped advisory lock keyed on the party). `PutAuthnPolicy` takes it in that case, and it is available for reuse by task 008.
4. The invitation and collaborator confirmation writers (`checkInvitePolicyMismatch` and `recordPolicyConfirmation`, and the policy read in `holder_policy_mismatch`) read the party's policy row `FOR SHARE`, or take the same per-party lock in shared mode when no row exists, so that they serialize with `PutAuthnPolicy`.
5. A policy update that changes neither `credential_rule` nor the required provider leaves existing confirmations in place.

## Validation

1. An integration test: changing the required OIDC provider from provider A to provider B under the `provider` rule purges the party's existing `profile_policy_confirmations`.
2. An integration test: a policy update that changes neither the rule nor the required provider keeps existing confirmations.
3. A concurrency test: an invitation or collaborator add that records a confirmation under the old rule, run concurrently with a rule change, does not leave a confirmation that persists under the new rule.
4. `make build.api`, `make test.unit` and the integration suite pass.

## References

- Finding `iFeg` in this plan's `plan/findings.yaml` (type:security, priority:medium): `PutAuthnPolicy` in `api/internal/service/party_policy.go` drops confirmations only when `credential_rule` changes, not when the required provider changes, and the prior-policy read and the confirmation writers are not serialized with the rule change.
