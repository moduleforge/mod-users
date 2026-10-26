# Treat Authorized Credential Rule As Satisfied At Invite Time

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `xhLc` and `t2OR`. At invite time, `mod_users.holder_policy_mismatch` evaluates the party's `credential_rule` `authorized` against `credential_party_authorizations` rows that cannot exist yet, because authorization requires a held profile. The check therefore reports a mismatch for every invitee, and the only way through is `confirm_policy_mismatch`, which writes a `profile_policy_confirmations` row that exempts the profile from the credential rule for good. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

This task records the binding user decision B:

- At invite time, `credential_rule` `authorized` is treated as satisfied when the holder has any usable interactive credential. Authorization follows acceptance: an administrator or the holder authorizes a credential once the profile exists. No `profile_policy_confirmations` row is written for this case.
- Members are therefore locked (`credential_not_authorized`) until a credential is authorized after acceptance. `api/openapi.yaml` and the docs must state that step.
- `t2OR` (settled consistently, adopting the recommended option): passing `confirm_policy_mismatch=true` on invite requires `update` on the party (else 403), and `PutAuthnPolicy` drops the party's `profile_policy_confirmations` rows when `credential_rule` changes. This amends resolution (a).

Ordering: this task MUST run after task 005 (`fix-policy-sql-null-handling`), which also edits `holder_policy_mismatch`; build on its NULL-handling result (`holder_acr` NULL when the holder has no usable credential). It MUST also run after task 007 (`restrict-admin-confinement-to-party-controlled-credentials`).

Files: `model/migrations/sql/0100_baseline.sql` (`mod_users.holder_policy_mismatch`, around lines 1307-1324, edited in place per plan policy), `api/internal/service/profile_service.go` (`InviteInTx`, `checkInvitePolicyMismatch`, around lines 525-561), `api/internal/service/party_policy.go` (`PutAuthnPolicy`), `api/openapi.yaml`, and integration tests.

## Requirements

1. **Invite-time `authorized` rule.** In `holder_policy_mismatch`, the `credential_rule = 'authorized'` branch returns no mismatch when the holder has any usable interactive credential (not revoked, not expired, `class = 'interactive'`, and not confined to a different party). It keeps `credential_not_authorized` only for a holder with no usable interactive credential at all, consistent with task 005's NULL `holder_acr`. The `provider` rule and the `min_acr` check are unchanged. Update the function comment.
2. **No confirmation row for this case.** An invitation that passes only because of requirement 1 writes no `profile_policy_confirmations` row.
3. **Confirmation authority.** In `InviteInTx` / `checkInvitePolicyMismatch`, `confirm_policy_mismatch=true` is honored only when the caller holds `update` on the party (`AuthorizeTypeIn`-style entity check against the party); otherwise respond 403. A request that does not need the confirmation is not affected.
4. **Drop confirmations on rule change.** `PutAuthnPolicy` deletes the party's `profile_policy_confirmations` rows in the same transaction when `credential_rule` changes (not when only `min_acr` or an unchanged rule is written). Observe the change with an op from the closed set in the API surface.
5. **Documentation.** `api/openapi.yaml` states, on the invite operation and the authn-policy operations:
   - under `credential_rule` `authorized`, an invitation is accepted when the holder has any usable interactive credential;
   - the member stays locked (`credential_not_authorized`) after acceptance until an administrator or the holder authorizes a credential;
   - `confirm_policy_mismatch` requires `update` on the party (403 otherwise);
   - changing `credential_rule` clears the party's policy confirmations.

   Update user-facing docs that describe invitation and policy mismatch to match.

## Validation

1. An integration test: an invited member of an `authorized` party, invited without confirmation, is gated by the rule after acceptance (`credential_not_authorized`) and is unlocked after a credential is authorized.
2. An integration test: inviting into an `authorized` party a holder with no usable interactive credential still yields `users.policy_mismatch_confirmation_required`.
3. An integration test: `confirm_policy_mismatch=true` from a caller without `update` on the party is refused with 403 and creates no profile.
4. An integration test: `PutAuthnPolicy` changing `credential_rule` removes the party's `profile_policy_confirmations` rows; a write that leaves the rule unchanged keeps them.
5. `make build.api`, `make test.unit` and the integration suite pass on a fresh DB (`make clean.data`).

## References

- Finding `xhLc` in this plan's `plan/findings.yaml`: `holder_policy_mismatch` evaluates the `authorized` rule at invite time against authorizations that cannot yet exist, forcing the permanent confirmation exemption.
- Finding `t2OR` in this plan's `plan/findings.yaml`: an invitation with `confirm_policy_mismatch` carries no authority check and the confirmation survives a later rule change.
- [Task 005](./005-fix-policy-sql-null-handling.md): also edits `holder_policy_mismatch`; this task must follow it.
- [Task 007](./007-restrict-admin-confinement-to-party-controlled-credentials.md): this task must follow it.
- [Decisions](../notes/decisions-and-resolutions.md#amendments-2026-10-26): amendment to resolution (a).
