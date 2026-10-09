# Users Type Grant Design

## Purpose and scope

Records mod-users' design for the federated `type-scoped-grants` change: the `AuthorizeType` type-grant arm in `api/internal/authz/authz.go`, the impact of type entities on mod-users' integration fixtures, how mod-users tasks build against the sibling plan branches, and the optional Q1 instance-semantics arm. Every task document in this plan references it.

It builds on mod-core's [type entity design note](../../../../../../mod-core/worktrees/plan/type-scoped-grants/plan/notes/type-entity-design.md) and mod-authz's [authz type entity design note](../../../../../../mod-authz/worktrees/plan/type-scoped-grants/plan/notes/authz-type-entity-design.md), and does not restate them. Both supersede the architect report's proxy-table (`authz_type_targets`) and `TypeChain` ancestor-walk design.

## Inputs from the sibling slices

mod-users relies only on these sibling guarantees:

- **mod-core.** `types.entity_id` is the type's entity. It is non-NULL for every committed `types` row, unique and immutable. Type entities are never owned and never archived. The exact-type lookup is `SELECT entity_id FROM types WHERE id = $typeID`. Creating an entity of a deprecated type fails at the schema level with a stable SQLSTATE.
- **mod-authz.** `grants` and `authz_target_group_members` are unchanged in shape. A target group can never contain both a type entity and an instance entity, directly or through nested groups (the `trg_target_group_members_kind` trigger in `0504`). The violation surfaces as SQLSTATE `P0001`.

## AuthorizeType after the change

### Decision flow

`(*Authorizer).AuthorizeType(ctx, operation, typeID)`:

1. No effective actor: `ErrUnauthenticated`. Unchanged.
2. `typeID <= 0`: `ErrForbidden`, even for a wildcard holder. Unchanged.
3. `authorizePrelude`. A wildcard grant whose operation is in the `SatisfiedBy` closure allows. The unknown-slug fallback (wildcard `manage` only) is unchanged. **The wildcard grant still satisfies type-level checks.**
4. **New type-grant arm.** Run only when the prelude did not finish. One query:

   ```sql
   WITH RECURSIVE
       ActorChain AS (
           SELECT $1::bigint AS aid
           UNION
           SELECT agm.group_id
           FROM authz_actor_group_members agm
           JOIN ActorChain ac ON agm.member_id = ac.aid
       ),
       TargetChain AS (
           SELECT t.entity_id AS tid
           FROM types t
           WHERE t.id = $2::bigint
             AND t.entity_id IS NOT NULL
           UNION
           SELECT atgm.group_id
           FROM authz_target_group_members atgm
           JOIN TargetChain tc ON atgm.member_id = tc.tid
       )
   SELECT EXISTS (
       SELECT 1 FROM grants g
       JOIN ActorChain ac ON g.actor_id = ac.aid
       JOIN TargetChain tc ON g.target_id = tc.tid
       WHERE g.operation_id = ANY($3::int[])
   )
   ```

   `true` allows. `false` returns `ErrForbidden`. A DB error propagates unchanged and is never turned into a denial.

### Properties the arm must keep

| Property | How |
|---|---|
| Exact type only (Q2) | `TargetChain` is seeded from `types.id = $2` only. There is no `parent_id` walk, so a grant on `entity` or `legal_entity` does not cover `natural_person`. |
| No ownership arm | The query never reads `entities.owner_id`. Type entities are unowned anyway, but the arm must not depend on that. |
| `typeID` is never an `entities.id` | `$2` is compared only with `types.id`. It never reaches `grants.target_id`, `entities.id` or `owner_id`. |
| Fail closed | An unknown `typeID` (no `types` row) gives an empty `TargetChain`, so `false`. The `entity_id IS NOT NULL` guard is defensive: mod-core's commit-time invariant already makes NULL impossible for committed rows. |
| Target groups | Walks **up** from the type entity through `authz_target_group_members`, exactly as `checkGrantOrOwn` does. By mod-authz's kind rule, any group reached contains only type entities (and nested type-only groups). |
| Actor groups and sudo | Same `ActorChain` as the other checks, seeded with the effective (sudo-first) actor returned by the prelude. |
| Operation closure | Same `opIDs` from the prelude. `manage` on a type entity implies `create` and `list`; `create` does not imply `list`. |
| Entity-level `Authorize` | Unchanged in this phase. `AuthorizeType` still never calls `checkGrantOrOwn`. |

