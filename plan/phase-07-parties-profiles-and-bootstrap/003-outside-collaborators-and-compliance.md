# Outside Collaborators and Continuous Compliance

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Implement the outside-collaborator rules of the ADR and resolution (a): policy-mismatch detection
with explicit, recorded confirmation on invite (filling the seam task 001 left in
`ProfileService.InviteInTx`), the `usersservice.CollaboratorGate` that mod-authz consults for
cross-party group members and grant actors, and verification that compliance is continuously
enforced.

Roles: `plugins/flow/roles/developer-go.md` (primary), `plugins/flow/roles/developer-sql.md`.

Depends on tasks 001 (invite seam) and 002 (policy administration and the availability helpers it
adds).

## Requirements

1. **Mismatch detection on invite.** Replace task 001's no-op seam. A mismatch exists when the
   inviting party's `party_authn_policies` cannot be met by the invited holder's current active
   credentials: under `authorized`, no holder credential is authorized for or confined to the
   party; under `provider`, the holder has no active OIDC credential from the required provider;
   in every case, no combination of the holder's active interactive credentials can reach the
   party's `min_acr` (use `credential_kinds.base_acr` and the `session_acr` composition rule of
   API surface section 3; reuse SQL rather than re-deriving the rule in Go). A policy of `any` with
   `min_acr = 1` never mismatches.
   - Mismatch and `ConfirmPolicyMismatch` false: 409
     `users.policy_mismatch_confirmation_required` with mismatch details (which rule fails, the
     required value, what the holder has) and nothing written.
   - Mismatch and confirmation true: create the profile and insert mod-core
     `profile_policy_confirmations(profile_id, party_id, confirmed_by_profile_id, confirmed_at)`
     (use mod-core's actual column names) in the same transaction.
   - Per resolution (a) the confirmation exempts the profile from that party's `credential_rule`
     only, never from `min_acr`; `profile_availability` already implements this from phase 6.
     Verify it with a test rather than changing it.
2. **`CollaboratorGate`** (binding signature, API surface 6.3):
   `CheckOutsideCollaborator(ctx, tx, owningPartyID, memberProfileID int64, confirmed bool) error`.
   When the member profile's party differs from `owningPartyID` (a personal profile counts as
   outside), apply the same mismatch check against the owning party's policy, using the member
   profile's holder credentials (for a personal profile, the holder's login context). On mismatch
   without confirmation, return an error mod-authz can map to 409
   `users.policy_mismatch_confirmation_required`; with confirmation, record
   `profile_policy_confirmations(memberProfileID, owningPartyID, <acting profile>)` idempotently.
   Same-party members pass without a write. Register `collaboratorGate` in
   `moduleforge.module.yaml` and the dev server.
   - If mod-authz's injectable hook (API surface section 9, item 6) exists in its plan worktree,
     wire the gate into it in `api/cmd/server/main.go`. If it does not, implement and test the
     mod-users side only and state in the report that wiring waits on mod-authz.
3. **Continuous compliance** (ADR "Outside collaborators"): verify, with tests, and fix if missing:
   - revoking a credential revokes every session holding it as a factor (phase 6
     `RevokeInTx`);
   - a party's `min_acr` is checked on every request through `session_liveness`, so raising a
     party's `min_acr` above a collaborator session's `acr` locks that profile on the next request
     (`acr_too_low`) even when a confirmation exists;
   - a collaborator whose credentials were downgraded (the authorizing factor revoked) is refused
     that party's profile at the next login (landing skips it as locked).
4. **Docs**: `AGENTS.md`/`README.md` rows for the collaborator gate; `api/openapi.yaml` documents
   the 409 body for the invite route.

## Validation

- `make build`, `make test`, `make lint` pass; integration tests pass.
- Tests exist and pass for: invite mismatch refused without confirmation (nothing written);
  confirmed invite records the confirmation; confirmation exempts `credential_rule` but not
  `min_acr`; `CollaboratorGate` same-party pass, outside-party refusal, confirmed record,
  idempotent re-confirmation; the three compliance behaviors of requirement 3.
- `grep -rn "checkInvitePolicyMismatch" api/internal/service` shows the seam is no longer a no-op.

## Metadata

architectural_impact: true

## Assumptions

- mod-core's `profile_policy_confirmations` table exists in its plan worktree; read its migration
  for the exact column names.
- mod-authz's `CollaboratorGate` hook may not have landed yet (phase inputs).

## References

- [API surface](../notes/mod-users-api-surface.md) sections 3, 6.3, 7, 9 item 6.
- [Decisions](../notes/decisions-and-resolutions.md) resolution (a).
- [Work packages](../notes/work-packages.md), phase `parties-profiles-and-bootstrap`, package 3.
- mod-core ADR `docs/architecture/identity-model.md`, "Outside collaborators".
