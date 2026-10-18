# Identity Model Implementation: Mod-Users

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This is mod-users' share (participant 2 of 10) of the federated plan `identity-model-impl`, which
implements the accepted identity ADR (mod-core `docs/architecture/identity-model.md`). mod-core's
planning pass is complete; its plan notes are binding for the mod-core surface mod-users consumes.
mod-users owns the principal, credential, session and profile layer and the authorization engine's
consumer side.

### What must change in mod-users

1. **Principals instead of `user_accounts`:** the `user` principal subtype table (`person_id`,
   `managing_party_id`, `is_guest`, `default_profile_id`, `default_app_id`), consumption of mod-core's
   generic `service_account` and `system_actor` (the mod-users copies of `system_actors`, the
   `anonymous` seed and `entities_no_system_actor_owner` are deleted), identifiers, guests upgraded in
   place, managed users.
2. **Credentials:** the `credential_kinds` registry and per-kind credentials (password, email OTP,
   OIDC, SSH key, API key, anonymous device token) with DB-enforced kind rules per principal type,
   `auth_challenges`, and OIDC linking that never auto-links by email.
3. **Sessions and tokens:** the `sessions` row as the revocation unit with factors and assurance,
   a 1-hour EdDSA carrier token (`sid`, `prof`, `acr`, `amr`, `act`), per-request liveness,
   per-tab refresh lineages, profile landing and switching, step-up as factor composition, client
   credentials for machines, and the Bearer-only API contract (resolution (c)).
4. **Profiles and parties:** party profile lifecycle and invitations, party authn policies,
   `credential_party_authorizations`, `default_profile_id` and `confined_to_party_id` (confinement in
   v1), `acr`/`min_acr`/`max_auth_age` enforcement, outside-collaborator confirmations and continuous
   compliance (factor removal revokes sessions).
5. **Impersonation** as a separate session with reason, TTL and step-up, and a DB trigger forbidding
   nesting (fixing the nested-assume audit bug).
6. **Bootstrap:** setup-token bootstrap creating the operator party, first user, admin profile and
   wildcard `manage` grant (fixing the first-user `count(*)` race); personal team plans
   (resolution (b)) through an idempotent personal-party service.
7. **Authorization engine** (`api/internal/authz/authz.go`): the profile as the only subject, the
   per-operation own arm through `authz_operations.ownership_confers` (never `impersonate`, personal
   profiles only; with mod-core's generator and mod-authz's column it closes followup `rHzV`),
   owned-scope grants with `include_subtypes`, `AuthorizeTypeIn`, the `login` grant for
   `apps.invite_only`.
8. **GUI, docs, tests:** users-gui on the new contract, mod-users docs, and mod-users' slice of the
   final documentation drift scan.

### What must not change

- The HTTP error order (401, 400, 403, 404/409), masked lookups, and "internal ids never leave the
  process".
- `localAuthz.New(authzQ, opReg, pool)` and the `Authorize` / `AuthorizeType` signatures; the
  instance semantics of type-entity grants.
- The OIDC onboarding gate (`RequireOIDCConfirmed`) and provider merge behavior, apart from the new
  provider columns and the setup-token lifecycle of decision U9.
- Field encryption, the CORS default-deny policy and the `oidc_state` cookie path behavior.

### Hard constraints

- No production release: rewrite `0100_baseline.sql` in place, fold `0101` to `0104` into it; no
  incremental migrations, no backward compatibility, no data migration. Refactor size is not a
  constraint.
- Names and signatures fixed in mod-core's API surface note are used as written; this plan's
  [API surface](./notes/mod-users-api-surface.md) is binding for later participants.
- `created_by_profile_id` is never an authority input; liveness is never cached in v1.

### Success criteria

- Every DB invariant the ADR assigns to mod-users holds whatever the writer (MF101 to MF103,
  credential kinds, managed-user rules, no impersonation nesting), proven by integration tests.
- Sessions, liveness and revocation, credential gating and confinement, outside-collaborator
  compliance, impersonation and bootstrap are proven by service and HTTP tests; the engine's arms,
  assurance gate and `AuthorizeTypeIn` are proven by unit, integration and list-function parity
  tests.
- The contract in [the API surface](./notes/mod-users-api-surface.md) is implemented with the
  names it fixes; mod-users builds and its tests pass at the end of each phase (subject to the
  cross-project prerequisites each phase names).
- mod-users' docs describe the new model; its drift-scan slice is done; superseded followups are
  reconciled.

### Planning notes

