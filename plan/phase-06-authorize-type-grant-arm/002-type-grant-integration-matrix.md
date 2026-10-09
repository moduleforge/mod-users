# Type-Grant Integration Matrix

## Purpose and scope

Prove the `AuthorizeType` type-grant arm from task 001 against the real composed schema (core with type entities, authz with the kind trigger, users). This adds the full integration matrix, the `ilu6` regression in its test-only-type form, an end-to-end `UserAccountService.Create` case, and a parity check against mod-authz's type-aware stand-in.

This is a standard implementation task: follow the `implement-task` procedure. It adds tests only. If a test exposes a defect in task 001's arm, fix the arm in `api/internal/authz/authz.go` and say so in the report.

Files in scope:

- a new `api/internal/authz/type_grant_integration_test.go`
- `api/internal/authz/type_target_integration_test.go`, only to share or move helpers
- `api/internal/authz/user_account_create_integration_test.go`, only for the end-to-end `ilu6` case
- `api/internal/authz/authz.go`, only if a test exposes a defect

## Requirements

1. **Helpers** (behind the `integration` build tag), inserting through SQL or the sqlc queries, not through mod-authz services:
   - `typeEntityID(t, slug)`: resolves `types.entity_id`. It fails the test if the value is NULL.
   - `seedTargetGroup(t, name)`: an `authz_target_group` entity plus its `authz_target_groups` row, modelled on `seedActorGroup`.
   - `addTargetGroupMember(t, groupID, memberID)` and `addActorGroupMember(t, groupID, memberID)`. Reuse existing helpers if present.
   - `registerTestType(t, slug)`: advances the `types` id sequence past `max(entities.id)` (never backwards), registers a concrete type under `entity`, and returns its `types.id` and `types.entity_id`. It asserts that the two differ. See [the `ilu6` regression in its new form](../notes/users-type-grant-design.md#the-ilu6-regression-in-its-new-form).
   - Use slugs unique per test. Types are append-only, so they persist for the whole run.
2. **Kind-separation discipline.** Every target group in these fixtures holds only type entities or only instance entities, counting nested groups ([why](../notes/users-type-grant-design.md#kind-separation-constraint-on-fixtures)). Add one guard test: inserting an instance into a type-only group fails with SQLSTATE `P0001`. It confirms the composed schema carries mod-authz's kind trigger, so the other tests are not run against a stale schema.
3. **Matrix.** Each row is one assertion on `integAZ.AuthorizeType`, unless stated otherwise. "Type X" means `types.id` of X, and grants target X's type entity unless stated otherwise.

   | Case | Expected |
   |---|---|
   | Direct `create` grant on `corporation`'s type entity: `AuthorizeType(create, corporation)` | allowed |
   | Same actor: `AuthorizeType(list, corporation)` | `ErrForbidden` (`create` does not imply `list`) |
   | Same actor: `AuthorizeType(create, natural_person)` | `ErrForbidden` (a different type) |
   | `manage` on `corporation`'s type entity: `create` and `list` | both allowed (closure) |
   | `read` on `corporation`'s type entity: `create` | `ErrForbidden` |
   | Grant held by an actor group the actor belongs to (nested two levels) | allowed |
   | Grant on a type-only target group containing the type entity | allowed |
   | Grant on an outer type-only group containing an inner type-only group that contains the type entity | allowed |
   | Grant on a type-only group that does **not** contain this type's entity | `ErrForbidden` |
   | Q2: `create` on `legal_entity`'s type entity, then `AuthorizeType(create, natural_person)` and `(create, corporation)` | both `ErrForbidden` |
   | Q2: `create` on `entity`'s type entity, then `AuthorizeType(create, corporation)` | `ErrForbidden` |
   | Q2, reverse: `create` on `natural_person`'s type entity, then `AuthorizeType(create, legal_entity)` | `ErrForbidden` |
   | Sudo: the real actor holds the type grant, and the sudo actor does not | `ErrForbidden` (the effective actor is the sudo actor) |
   | Sudo: the sudo actor holds the type grant | allowed |
   | Unknown type id (`max(types.id) + 1000`) for an actor holding type grants | `ErrForbidden` |
   | Deprecated type: register a test type, grant `create` and `list` on its type entity, then set `deprecated_at` | `AuthorizeType` allows both ([decision](../notes/users-type-grant-design.md#deprecated-types)). Then assert that inserting an entity of that type fails, with the mod-core SQLSTATE, proving the write is still blocked. |
   | Revocation: delete the type grant | the next call is `ErrForbidden` |
   | Wildcard `manage` holder, wildcard `create` holder | unchanged outcomes |
   | Ownership never applies: an actor owns an instance entity, and holds nothing on the type entity | `ErrForbidden` |

4. **`ilu6` regression, test-only-type form.** Follow the five steps in the [design note](../notes/users-type-grant-design.md#the-ilu6-regression-in-its-new-form):
   - an instance at `id == testType.types.id`, with grants on it held directly, through an instance-only target group, and by owning it (create the instance with `owner_id` set to the actor at insert time): `AuthorizeType` returns `ErrForbidden` for `create` and `list`;
   - the control `Authorize(ctx, op, &testTypeID)` admits the same actor;
   - an actor holding the grants on the test type's **type entity**: `AuthorizeType` allows.
5. **End-to-end through the service.** In `user_account_create_integration_test.go`, build a `UserAccountService` whose type resolver maps `natural_person` to the test type's id (`types.NewFromMap`). An actor whose `create` grant targets the instance at `id == testTypeID` is denied (`ErrForbidden`, `BeginTx` never reached). An actor whose grant targets the test type's entity reaches the transaction stub. The service passes whatever id its resolver returns to `AuthorizeType`, so this exercises the real call path.
6. **Parity with mod-authz's stand-in.** Read `typeAwareIntegrationAuthorizer` in `api/service/type_level_authz_integration_test.go` in the mod-authz plan worktree (`/Users/zane/playground/moduleforge/mod-authz/worktrees/plan/type-scoped-grants`), if mod-authz phase 4, task 001 has landed there. Compare it with the landed arm on each rule in the [parity section](../notes/users-type-grant-design.md#parity-with-mod-authzs-type-aware-stand-in): wildcard, exact type, target-group walk direction, actor chain, no ownership, no parent walk, `typeID` never compared with entity ids, no deprecation check, unknown type id. Report the result in the task's Status section. File each divergence as a finding (`followups_add`, `type:conflict`) naming the rule and both files. Do not edit mod-authz. If the stand-in has not landed, say so and file nothing.

## Validation

- Sibling precondition greps from the [design note](../notes/users-type-grant-design.md#building-against-the-sibling-plan-branches) match after `make -C model compose`.
- The full `./internal/authz/...` integration suite passes against a throwaway Postgres (AGENTS.md recipe), with `-count=1`. It also passes with `-shuffle=on`, which proves the new tests do not depend on run order or on each other's types. Delete only the throwaway container afterwards.
- `make -C api test` and `make -C api lint` pass.
- `grep -nE "Exec\(.*(id|ID) = [0-9]+" api/internal/authz/type_grant_integration_test.go` finds no hard-coded entity or type ids.
- `git diff --stat -- . ':(exclude)plan'` touches only the files in [Purpose and scope](#purpose-and-scope).
- The Status section records the parity result.

## Assumptions

- Task 001 has landed, including `typeGrantFn` and the reworked fixtures.
- mod-authz phase 4 has landed in the composed schema: the kind trigger exists.
- If an existing test in the package fails under `-shuffle=on` for reasons unrelated to this task, record it as a finding and validate the new tests with `-run` instead.

## References

- [Users type grant design note](../notes/users-type-grant-design.md): the matrix rationale, the `ilu6` form, the kind constraint and the parity contract.
- `api/internal/authz/authz_integration_test.go:276-470`: the seed helpers and the grant helpers.
- `api/internal/authz/anonymous_actor_integration_test.go:60-100`: `seedActorGroup`, the pattern for `seedTargetGroup`.
- mod-authz `model/migrations/0503_authz_target_groups.sql` and `0504_authz_target_group_members.sql`: the table shapes and the kind trigger.
- mod-authz's [authz type entity design note](../../../../../../mod-authz/worktrees/plan/type-scoped-grants/plan/notes/authz-type-entity-design.md#the-ilu6-fixture-after-type-entities): the matching rework there.
- mod-core `model/migrations/0008_entities.sql` on the mod-core plan branch: the deprecated-type rejection and its SQLSTATE.

## Checkpoint hints

- After the helpers and the kind guard test pass.
- After the matrix passes.
- After the `ilu6` and end-to-end service cases pass.
- After the parity check is recorded.
