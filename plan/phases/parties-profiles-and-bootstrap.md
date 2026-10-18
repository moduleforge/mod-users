# Parties, Profiles and Bootstrap

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Goals

Deliver the party side of the identity model on top of the identity core: party profiles with
their lifecycle (invited, active, suspended, ended), invitations and acceptance, managed users,
party authn policies, credential party authorizations and confinement administration, the
outside-collaborator policy-mismatch confirmation and continuous compliance, the setup-token
bootstrap that creates the operator party, first user, admin profile and wildcard `manage` grant
race-free, and party-aware SSH profile derivation. It follows the identity core because it builds on
sessions, credentials and the availability function, and precedes the authorization-engine phase so
that phase can re-test these flows with owned-scope grants. Party administration authorizes through
`authz.AuthorizeTypeIn` from the start; until the engine phase, mod-core's helper falls back to
wildcard and type grants (U20).

## Inputs

- The `users-identity-core` phase's schema, services, availability function and routes.
- mod-core `profiles` (status transitions, MF004), `profile_policy_confirmations`,
  `platform_settings`, `core_claim_operator_party`, `core_is_operator_profile`.
- mod-authz phase 13's grants schema, merged before phase 6 closed (decision U27). Wildcard and
  type-entity grants go only to operator actors, so this phase's `AuthorizeTypeIn`-gated
  administration fixtures use operator-party administrators until the engine phase.
- mod-authz phase 14 task 004's `CollaboratorGate` hook (API surface section 9 items 6 and 9),
  merged before task 003 starts. Task 003 halts if it is absent (decision U26).
- mod-billing's request for a trusted admission, `ProfileService.AdmitInTx` (decision U22, task
  007).
- mod-audit's op vocabulary as mapped in API surface section 10.1 (decision U21).
- The ADR sections "Party profiles", "Credential gating and profile availability", "Platform
  administration and bootstrap", "Outside collaborators"; resolutions (a) and (d).
- [The API surface](../notes/mod-users-api-surface.md) sections 6.3, 7 and 9;
  [decisions](../notes/decisions-and-resolutions.md) U9, U10, U20;
  [work packages](../notes/work-packages.md) for this phase.
- Existing code: `api/internal/handlers/oidc_config.go`, `api/handlers/onboarding_boot.go`,
  `api/internal/auth/setup_token.go`.

## Outputs

- `ProfileService` (invite, accept, decline, leave, suspend, resume, end with departure cleanup,
  managed users, lifecycle observers, trusted `AdmitInTx`) and its routes under
  `/v1/parties/{uuid}`, `/v1/profiles`, `/v1/me/profiles`.
- `usersservice.CollaboratorGate` wired into mod-authz's hook in the dev server.
- `PartyPolicyService` (authn policy, credential authorizations, confinements) and routes.
- Outside-collaborator mismatch detection with recorded confirmations; `usersservice.CollaboratorGate`.
- `BootstrapService` and `/v1/bootstrap`; the setup-token lifecycle of U9; the bootstrap
  transaction of U10 with `core_claim_operator_party` as the single winner.
- `SSHKeyResolver.AuthContextForOwner` with the full party-derivation rules.
- Integration tests: the ADR's worked example end to end, invitation and status transitions,
  managed-user rules, confinement hiding the personal profile, collaborator compliance, departure,
  bootstrap concurrency and token lifecycle.
