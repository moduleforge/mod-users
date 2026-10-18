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
  implemented, the person-record ownership choice (U1), decisions U2 to U20, the cross-plan
  reconciliation decisions U21 to U28 with one open question for the manager, the mod-users versus
  mod-authz boundary confirmation with flagged mismatches, cross-project ordering risks, and the
  mapping of ADR impact notes to phases.
- [Work packages](./notes/work-packages.md): files, tests, suggested task boundaries and tiers for
  the phase-decomposition agents, the drift-scan term list and the superseded-followup list.

## Current status

Planning. No phase has started. Phases 6 to 11 are registered; task breakdown is pending phase
decomposition. mod-core holds phases 1 to 5.

Pre-condition for cross-project work: sibling projects resolve `../../mod-core` and
`../../mod-authz` through `scripts/link-siblings.sh` symlinks to their main checkouts; tasks must
point them at the plan worktrees while the plan runs (see the coordination risks in
[decisions and resolutions](./notes/decisions-and-resolutions.md)).

The architectural-implications `doc-updates` phase is already registered as phase 10, before the
final phase. A re-invocation of the planning procedure must add its `update-architecture-docs` task
to that phase rather than registering a second, trailing `doc-updates` phase.

### Execution order

**Phase numbers do not reflect execution order across the federation.** Global blocks: mod-core
1 to 5, mod-users 6 to 11, mod-authz 12 to 16, mod-audit 20 to 22, mod-tokens 30 to 33,
mod-billing 40 to 43, mod-workflows 50 to 52, mod-tasks 60 to 62, mod-repos 70 to 73,
app-mfmanager 80 to 84. Each participant's own overview states its constraints. The table below
lists every constraint that touches a mod-users phase, with the participant's own constraint id.

| # | Constraint | Source |
|---|---|---|
| U-X1 | mod-users 6 starts only after mod-core 1 to 3 have merged. | mod-core overview |
| U-X2 | **mod-authz 12 (`authz-auth-context-adoption`) merges before mod-users 6 closes.** | mod-authz H2 |
| U-X3 | **mod-audit 20 (`audit-auth-context-adoption`) merges before mod-users 6 closes.** | mod-audit E2 |
| U-X4 | **mod-authz 13 (`authz-identity-schema`) merges before mod-users 6 closes**, and so before mod-users 7 and 8 (decision U27). | mod-authz H3 |
| U-X5 | **mod-authz 14 task 004 (collaborator gate hook) merges before mod-users 7 task 003 starts.** That task halts if the hook is absent (decision U26). | mod-authz H5 |
| U-X6 | mod-users 6 task 005 and 8 task 003, and every other emitting task, use mod-audit's op vocabulary ([API surface 10.1](./notes/mod-users-api-surface.md)). | mod-audit E4 |
| U-X7 | mod-billing 40 starts after mod-users 6 (and mod-authz 13). mod-billing 41 starts after mod-users 7 (including task 007, `AdmitInTx`) and 8, and mod-authz 14. | mod-billing E1, E2 |
| U-X8 | mod-tokens 30 starts after mod-users 6 (its task 004 uses `ReadPrincipalSummaries` from 6/007) and mod-billing's identity phases. mod-tokens 31 starts after mod-users 6 to 8 and mod-audit 20. | mod-tokens E1 to E3 |
| U-X9 | mod-repos 70 starts after mod-users 6 to 8 and mod-authz 12 to 14. Its phase 71 relies on mod-users 7 task 005. | mod-repos H1, H3 |
| U-X10 | app-mfmanager 80 starts after mod-users 6 to 8 (it uses `LinkOIDCInTx` from 6/004), mod-authz 12 to 14 and mod-audit 20. app-mfmanager 81 starts after mod-users 9. | app-mfmanager E1, E3 |
| U-X11 | mod-authz 16, mod-audit 22 and mod-workflows 52 start after mod-users 8. | mod-authz H7, mod-audit E6, mod-workflows H4 |
| U-X12 | mod-users 9 follows 8. 10 follows the code phases. 11 follows 10 and merges before mod-core 5, which runs last, after every participant's final phase. | this plan, mod-core overview |
| U-X13 | **The mod-core phase 1 task adding the shared `apiresp` action-required error (followup `Uure`) merges before mod-users 6 task 005 and 007 start.** Phases 6 and 8 return or wrap it for every `users.step_up_required` refusal (U8, U14); it replaces any mod-users-local rendering and lifts the earlier plain 409/500 limitation for step-up refusals raised inside other modules' services. | mod-core 1 (`Uure`) |

