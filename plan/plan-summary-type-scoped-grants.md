# Plan Summary: type-scoped-grants

## What was planned and why

This plan delivers mod-users' part of the federated `type-scoped-grants` change across mod-core, mod-authz and mod-users. The change lets grants target entity **types** ("may create/list resources of type X") as well as the wildcard (NULL target). It resolves followup `qWTH` in mod-authz.

mod-core makes every type an entity (`types.entity_id`). mod-authz keeps target groups kind-separated and guards who may grant. mod-users owns the production `Authorizer`, so its job is to make `AuthorizeType` honour grants on a type's entity.

In scope (mod-users only):

- **The `AuthorizeType` arm** in `api/internal/authz/authz.go`. The wildcard grant still satisfies. Otherwise the arm looks up the **exact** type's entity (`SELECT entity_id FROM types WHERE id = $typeID`), walks up through target groups (`TargetChain`) and checks for a grant over the actor chain. There is no ownership arm and no subtype inheritance (Q2), and every failure path fails closed.
- **Deprecated types**, handled consistently with mod-core: `AuthorizeType` does not consult `deprecated_at`. mod-core rejects the write.
- **Fixtures.** Rework the `ilu6` id-collision fixtures and the type-target integration tests that type entities invalidate. Every new target-group fixture respects mod-authz's type/instance kind-separation trigger.
- **Tests**, unit and integration, including a parity check against mod-authz's type-aware stand-in.
- **Docs**: Go doc comments, `docs/architecture.md`, `docs/mod-users-spec.md`.
- **Optional Q1, instance semantics**, in phase 8: a grant over a type entity also confers the operation over existing instances of that type. This is an extra arm in `checkGrantOrOwn`, with the required documented warning.
- **Phase 9, added after the main plan:**
  - the production-side list/single-row symmetry test for type-grant holders, once mod-core's Q1 list arm (mod-core phase 9) has landed;
  - **migrating `UserAccountService.List` to `authorizeType`** (followup `4dka`). The user decided to include it: a type-level `list` grant on `natural_person` then authorizes listing, and the fact that it exposes every account's email to its holder is documented as a warning.

Out of scope:

- **mod-core and mod-authz work**, planned in their own slices (mod-core phases 1-3 and 9, mod-authz phases 4-5 and 9-10).
- **The matching Q1 arms in mod-core's `GrantTableGenerator` and mod-authz's `integrationAuthorizer` stand-in**, planned as mod-core phase 9 and mod-authz phase 9. See [cross-project dependencies](#cross-project-dependencies).
- **Scoping the user-account search** per row or by holder type. The `List` decision accepts the exposure.
- **Q7, an "all authenticated users" grant.** Deferred by the user.
- **`docs/mf-standards/architecture/authorization-design.md`**, a submodule. docs-mf-standards followup `qjI3` tracks it.

The design, its rationale, the fixture-impact analysis and the Q1 design are in the [users type grant design note](./notes/users-type-grant-design.md). Every task document references it.

### Clarified instructions

What must change:

- **`AuthorizeType` honours type grants.** It allows when either holds:
  - the effective actor (sudo first), or an actor group in its chain, holds a wildcard grant whose operation is in `SatisfiedBy(op)`;
  - the actor chain holds a grant, with an operation in `SatisfiedBy(op)`, on the exact type's entity, directly or on a target group that reaches it walking up through `authz_target_group_members`.
- **It still never consults ownership**, never walks parent or child types, and never compares `typeID` with `entities.id`, `owner_id` or `grants.target_id`.
- **It fails closed:** no actor gives `ErrUnauthenticated`; `typeID <= 0` gives `ErrForbidden` even for a wildcard holder; an unknown `typeID` gives `ErrForbidden`; a DB error propagates.
- **Fixtures and tests reflect the new contract.** A grant on the type's own entity now allows. A grant on an unrelated entity whose id equals the type id (`ilu6`) is still denied, proved with a test-only type whose `types.id` differs from its type entity's id.
- **Docs** describe the type-grant arm instead of "wildcard-only".
- **Q1 (last phase):** entity-level `Authorize` also allows when the actor chain holds a grant on the target's exact fundamental type's entity (directly or through target groups). Type entities are never targets of this arm. The warning is documented.

What must not change:

- the semantics of wildcard grants (`target_id IS NULL`),
- entity-level `Authorize` in phases 6 and 7,
- the exported signatures of `Authorize`, `AuthorizeType`, `localAuthz.TypeAuthorizer` and `localAuthz.New`,
- `UserAccountService` call sites in phases 6-8, including the `authorizeType` helper's fail-closed nil-target fallback (phase 9 moves `List` onto that helper, and the fallback itself never changes),
- `model/`: no migration, query or generated-code change.

Success criteria:

- `make -C api build`, `make -C api test` and `make -C api lint` pass.
- The full `-tags=integration` `./internal/authz/...` suite passes against a throwaway Postgres composed from the type-entity mod-core and kind-separated mod-authz.
- New integration tests prove every row of the type-grant matrix in phase 6, task 002, the Q1 matrix in phase 8, task 001, the production type-grant symmetry in phase 9, task 001, and the `List` matrix in phase 9, task 002.

Hard constraints:

- Never pass a `types.id` as an `Authorize` target, and never compare `typeID` with an entity id.
- No new migration files. No sqlc changes.
- mod-users does not edit sibling repositories. Divergences found in mod-authz's stand-in are filed as findings.

### Phase 6: AuthorizeType type-grant arm (`phase-06-authorize-type-grant-arm`)

- `001-authorize-type-arm` (`sonnet-high`):
  - the type-grant arm, its dispatch and test hook, and the reworded doc comments in `api/internal/authz/authz.go`, `api/localAuthz/authz.go` and `api/internal/service/user_accounts.go`,
  - unit tests,
  - the rework of the existing type-target and user-account-create integration tests that the arm invalidates, so the suite stays green.
- `002-type-grant-integration-matrix` (`sonnet-med`):
  - the full integration matrix (direct, actor group, target groups, nested groups, operation closure, Q2 no inheritance, deprecated type, unknown type, sudo),
  - the `ilu6` regression in its test-only-type form,
  - an end-to-end `UserAccountService.Create` case,
  - the parity check against mod-authz's type-aware stand-in.

  Depends on 001.

Parallelism: none. 002 builds on 001's arm and helpers.

### Phase 7: Documentation updates (`phase-07-doc-updates`)

- `001-update-architecture-docs` (`sonnet-high`): update `docs/architecture.md` (D12 and the API-layer `TypeAuthorizer` paragraph), `docs/mod-users-spec.md` (the create-authorization rule) and AGENTS.md if it states the old rule. Match mod-core's reworded `TypeAuthorizer` contract.

This phase sits before Q1 on purpose, so the main change is fully documented even if the manager drops the optional phase.

### Phase 8: Type-grant instance semantics, optional Q1 (`phase-08-type-grant-instance-semantics`)

Runs after phases 6 and 7. The manager may drop this whole phase without affecting phases 6-7.

- `001-instance-semantics-arm` (`sonnet-med`): the extra `TargetChain` seed in `checkGrantOrOwn` (exact type, type entities excluded), the in-code warning, and unit and integration tests.
- `002-instance-semantics-warning-docs` (`sonnet-med`): the documented warning in `docs/architecture.md`, `docs/mod-users-spec.md` and the `localAuthz` facade doc.

Parallelism: 001 and 002 are parallel-eligible. They own disjoint files (001: `api/internal/authz/`; 002: `docs/` and `api/localAuthz/authz.go`). 002 must describe the semantics as specified in the design note, and its reviewer should re-read it against 001's landed code.

### Phase 9: Type-grant list symmetry and `UserAccountService.List` migration (`phase-09-type-grant-list-symmetry-and-list-migration`)

Added after the main plan. The design is in [production-side symmetry and the List migration](./notes/users-type-grant-design.md#production-side-symmetry-and-the-list-migration).

- `001-production-type-grant-symmetry` (`sonnet-med`): extend `wireServices` to install the real `corporation`, `natural_person` and `legal_entity` list functions, and extend `TestInteg_OwnerPredicate_ListSingleRowSymmetry` with type-grant holders, comparing the real list functions with the real `Authorizer`. Test-only. **Runs after mod-core phase 9** and mod-users phase 8. Moot, and to be removed, if the manager drops Q1.
- `002-user-account-list-authorize-type` (`sonnet-med`): move `UserAccountService.List` to `authorizeType(ctx, s.az, "list", <natural_person types.id>)`, with unit and integration tests, and document the warning that a type-level `list` grant exposes every account's email (corporation-held accounts included) in the doc comment, `docs/architecture.md` (D12) and `docs/mod-users-spec.md`. Needs mod-users phases 6-7 only, not mod-core phase 9.

Parallelism: 001 and 002 are parallel-eligible. They own disjoint files (001: `authz_integration_test.go`; 002: the service, a new authz integration test file, the handler comment and `docs/`). 002 may start before mod-core phase 9 lands; 001 may not. Both run the `./internal/authz/...` integration suite, so two concurrent runs need separate throwaway Postgres instances, and both share the sibling-link side effects in the design note.

### Cross-project dependencies

| Direction | Dependency |
|---|---|
| mod-users depends on mod-core phase 1 | The arm reads `types.entity_id`. Every integration test needs type entities. mod-core's id shifts are what invalidate the existing type-target fixtures. |
| mod-users depends on mod-authz phase 4 | The composed schema must carry the kind trigger, which constrains every new target-group fixture. mod-authz's `typeAwareIntegrationAuthorizer` is a stand-in for this arm; phase 6, task 002 checks parity. |
| mod-users docs depend on mod-core phase 2 | Phase 7 matches mod-core's reworded `TypeAuthorizer` contract wording. |
| Q1 (phase 8) and mod-core phase 9 | mod-core phase 9 holds the matching `GrantTableGenerator` arm, with the same type-entity exclusion, and reverses mod-core phase 2's "a type grant confers no instance access" wording. It runs after mod-users phase 8 and adds a symmetry test against a ported copy of `checkGrantOrOwn`. Until it lands, single-row and list-side checks disagree for type-grant holders. |
| mod-users task 9.001 depends on mod-core phase 9 | The production-side symmetry test calls the real `accessible_*_ids_for_actor` functions, which come from mod-core's `GrantTableGenerator` Go code through the sibling link. mod-core phase 9 must be checked out in the mod-core plan worktree before the task starts; the task greps `grant_table.go` for the arm's exclusion and halts if it is missing. |
| Q1 (phase 8) and mod-authz phase 9 | mod-authz's `integrationAuthorizer` stand-in for `Authorize` mirrors `checkGrantOrOwn`; mod-authz phase 9 adds the same arm and the warning. |
| mod-authz phase 10 reads mod-users phase 9 | mod-authz's federation-wide followups sweep runs last. It verifies that task 9.002 landed and proposes resolving followup `4dka`. |
| docs-mf-standards (outside the federation) | Followup `qjI3` should describe the final arm, and Q1's warning if adopted. |

### Assumptions applied

- **Deprecated types are not checked in `AuthorizeType`.** mod-core rejects the write, keeps deprecated types resolvable for `list`, and mod-authz allows grants on a deprecated type's entity. A type-level check that denied deprecated types would also deny listing their existing instances.
- **Two round trips for non-wildcard type checks are acceptable.** Merging the wildcard check into the arm's query is a later optimisation.
- **Q1 excludes type entities as targets of the instance arm.** Without that, `grant` on the sentinel `type` entity would confer `grant` over every type entity, which is a privilege escalation over all type-level authority. mod-core's later list-side arm must make the same exclusion.
- **Q1 is unconditional once phase 8 lands.** There is no configuration switch. "Optional" means the manager may drop the phase.
- **`UserAccountService.List` stays wildcard-only through phase 8.** Phase 9 moves it onto `authorizeType` by the user's later decision (followup `4dka`). Phase 7's D12 wording about `List` is superseded by task 9.002.
- **The `List` grant is checked on `natural_person`**, the same type `AuthorizeCreate` uses, even though a corporation can hold an account. The search is not filtered by holder type, so the warning names corporation-held accounts explicitly.

## What shipped

### Phase 06 — AuthorizeType Type-Grant Arm

1. **AuthorizeType Arm** (`001-authorize-type-arm.md`, tier `sonnet-high`) — Added checkTypeGrant, a recursive-CTE query seeded from types.entity_id WHERE types.id = typeID with no owner or parent walk, failing closed on unknown types; AuthorizeType keeps its prelude (wildcard first) and consults the arm via checkTypeGrantDispatch. Contract comments reworded. Unit tests cover the full stub matrix; the two invalidated integration tests were reworked. Integration suite passed against a local Postgres 14 (docker unavailable); api build, test, lint pass.
   Commit `8791b2c`, merged at `221660150ac7446e58850802f0d155ce4ee5bf44`.

2. **Type-Grant Integration Matrix** (`002-type-grant-integration-matrix.md`, tier `sonnet-med`) — Added the type-grant integration matrix (14 subtests), kind-trigger guard, the ilu6 test-only-type regression and an end-to-end service case; all pass against the composed schema under shuffle. The arm needed no fix. Parity with mod-authz's typeAwareIntegrationAuthorizer holds on all nine contract rules; three non-contract differences filed as finding UbXY. Docker was unavailable, local Postgres 14 used.
   Commit `c3b2514`, merged at `d680950121fe629e72087fb7e843b342e06a8c99`.

### Phase 07 — Documentation Updates

1. **Update Architecture Docs** (`001-update-architecture-docs.md`, tier `sonnet-high`) — Restated D12, the API-layer paragraph and the spec Authorization bullet for the shipped type-grant arm, worded to match mod-core's contract, with a rollout sentence (manage vs create-only authority) added. No instance-semantics claims; qjI3 verified accurate, no finding needed.
   Commit `13240f3`, merged at `241c8e724adfa8e13aa52b4041a42a3d3005b5ee`.

### Phase 08 — Type-Grant Instance Semantics, Optional Q1

1. **Instance Semantics Arm** (`001-instance-semantics-arm.md`, tier `sonnet-med`) — Added the Q1 instance-semantics seed to checkGrantOrOwn (target plus its exact type's entity via entities.fundamental_type_id -> types.entity_id) with the type-entity exclusion so a grant on the sentinel type entity confers nothing over other type entities. Warning documented in package, Authorize and checkGrantOrOwn docs; matrix integration tests added; mutation checks confirmed the tests catch a removed exclusion and a mis-joined seed. AuthorizeType and nil-target behaviour unchanged. Local Postgres 14 used (Docker unavailable).
   Commit `88735da`, merged at `bfe8ed127b21fc00aabb3f5e0b284a8613ae864d`.

2. **Instance Semantics Warning Docs** (`002-instance-semantics-warning-docs.md`, tier `sonnet-med`) — Added decision D13 with the mandatory instance-wide warning (all design-note points incl. natural_person assume and SSH-key routes) to docs/architecture.md, aligned D12's rollout sentence, added the spec cross-reference, and a warning in the localAuthz Authorizer doc. Prose matches task 8.001's in-code comment. api lint passes.
   Commit `e429826`, merged at `4e3d170869bbcb35e72ce2850f15dd6d6418ffe9`.

### Phase 09 — Type-Grant List Symmetry and UserAccountService.List Migration

1. **Production Type-Grant List/Single-Row Symmetry** (`001-production-type-grant-symmetry.md`, tier `sonnet-med`) — Production symmetry test added: the real GrantTableGenerator list functions (corporation, natural_person, legal_entity) agree with the real Authorizer for six type-grant fixtures plus a sentinel-type fixture; instance-seed mutation caught with a 'list side admitted' message; exclusion mutation invisible to the iff so a direct assertion was added; time-bound 'until the arm lands' sentences removed from authz.go docs and docs/architecture.md. api build/test/lint pass; local Postgres used (Docker unavailable).
   Commit `2e110f1`, merged at `9d6cfdd0e9c8e29c5d391feb54066bee6d338f2e`.

2. **UserAccountService.List on AuthorizeType** (`002-user-account-list-authorize-type.md`, tier `sonnet-med`) — UserAccountService.List now goes through authorizeType("list", natural_person) before any query (nil-target fallback unchanged and fail-closed); List doc comment, handler comment, architecture doc (warning blockquote after D12) and spec carry the email-exposure warning; unit tests with a recording stub querier and a 9-subtest integration matrix (allow cases assert other users' emails appear) pass; api build/test/lint pass. Local Postgres used (Docker unavailable).
   Commit `445cacf`, merged at `4fc7bf2cd7d6fe54aa7d8f60cebedb72b2b70760`.

### Phase 10 — Remediation Round 1

1. **Complete Instance Warning and Strengthen Instance Tests** (`001-complete-instance-warning-and-tests.md`, tier `sonnet-med`) — Warning completed in all five locations (group types, other instance-bearing types, assume of admin accounts) after verifying mod-authz group-edit gating; replaced the vacuous child-walk subtest with a real no-subtype-walk test (mutation-verified), added a via-groups guard on the test-only type, renamed plan shorthand to descriptive names; comment-only production diff; integration suite and api build/test/lint pass.
   Commit `d508731`, merged at `783cc5bdf1d800d9106c2908cadd1a40a504ee68`.

### Phase 11 — Remediation Round 2

1. **Fix Warning Sentence Integrity and Wording Precision** (`001-fix-warning-sentence-integrity.md`, tier `haiku-med`) — Comment and doc-only fix of the instance-semantics warning: escalation block now follows the intact parenthetical (verified with go doc), group-membership authority wording corrected (update alone only permits removal), anonymous system actor wording fixed, two test header comments reflowed. api build/test/lint pass.
   Commit `6d228fd`, merged at `a968444c1758d495398d1d11a937a8ac9d261a24`.

### Phase 12 — Remediation Round 3

1. **Fix List Migration Comments and Test Gaps** (`001-fix-list-comments-and-test-gaps.md`, tier `sonnet-med`) — Updated the List handler and route comments; added a corporation-held-account assertion and a create-on-type-entity operation-filter fixture to the symmetry test, both mutation-verified; no production code or SQL changed; integration run and api build/test/lint pass; local Postgres used (Docker unavailable).
   Commit `4781abb`, merged at `c4d62d0bd06179ab7c2f19399581cf34e31b59cc`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Findings

- **`adeO`** — **Docker was unreachable in the sandbox; the in** — promoted — ref: `adeO` — 2026-10-09

- **`UbXY`** — **Stand-in ignores sudo, unauth, unknown-op** — promoted — ref: `UbXY` — 2026-10-09

- **`16do`** — **Instance tests: vacuous child-walk case, ids** — fixed — ref: `phase-10-remediation-01/001-complete-instance-warning-and-tests.md` — 2026-10-09

- **`n4t2`** — **Instance warning omits escalation paths** — fixed — ref: `phase-10-remediation-01/001-complete-instance-warning-and-tests.md` — 2026-10-09

- **`oDuA`** — **Warning paragraph splits a sentence in godoc** — fixed — ref: `phase-11-remediation-02/001-fix-warning-sentence-integrity.md` — 2026-10-09

- **`HIBp`** — **Warning wording: update, anonymous actor** — fixed — ref: `phase-11-remediation-02/001-fix-warning-sentence-integrity.md` — 2026-10-09

- **`QpDq`** — **List migration: stale comments, test gaps** — fixed — ref: `phase-12-remediation-03/001-fix-list-comments-and-test-gaps.md` — 2026-10-09

- **`EJEa`** — **link-siblings.sh repointed the shared mod-use** — promoted — ref: `EJEa` — 2026-10-09

## Remediation

- Rounds used: 3 (resolved max_rounds: 3).
- Remediation tasks added: 3 (resolved max_added_tasks: 14).
- Remediation phases:
  - `remediation-01` — 1 task(s)
  - `remediation-02` — 1 task(s)
  - `remediation-03` — 1 task(s)
- Security review required: yes (at least one remediation phase carries `security_review: required`).

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 06 — AuthorizeType Type-Grant Arm

- [x] [001-authorize-type-arm.md](./phase-06-authorize-type-grant-arm/001-authorize-type-arm.md) — tier `sonnet-high` · branch `plan/type-scoped-grants-06-001` · commit `8791b2c` · merge `221660150ac7446e58850802f0d155ce4ee5bf44`
- [x] [002-type-grant-integration-matrix.md](./phase-06-authorize-type-grant-arm/002-type-grant-integration-matrix.md) — tier `sonnet-med` · branch `plan/type-scoped-grants-06-002` · commit `c3b2514` · merge `d680950121fe629e72087fb7e843b342e06a8c99`

### Phase 07 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-07-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `plan/type-scoped-grants-07-001` · commit `13240f3` · merge `241c8e724adfa8e13aa52b4041a42a3d3005b5ee`

### Phase 08 — Type-Grant Instance Semantics, Optional Q1

- [x] [001-instance-semantics-arm.md](./phase-08-type-grant-instance-semantics/001-instance-semantics-arm.md) — tier `sonnet-med` · branch `plan/type-scoped-grants-08-001` · commit `88735da` · merge `bfe8ed127b21fc00aabb3f5e0b284a8613ae864d`
- [x] [002-instance-semantics-warning-docs.md](./phase-08-type-grant-instance-semantics/002-instance-semantics-warning-docs.md) — tier `sonnet-med` · branch `plan/type-scoped-grants-08-002` · commit `e429826` · merge `4e3d170869bbcb35e72ce2850f15dd6d6418ffe9`

### Phase 09 — Type-Grant List Symmetry and UserAccountService.List Migration

- [x] [001-production-type-grant-symmetry.md](./phase-09-type-grant-list-symmetry-and-list-migration/001-production-type-grant-symmetry.md) — tier `sonnet-med` · branch `plan/type-scoped-grants-09-001` · commit `2e110f1` · merge `9d6cfdd0e9c8e29c5d391feb54066bee6d338f2e`
- [x] [002-user-account-list-authorize-type.md](./phase-09-type-grant-list-symmetry-and-list-migration/002-user-account-list-authorize-type.md) — tier `sonnet-med` · branch `plan/type-scoped-grants-09-002` · commit `445cacf` · merge `4fc7bf2cd7d6fe54aa7d8f60cebedb72b2b70760`

### Phase 10 — Remediation Round 1

- [x] [001-complete-instance-warning-and-tests.md](./phase-10-remediation-01/001-complete-instance-warning-and-tests.md) — tier `sonnet-med` · branch `plan/type-scoped-grants-10-001` · commit `d508731` · merge `783cc5bdf1d800d9106c2908cadd1a40a504ee68`

### Phase 11 — Remediation Round 2

- [x] [001-fix-warning-sentence-integrity.md](./phase-11-remediation-02/001-fix-warning-sentence-integrity.md) — tier `haiku-med` · branch `plan/type-scoped-grants-11-001` · commit `6d228fd` · merge `a968444c1758d495398d1d11a937a8ac9d261a24`

### Phase 12 — Remediation Round 3

- [x] [001-fix-list-comments-and-test-gaps.md](./phase-12-remediation-03/001-fix-list-comments-and-test-gaps.md) — tier `sonnet-med` · branch `plan/type-scoped-grants-12-001` · commit `4781abb` · merge `c4d62d0bd06179ab7c2f19399581cf34e31b59cc`