- [The mod-users API surface](./notes/mod-users-api-surface.md): the binding contract for later
  participants (schema, SQL functions, SQLSTATE codes, token claims, Go packages, HTTP routes,
  configuration, manifest, frontend contract, requirements placed on mod-authz).
- [Decisions and resolutions](./notes/decisions-and-resolutions.md): how resolutions (a) to (d) are
  implemented, the person-record ownership choice (U1), decisions U2 to U20, the mod-users versus
  mod-authz boundary confirmation with flagged mismatches, cross-project ordering risks, and the
  mapping of ADR impact notes to phases.
- [Work packages](./notes/work-packages.md): files, tests, suggested task boundaries and tiers for
  the phase-decomposition agents, the drift-scan term list and the superseded-followup list.

## Current status

Planning. No phase has started. Phases 6 to 11 are registered; task breakdown is pending phase
decomposition. mod-core holds phases 1 to 5.

Execution order is **not** the global phase-number order:

1. mod-core phases 1 to 3 (and its doc phase 4) run first.
2. mod-authz's and mod-audit's `opctx.AuthContext` adoption in their `api/service` packages must
   merge before phase 6 closes (mod-users' dev server imports both).
3. Phases 6 (`users-identity-core`) and 7 (`parties-profiles-and-bootstrap`) run next.
4. **mod-authz's schema phase** (registered later, with a higher number) must run before phase 8
   (`users-authorization-engine`).
5. Phase 9 (GUI) follows phase 8; phase 10 (`doc-updates`) follows the code phases; phase 11
   (`users-identity-final`) runs after phase 10 and before mod-core's final coordination phase 5.

Pre-condition for cross-project work: sibling projects resolve `../../mod-core` and
`../../mod-authz` through `scripts/link-siblings.sh` symlinks to their main checkouts; tasks must
point them at the plan worktrees while the plan runs (see the coordination risks in
[decisions and resolutions](./notes/decisions-and-resolutions.md)).

The architectural-implications `doc-updates` phase is already registered as phase 10, before the
final phase. A re-invocation of the planning procedure must add its `update-architecture-docs` task
to that phase rather than registering a second, trailing `doc-updates` phase.

## Overview

| # | Phase | Summary | Depends on |
|---|---|---|---|
| 6 | [Users Identity Core](./phases/users-identity-core.md) | Schema rewrite into `mod_users`; users, identifiers, credentials, challenges, sessions, refresh lineages; EdDSA carrier and per-request liveness populating `AuthContext`; `/v1/auth`, `/v1/session`, `/v1/me`, `/v1/principals`; OIDC resolver without email auto-link; old surfaces removed; invariant and session tests. | mod-core 1 to 3; mod-authz and mod-audit `AuthContext` adoption |
| 7 | [Parties, Profiles and Bootstrap](./phases/parties-profiles-and-bootstrap.md) | Party profile lifecycle and invitations, managed users, party authn policies, credential authorizations and confinement, outside-collaborator confirmations and compliance, setup-token bootstrap, party-aware SSH profile derivation. | 6 |
| 8 | [Users Authorization Engine](./phases/users-authorization-engine.md) | `authz.go` engine (assurance gate, owned scope, per-operation own arm, `AuthorizeTypeIn`) with parity tests; impersonation sessions; `credentials` operation; invite-only app gate; `EnsurePersonalParty`. | 7; mod-authz schema phase |
| 9 | [Users GUI and Frontend Contract](./phases/users-gui-and-frontend-contract.md) | Per-tab tokens, refresh lineages, handoff, profile chrome, self-service panels, bootstrap and OIDC pages. | 8 |
| 10 | [Documentation Updates](./phases/doc-updates.md) | Architecture and spec documents; repository documents review. | 6 to 9 |
| 11 | [Users Identity Final](./phases/users-identity-final.md) | mod-users drift-scan slice; superseded-followup reconciliation. | 10; precedes mod-core phase 5 |

Within phase 6, the schema task comes first and the queries task second; the token/middleware task
precedes the credentials, sessions, OIDC and wiring tasks, which can then largely run in parallel;
the two test suites close the phase. Within phase 7, the profile, policy, bootstrap and SSH tasks are
parallel after the profile service exists; the collaborator task follows the policy task. Within
phase 8, the engine task comes first; impersonation, `credentials` and personal-party tasks are
parallel after it. Phase 9's client task precedes its UI tasks. Phase 10's two tasks are parallel.
Phase 11's scan precedes the reconciliation. The [work packages](./notes/work-packages.md) note gives
the suggested task boundaries.