Recommended global interleaving (it satisfies every constraint above and those in the other
participants' overviews):

1. mod-core 1, 2, 3. mod-core 4 may run alongside what follows.
2. mod-authz 12 and mod-audit 20, in parallel. Both are small and unblock mod-users' build.
   mod-workflows 50 may start here; it needs only mod-core.
3. mod-authz 13, in parallel with the start of mod-users 6.
4. The rest of mod-users 6, with mod-authz 14 in parallel. mod-users 6 closes only once
   mod-authz 12 and 13 and mod-audit 20 have merged. mod-authz 14 task 004 merges before mod-users
   7 task 003.
5. mod-users 7, then mod-users 8. mod-billing 40 may run alongside them.
6. The downstream consumers run in parallel, each after its own prerequisites:
   - mod-billing 41, then mod-tokens 30 and 31;
   - mod-tasks 60 (soft: after mod-authz 14), then mod-repos 70 and 71 (mod-repos 70 tasks 003
     and 007 need mod-tasks' and mod-audit's schema phases);
   - app-mfmanager 80.

   mod-users 9 runs alongside them, and app-mfmanager 81 follows it.
7. Documentation and final phases: mod-users 10 and 11, mod-authz 15 and 16, mod-audit 21 and 22,
   and the other participants' doc and final phases.
8. mod-core 5 last.

## Overview

| # | Phase | Summary | Depends on |
|---|---|---|---|
| 6 | [Users Identity Core](./phases/users-identity-core.md) | Schema rewrite into `mod_users`; users, identifiers, credentials (including the trusted `LinkOIDCInTx`), challenges, sessions, refresh lineages; EdDSA carrier and per-request liveness populating `AuthContext`; `/v1/auth`, `/v1/session`, `/v1/me`, `/v1/principals`; `DirectoryService` and the transaction-scoped `ReadPrincipalSummaries`; OIDC resolver without email auto-link; old surfaces removed; invariant and session tests. | mod-core 1 to 3; before closing: mod-authz 12 and 13, mod-audit 20 |
| 7 | [Parties, Profiles and Bootstrap](./phases/parties-profiles-and-bootstrap.md) | Party profile lifecycle and invitations, managed users, trusted admission (`AdmitInTx`), party authn policies, credential authorizations and confinement, outside-collaborator confirmations and compliance, setup-token bootstrap, party-aware SSH profile derivation. | 6; mod-authz 14 task 004 before task 003 |
| 8 | [Users Authorization Engine](./phases/users-authorization-engine.md) | `authz.go` engine (assurance gate, owned scope, per-operation own arm, `AuthorizeTypeIn`) with parity tests; impersonation sessions; `credentials` operation; invite-only app gate; `EnsurePersonalParty`. | 7; mod-authz 13 (already required by 6) |
| 9 | [Users GUI and Frontend Contract](./phases/users-gui-and-frontend-contract.md) | Per-tab tokens, refresh lineages, handoff, profile chrome, self-service panels, bootstrap and OIDC pages. | 8 |
| 10 | [Documentation Updates](./phases/doc-updates.md) | Architecture and spec documents; repository documents review. | 6 to 9 |
| 11 | [Users Identity Final](./phases/users-identity-final.md) | mod-users drift-scan slice; superseded-followup reconciliation. | 10; precedes mod-core phase 5 |

Within phase 6, the schema task comes first and the queries task second; the token/middleware task
precedes the credentials, sessions, OIDC and wiring tasks, which can then largely run in parallel;
the two test suites close the phase. Within phase 7, the profile, policy, bootstrap and SSH tasks are
parallel after the profile service exists; the collaborator task (003) follows the policy task and
mod-authz 14 task 004. The trusted-admission task (007, listed last) depends only on task 001,
runs in parallel with 002 to 005, and merges before the closing integration task 006. Within
phase 8, the engine task comes first; impersonation, `credentials` and personal-party tasks are
parallel after it. Phase 9's client task precedes its UI tasks. Phase 10's two tasks are parallel.
Phase 11's scan precedes the reconciliation. The [work packages](./notes/work-packages.md) note gives
the suggested task boundaries.