### Deprecated types

`AuthorizeType` does **not** consult `types.deprecated_at`, for wildcard holders or for type-grant holders. This is consistent with mod-core:

- mod-core rejects creating an entity of a deprecated type at the schema level, so an authorized `create` on a deprecated type still fails when it writes. The rejection belongs to the data layer, not the authorization layer.
- mod-core keeps deprecated types resolvable through `types.Resolver` precisely so that `AuthorizeType(list, ...)` keeps working for the existing instances of a deprecated type.
- mod-authz allows grants on a deprecated type's entity.
- mod-authz's type-aware stand-in has no deprecation check, so the two stay in step.

The behaviour is pinned by an integration test and stated in the doc comment.

### Round trips

A wildcard holder still pays one round trip. A non-wildcard actor pays two on a type-level check (wildcard miss, then the type-grant arm), where it previously paid one and was denied. Merging the two into one query is a possible later optimisation. It is not part of this plan, because `authorizePrelude` is shared with `Authorize` and must not drift.

### Test hook

Add a `typeGrantFn func(ctx, actorEntityID, typeID int64, opIDs []int32) (bool, error)` field and a `checkTypeGrantDispatch` method, mirroring `grantOrOwnFn` and `checkGrantOrOwnDispatch`. Export a `SetTypeGrantFn` setter in `export_test.go`. Unit tests then exercise the arm's control flow without a database. `NewWithStubOpReg` leaves `pool` nil, so a unit test that reaches the arm without setting the stub panics, which is the existing convention for `grantOrOwnFn`.

## Fixture impact in mod-users' composed schema

mod-users composes core, then users (`0100`), then authz (`0500`+). Predicted layout once mod-core's phase 1 lands. This follows from creation order and is **not a contract**: tests must resolve ids at runtime and never hard-code them.

| `types.id` | Slug | Type entity id | Note |
|---|---|---|---|
| 1-5 | `entity` ... `service_account` | 1-5 | Seeded in `0008`, in `types.id` order |
| 6 | `type` (sentinel) | 6 | |
| 7 | `app` | 7 | `0014` |
| 8 | `system_actor` | 8 | `0100` |
| (none) | anonymous actor instance | 9 | `0100` inserts the `anonymous` entity |
| 9 | `authz_actor_group` | 10 | `0501` |
| 10 | `authz_target_group` | 11 | `0503` |

Consequences for the existing tests in `api/internal/authz/`:

- **`natural_person` (`types.id` 3).** Entity 3 is now `natural_person`'s own type entity. `grantEntityLevelAuthorityOver` finds it existing and inserts a targeted `create` grant on it. That is a **legitimate type grant** after the change. `TestInteg_TypeTarget_EntityAuthorityDoesNotAnswerTypeCheck/natural_person` and `TestInteg_UserAccountService_Create_EntityAuthorityOverTypeIDDenied` therefore fail once the arm lands, and must be reworked in the same task.
- **`authz_actor_group` (`types.id` 9, predicted).** The entity at id 9 is predicted to be the anonymous actor, an instance. A grant on it must still be denied by `AuthorizeType`. That is a natural `ilu6` collision, but it is an accident of ordering, so the deterministic regression must not rely on it.
- **`seedEntityWithExplicitID` (the ownership-arm path).** It can only run when no entity has id `typeID`. After the change every low id is taken, so the ownership branch of `grantEntityLevelAuthorityOver` effectively becomes dead. Remove it, rather than keeping a branch that no longer runs.
- **`TestInteg_TypeTarget_OwnershipArm_Deterministic` and `..._TargetedGrantArm_Deterministic`** pass an entity id as `typeID`. No `types` row has that id, so they still pass, but now through the "unknown type id" path. Keep them, and update their comments to say so.
- **`TestInteg_TypeTarget_WildcardHolders_Allowed`** is unaffected.

