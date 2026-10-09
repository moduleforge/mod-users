# Type-Scoped Grants: mod-users Slice

## Purpose and scope

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

## Current status

The plan starts at phase 6, `authorize-type-grant-arm`, task 001. No code has changed yet. Phases 1-3 belong to mod-core and phases 4-5 to mod-authz in this federated plan; mod-users uses phases 6-8, plus phase 9, added later. Phase 9 shares its number with mod-core's and mod-authz's phase 9; each project tracks its own phases.

Preconditions:

- The plan worktree is on branch `plan/type-scoped-grants` in mod-users.
- **mod-core phase 1 and mod-authz phase 4 must have landed before task 6.001 starts**, and mod-core phase 2 before phase 7. **mod-core phase 9 must have landed before task 9.001 starts.** Task 9.002 needs only mod-users phases 6-7 (and 8, unless dropped).
- **Sibling builds.** mod-users compiles against, and composes migrations from, `../../mod-core`, `../../mod-authz` and `../../mod-audit`. In worktrees these are symlinks that `scripts/link-siblings.sh` resets on every root `make` to `$MODULEFORGE_SIBLINGS_DIR/<sibling>`. That variable defaults to the aggregate `moduleforge/` directory (the `main` checkouts). Unless both sibling plans have merged to `main`, every task must build with `MODULEFORGE_SIBLINGS_DIR` pointing at a staging directory whose `mod-core` and `mod-authz` entries link to the sibling **plan worktrees**, and must confirm that the composed schema carries `types.entity_id` and the kind trigger before trusting an integration run. The exact recipe, the precondition greps and the side effects on other mod-users worktrees are in [building against the sibling plan branches](./notes/users-type-grant-design.md#building-against-the-sibling-plan-branches). The manager states in each dispatch which mode applies.
- Integration tests need Docker plus `goose` on `PATH`, using the throwaway-Postgres recipe in AGENTS.md ("Authz integration tests against a throwaway Postgres"). `make -C model compose` must be re-run after the sibling links change.

## Overview

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
