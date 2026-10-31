# Keep Collaborator Policy Mismatch Strict Under Authorized Rule

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes finding `ggb7` (type:security, priority:high). The invite-time relaxation of the `authorized` credential rule in `mod_users.holder_policy_mismatch` also reaches `CollaboratorGate.CheckOutsideCollaborator`, which lets an outside collaborator holding only a personal password pass under an `authorized` party. A collaborator holds no profile in the owning party, so it can never acquire a `credential_party_authorizations` row.

This is the user's decision (2026-10-26, interactive): option 1, strict collaborator path. The invitation path keeps the relaxed check; the collaborator path reverts to the strict pre-change check. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

## Requirements

1. USER DECISION (2026-10-26, interactive): option 1, strict on the collaborator path. Follow the finding's recommendation verbatim.
2. Give mod_users.holder_policy_mismatch an invite-time mode parameter (e.g. p_invite_time BOOLEAN) in model/migrations/sql/0100_baseline.sql (rewritten in place, no new migration): true keeps the relaxed invite-time 'authorized' check; false restores the strict pre-change check requiring a credential_party_authorizations row.
3. The invitation path passes true (relaxed); CollaboratorGate.CheckOutsideCollaborator (api/internal/service/collaborator_gate.go) passes false, so collaborators under an 'authorized' party always need an explicit confirmation.
4. Update every policy_mismatch.go caller (detectPolicyMismatch and its users: collaborator gate, profile_service invite check, managed-user provisioning) and the sqlc query in model/queries, regenerating model/db with sqlc; ensure the credential_not_authorized message fits both modes.
5. Correct README.md:84 and model/README.md to describe the two modes of the shared gate.
6. Add one integration test showing a personal-password-only outside collaborator under an 'authorized' party is refused with 409 unless confirmed (and admitted once confirmed).
7. Amend the xhLc amendment note in plan/notes/decisions-and-resolutions.md to state collaborators are excluded from the invite-time relaxation.
8. Sequence after any other task in this round that edits 0100_baseline.sql (none expected) so edits do not conflict.

## Validation

1. make build.api and make test.unit pass; go vet -tags integration clean.
2. The new integration test and the existing collaborator_integration_test.go pass against a throwaway Postgres 16 with a users superuser (AUTHZ_DEV_PG_HOST/PORT); the existing invitation tests still pass (relaxed mode unchanged).
3. grep shows every holder_policy_mismatch call passes the mode argument, and the collaborator path passes false.

## References

- Finding `ggb7` in this plan's plan/findings.yaml (type:security, priority:high): CollaboratorGate.CheckOutsideCollaborator (api/internal/service/collaborator_gate.go) reaches the invite-time 'authorized' arm of holder_policy_mismatch, which admits a collaborator holding only a personal password. The recommendation is the strict collaborator path, which this task implements.

## Status

Outcome: succeeded (2026-10-26). `mod_users.holder_policy_mismatch` gained `p_invite_time BOOLEAN` (`model/migrations/sql/0100_baseline.sql`, rollback signature updated); `true` keeps the relaxed `authorized` check, `false` restores the strict `credential_party_authorizations`/confinement check. `detectPolicyMismatch` (`api/internal/service/policy_mismatch.go`) takes `inviteTime`; `profile_service.go` passes `true`, `collaborator_gate.go` passes `false`. sqlc query and `model/db` regenerated; `credential_not_authorized` message reworded to fit both modes; `README.md` and `model/README.md` describe both modes; the xhLc note in `plan/notes/decisions-and-resolutions.md` records the phase-16 task. New integration test `TestCollaborator_AuthorizedPartyStrictForOutsideCollaborator`. Validation: build.api, test.unit, `go vet -tags integration` passed; authz, config and integration suites passed against a throwaway Postgres 16; grep shows every call passes the mode argument.