### The `ilu6` regression in its new form

Same transformation as mod-core (phase 1, task 003) and mod-authz (phase 4, task 001):

1. Advance the `types` id sequence well past `max(entities.id)` (for example `setval` to `max(entities.id) + 1000`, never moving it backwards).
2. Register a test-only concrete type under `entity` with a unique slug. mod-core's trigger gives it a type entity whose id differs from its `types.id`.
3. Insert an instance entity of an existing concrete type (for example `corporation`) at `id == types.id` of the test type, then advance the `entities` sequence past it (the existing `seedEntityWithExplicitID` sequence-advance logic, without the owner).
4. Give an actor `create` and `list` grants on that instance, directly, through an instance-only target group, and (separately) by ownership of an instance at that id where it can be arranged. `AuthorizeType(ctx, op, testTypeID)` must return `ErrForbidden`. As a control, `Authorize(ctx, op, &testTypeID)` admits the same actor, which is why call sites must never pass `&typeID`.
5. Give another actor the same grants on the test type's **type entity**. `AuthorizeType` admits them. This proves the arm reads `types.entity_id` and not `types.id`.

The test-only type persists in the database for the rest of the run, because types are append-only. Use a slug that cannot collide with any real type and with other tests.

### Kind-separation constraint on fixtures

mod-users' integration suite composes mod-authz's migrations, so it inherits the kind trigger. Every fixture target group must hold **only** type entities or **only** instance entities, counting nested groups and ancestor groups. A fixture that mixes them now fails with SQLSTATE `P0001` at insert time. Use separate groups for the type-grant and instance-grant cases.

Today no mod-users integration test creates a target group, so no existing fixture violates the rule. The new fixtures need a small `seedTargetGroup` helper and an `addTargetGroupMember` helper. `seedActorGroup` in `anonymous_actor_integration_test.go` and mod-authz's `0503`/`0504` migrations show the table shapes. Insert through SQL, not through mod-authz's services, so these tests stay focused on the Authorizer.

## Parity with mod-authz's type-aware stand-in

mod-authz's `api/service/type_level_authz_integration_test.go` holds `typeAwareIntegrationAuthorizer`, a stand-in for this arm (mod-authz phase 4, task 001). Its contract: wildcard grant, or a grant on the exact type's entity (direct or through target groups); no ownership; no parent walk; `typeID` never compared with `entities.id`; no deprecation check.

The arm specified here matches that contract. mod-users cannot edit mod-authz in this plan. The integration-matrix task therefore reads the stand-in on mod-authz's plan branch and compares it with the landed arm, rule by rule. A divergence is filed as a finding naming the rule, not fixed here.

mod-authz's `integrationAuthorizer` in `api/service/grants_integration_test.go` is the matching stand-in for entity-level `Authorize`. It is unaffected by phase 6. It **is** affected by the Q1 arm, see below.

## Building against the sibling plan branches

mod-users compiles against, and composes migrations from, `../../mod-core`, `../../mod-authz` and `../../mod-audit`, relative to `api/` and `model/` (`api/go.mod` replace directives; `model/Makefile` `CORE_DIR` and `AUTHZ_DIR`). In a worktree these resolve through symlinks that `scripts/link-siblings.sh` plants in the worktree's parent directory **and** in the shared `mod-users/worktrees/` directory. `make preflight`, a prerequisite of `make build`, `make test` and `make lint`, re-runs the script on every invocation and resets the symlinks to `$MODULEFORGE_SIBLINGS_DIR/<sibling>`. `MODULEFORGE_SIBLINGS_DIR` defaults to the aggregate `moduleforge/` directory, that is the `main` checkouts.

mod-users needs both siblings' plan work: mod-core phase 1 (type entities) for the arm's lookup and every integration test, and mod-authz phase 4 (kind trigger) for the fixture constraint. So unless both plans have merged to their `main` before a mod-users task starts, the task must:

1. Use a staging directory outside every repository, for example `/Users/zane/playground/moduleforge/tmp/type-scoped-grants-siblings/`, holding exactly three symlinks:
   - `mod-core` -> `/Users/zane/playground/moduleforge/mod-core/worktrees/plan/type-scoped-grants`
   - `mod-authz` -> `/Users/zane/playground/moduleforge/mod-authz/worktrees/plan/type-scoped-grants`
   - `mod-audit` -> `/Users/zane/playground/moduleforge/mod-audit`
2. Run `MODULEFORGE_SIBLINGS_DIR=<staging dir> bash scripts/link-siblings.sh` from the worktree root before building, and keep the variable exported for **every** root-level `make` invocation in the task (root `make build`, `make test` and `make lint` re-run the script through `preflight`, and without the variable they reset the links to the `main` checkouts). The sub-project targets `make -C api build|test|lint` and `make -C model compose` do not run the script, so they use whatever the links point at. Prefer them for validation; the root targets also build `gui/`, which this plan does not touch.
3. Before trusting any integration run, confirm the composed schema is the plan-branch one:
   - `grep -l entity_id model/schema/migrations/0002_types.sql` matches.
   - `grep -l trg_target_group_members_kind model/schema/migrations/0504_authz_target_group_members.sql` matches.

   If either fails, halt and report rather than working around it.

Side effects the manager must know about:

- The script also repoints the shared `mod-users/worktrees/{mod-core,mod-authz,mod-audit}` symlinks. Another mod-users worktree building at the same time (for example `worktrees/plan/users-action-required-migration`) sees the plan branches too, and its next plain `make` resets them. Do not run unrelated mod-users builds concurrently with this plan's tasks.
- The plan worktrees' working trees, not their branch tips, are what gets built. A sibling task merged into the sibling plan branch but not yet checked out in the sibling plan worktree is invisible.

## Q1: instance semantics (optional, last phase)

### Semantics

A grant over a type's entity also confers the same operation over every existing **instance** of exactly that type. "`read` on Cars" then reads every Car. This is an extra arm in entity-level `Authorize`, inside `checkGrantOrOwn`.

### Shape

Seed `TargetChain` with both the target and the exact fundamental type's entity of the target:

```sql
TargetChain AS (
    SELECT $2::bigint AS tid
    UNION
    SELECT t.entity_id
    FROM entities e
    JOIN types t ON t.id = e.fundamental_type_id
    WHERE e.id = $2::bigint
      AND NOT EXISTS (SELECT 1 FROM types tt WHERE tt.entity_id = $2::bigint)
    UNION
    SELECT atgm.group_id
    FROM authz_target_group_members atgm
    JOIN TargetChain tc ON atgm.member_id = tc.tid
)
```

`UNION` inside a recursive CTE's non-recursive term is allowed in PostgreSQL. An implementer may restructure the CTE (for example a separate non-recursive `Seed` CTE) as long as the semantics hold. The ownership arm is unchanged.

Properties:

- **Exact type only (Q2).** The seed uses the target's own `fundamental_type_id`. A grant on `legal_entity`'s entity does not reach a `natural_person`.
- **Target groups.** A grant on a type-only group that contains the type entity also confers the instance access, because the group walk starts from the seeded type entity.
- **Type entities are excluded as targets.** When the target is itself a type entity, its fundamental type is the sentinel `type`, so without the exclusion a grant on the sentinel's own entity would confer the operation over every type entity. `grant` or `manage` on the sentinel's entity would then let its holder create grants on every type, which is a privilege escalation over all of type-level authority. The `NOT EXISTS` guard keeps class-level authority from ever being derived through the sentinel. mod-core's matching `GrantTableGenerator` arm must make the same exclusion.
- **Fails closed** for a target id with no `entities` row: the seed contributes nothing.

### Required warning (documented)

The warning text must say, at least:

- A type grant now confers the operation over **every existing and future instance** of exactly that type, for every operation in its closure. `manage` on a type entity is full control (read, update, delete, `assume`, `grant`, `revoke`, ...) of every instance. For `natural_person` that includes `assume` of every user account and installing SSH keys through the operator routes.
- `list` implies `read`, so a type-level `list` grant also reads every instance.
- `manage` or `grant` on a type-only target group confers all of the above for every type in the group.
- It does not extend to subtypes or parent types, and it never applies to type entities themselves.
- Holders of `grant` on a type entity can therefore hand out instance-wide authority over that type. Grant on type entities only to principals trusted with every instance.
- Until mod-core's matching `GrantTableGenerator` arm lands, single-row `Authorize` and the list-side `accessible_*_ids_for_actor` functions disagree for type-grant holders.

### Cross-project dependencies of Q1

- **mod-core.** A matching `GrantTableGenerator` arm (list side), planned in a later mod-core phase. mod-core's phase 2 contract wording states that a type grant confers **no** instance access. That later mod-core phase must reverse that wording and carry the same warning.
- **mod-authz.** The `integrationAuthorizer` stand-in for `Authorize` (in `api/service/grants_integration_test.go`) mirrors `checkGrantOrOwn`. It must gain the same arm, or it stops being a faithful stand-in. mod-authz's own plan currently treats Q1 as unplanned.
- **List/single-row symmetry.** mod-users' `TestInteg_OwnerPredicate_ListSingleRowSymmetry` uses only ownership, so it stays green. A symmetry assertion for type-grant holders needs mod-core's arm. Whichever of the two arms lands second must add it. mod-core's arm lands second and adds a ported-reference version; mod-users adds the production-side version in phase 9, described in [production-side list/single-row symmetry](#production-side-listsingle-row-symmetry).

## Production-side symmetry and the List migration

Added after the main plan, as phase 9.

### Production-side list/single-row symmetry

mod-core phase 9 lands its `GrantTableGenerator` arm after mod-users phase 8, so mod-core owns the "second arm" symmetry test. mod-core cannot import mod-users' `Authorizer`, so its test compares the list functions with a **copy** of `checkGrantOrOwn`'s SQL. That copy drifts silently if mod-users later changes the arm.

mod-users composes mod-core's real `GrantTableGenerator` in its integration suite (`wireServices` calls `setup.ApplyFuncs`). It can therefore compare the real list functions with the real `Authorizer`. The test extends `TestInteg_OwnerPredicate_ListSingleRowSymmetry` with type-grant holders, over the production slugs `corporation`, `natural_person` and `legal_entity`. It skips `type`, which production never generates (`authzSlugs` in `api/cmd/server/main.go`). The assertion is "listed iff `Authorize` admits and the candidate's type is under the slug". It needs mod-core phase 9 checked out in the mod-core plan worktree, and is moot if the manager drops Q1.

### `UserAccountService.List` on `authorizeType` (followup `4dka`)

The user decided to include this, accepting the exposure below. `List` calls `authorizeType(ctx, s.az, "list", <natural_person types.id>)`, exactly like `AuthorizeCreate`. A wildcard grant, or a grant on `natural_person`'s type entity (directly or through type-only target groups, by the actor or its actor groups), authorizes it. The nil-target fallback for an authorizer without `AuthorizeType` is unchanged and stays wildcard-only.

Exposure, which must be documented as a warning:

- `SearchUserAccounts` is unscoped. A `list` (or `manage`) grant on `natural_person`'s type entity returns **every** user account with its email.
- `user_accounts.account_holder` references `legal_entities`, so a holder can be a corporation. The search does not filter by holder type, so the grant exposes corporation-held accounts too, although it names `natural_person`.
- It is exact-type (Q2). Grants on `legal_entity` or `entity`, grants on instances and ownership do not authorize it.
- With Q1 landed, the same grant already confers `read` on every natural_person instance (`list` implies `read`), so `List` adds bulk email enumeration to authority the holder largely has.

Filtering the search per row, or by holder type, would change the wildcard holder's results too. It is not part of the decision.

## Out of scope for mod-users

- **mod-users' `authorizeType` helper fallback.** Unchanged: a decorator that hides `TypeAuthorizer` falls back to `Authorize(ctx, op, nil)`, so type-grant holders are denied (fail closed), matching mod-core's reworded contract.
- **`docs/mf-standards/architecture/authorization-design.md`** (a submodule). Tracked upstream by docs-mf-standards followup `qjI3`.
