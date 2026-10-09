# AuthorizeType Arm

## Purpose and scope

Make the production `AuthorizeType` honour grants on a type's entity, as specified in the [design note](../notes/users-type-grant-design.md#authorizetype-after-the-change). After the wildcard check misses, `AuthorizeType` runs a new type-grant arm. It looks up the exact type's entity, walks up through target groups, and checks for a grant over the actor chain. It has no ownership arm and no parent walk, and it fails closed.

The task also reworks the existing integration tests that the arm invalidates, so the `./internal/authz/...` integration suite stays green. The full new matrix is task 002.

This is a standard implementation task: follow the `implement-task` procedure.

Files in scope:

- `api/internal/authz/authz.go`
- `api/internal/authz/export_test.go`
- `api/internal/authz/authz_test.go`
- `api/internal/authz/type_target_integration_test.go`
- `api/internal/authz/user_account_create_integration_test.go`
- `api/localAuthz/authz.go` (doc comment only)
- `api/internal/service/user_accounts.go` (comments only)
- `api/internal/service/user_accounts_create_authz_test.go` (comments only, if they state "wildcard-only" as the production rule)

## Requirements

1. **The arm.** Add `checkTypeGrant(ctx, actorEntityID, typeID int64, opIDs []int32) (bool, error)` running the query in the [design note](../notes/users-type-grant-design.md#decision-flow). Add a `typeGrantFn` test-stub field and `checkTypeGrantDispatch`, mirroring `grantOrOwnFn` and `checkGrantOrOwnDispatch`.
2. **`AuthorizeType` flow.** Keep the unauthenticated check, the `typeID <= 0` check and `authorizePrelude` exactly as they are. Capture `actorEntityID` and `opIDs` from the prelude (today they are discarded). When the prelude is not done, call `checkTypeGrantDispatch`:
   - an error is returned unchanged,
   - `true` returns nil,
   - `false` returns `ErrForbidden`.

   `AuthorizeType` must still never call `checkGrantOrOwn`, never read `entities.owner_id`, and never compare `typeID` with an entity id. Do not add a `deprecated_at` check ([why](../notes/users-type-grant-design.md#deprecated-types)).
3. **`Authorize` is unchanged** in behaviour and code path.
4. **Doc comments.**
   - Rewrite the package doc paragraphs and the `AuthorizeType` doc comment that say type-level authority is wildcard-only, or that "no grant can target a type". State the new contract: a wildcard grant, or a grant on the exact type's entity (`types.entity_id` of `typeID`), held directly or through target groups, by the actor or its actor groups. No ownership, no subtype or parent-type inheritance, `typeID` never treated as an `entities.id`, unknown type ids denied, deprecation not consulted.
   - Remove the "If type-scoped grants are ever introduced they slot in here" sentence, since this is that arm.
   - Keep the warning against passing a `types.id` to `Authorize`. Add that a type's own entity is a legitimate `Authorize` target for entity-level operations on the type entity itself (for example `read` or `grant`), but is never a stand-in for the type id in a type-level check.
   - Update `api/localAuthz/authz.go`'s `TypeAuthorizer` doc ("allowed only to actors holding a wildcard grant ... never answered by ... targeted grants") the same way. Keep its consumer contract and nil-target fallback paragraph. Add that the fallback denies type-grant holders, which is fail-closed.
   - Update comments in `api/internal/service/user_accounts.go` (`authorizeType`, `AuthorizeCreate`, `Create`) that call create "wildcard-only". The code does not change.
5. **Unit tests** in `authz_test.go`, using a `SetTypeGrantFn` setter added to `export_test.go`:
   - Wildcard allow short-circuits: the type-grant stub is not called.
   - Wildcard miss plus type-grant stub `true`: allowed. The stub receives the effective actor (the sudo actor when set), the exact `typeID`, and the `opIDs` closure.
   - Wildcard miss plus stub `false`: `ErrForbidden`.
   - Stub error: propagates (`errors.Is`).
   - `typeID <= 0` and no-actor: the stub is never called.
   - Unknown operation slug: the existing fallback outcome holds, and the stub is never called.
   - `TestAuthorizeType_EntityOwnerOfIDEqualToTypeID_Denied`: keep the guarantee that `grantOrOwnFn` is never invoked. Give it a type-grant stub returning `false`.
   - Update the `--- AuthorizeType (type-level) tests ---` header comment to the new contract.
6. **Rework the invalidated integration tests**, as analysed in the [design note](../notes/users-type-grant-design.md#fixture-impact-in-mod-users-composed-schema):
   - `TestInteg_TypeTarget_EntityAuthorityDoesNotAnswerTypeCheck`: it must no longer assume the entity at `id == typeID` is unrelated to the type. For each tested slug, resolve `types.entity_id`. If the entity at `id == typeID` is that type's own entity, assert that `AuthorizeType` **allows** (type grant) and that it is unowned. Otherwise assert that it denies, as today. Log which case applied. Remove the ownership branch of `grantEntityLevelAuthorityOver` and `seedEntityWithExplicitID`'s owner parameter if nothing else needs them. Keep the sequence-advance logic, because task 002 reuses it.
   - `TestInteg_UserAccountService_Create_EntityAuthorityOverTypeIDDenied`: entity `natural_person.types.id` is now `natural_person`'s type entity, so a grant on it is a legitimate type grant. Replace the test with two:
     - a `create` grant on `natural_person`'s type entity (resolved through `types.entity_id`, not assumed equal to the type id) lets `Create` reach the transaction stub;
     - a `create` grant on an **instance** entity does not. Use a freshly seeded user's entity: its id is not `natural_person`'s type entity.

     The deterministic `ilu6` form, with a test-only type, belongs to task 002.
   - `TestInteg_TypeTarget_OwnershipArm_Deterministic` and `..._TargetedGrantArm_Deterministic`: keep. Update their comments to say they now exercise the unknown-type-id path (no `types` row has that id), and assert that precondition explicitly (`SELECT NOT EXISTS (SELECT 1 FROM types WHERE id = $1)`), so they cannot silently start testing something else.
   - Update the file-header comments of both integration files. Do not leave text saying `AuthorizeType` answers "from wildcard grants only".
7. **Build mode.** Build and test against the sibling plan branches as described in the [design note](../notes/users-type-grant-design.md#building-against-the-sibling-plan-branches), unless the dispatch says both sibling plans have merged to `main`.

## Validation

- Sibling precondition: after `make -C model compose`, both greps in the [design note](../notes/users-type-grant-design.md#building-against-the-sibling-plan-branches) match. If not, halt and report.
- `make -C api build`, `make -C api test` and `make -C api lint` pass.
- The full `./internal/authz/...` integration suite passes against a throwaway Postgres (AGENTS.md recipe), with `-count=1`.
- `grep -n "wildcard-only today\|No grant can target a type\|slot in here" api/internal/authz/authz.go api/localAuthz/authz.go` returns nothing.
- `grep -n "owner_id" api/internal/authz/authz.go` shows no use inside `checkTypeGrant`'s SQL.
- `grep -n "parent_id" api/internal/authz/authz.go` returns nothing.
- `git diff --stat -- . ':(exclude)plan'` touches only the files listed in [Purpose and scope](#purpose-and-scope), and nothing under `model/`.
- `git diff -- api/localAuthz/authz.go api/internal/service/user_accounts.go` changes comment lines only.

## Metadata

architectural_impact: true

## Assumptions

- mod-core phase 1 and mod-authz phase 4 are visible through the sibling links. If `SELECT entity_id FROM types LIMIT 1` fails in the composed schema, halt and report.
- The predicted id layout in the design note may differ. Tests resolve ids at runtime.

## References

- [Users type grant design note](../notes/users-type-grant-design.md): the arm, its properties, the fixture analysis and the build recipe.
- `api/internal/authz/authz.go:177-216` (`AuthorizeType`), `:218-276` (`authorizePrelude`), `:280-295` (dispatch helpers), `:340-419` (`checkGrantOrOwn`, the SQL pattern to mirror).
- `api/internal/authz/authz_test.go:361-492`: the existing `AuthorizeType` unit tests.
- `api/internal/authz/type_target_integration_test.go` and `user_account_create_integration_test.go`: the tests to rework.
- mod-core's type entity design note, "Contract for sibling slices": `/Users/zane/playground/moduleforge/mod-core/worktrees/plan/type-scoped-grants/plan/notes/type-entity-design.md`.
- mod-authz's stand-in contract: `/Users/zane/playground/moduleforge/mod-authz/worktrees/plan/type-scoped-grants/plan/phase-04-type-entity-authz-integrity/001-integration-fixture-rework.md`, requirement 3.
- `plan/plan-summary-fix-actor-group-target-confusion.md`: the original `ilu6` fix.

## Checkpoint hints

- After the arm, the hook and the unit tests pass.
- After the doc-comment rewrites.
- After the reworked integration tests pass against the composed plan-branch schema.

## Status

- Outcome: succeeded (2026-10-09).
- Built against the sibling plan branches (staging dir with mod-core and mod-authz plan worktrees, mod-audit main). Both precondition greps matched after `make -C model compose`.
- Validation: `make -C api build|test|lint` pass. The `./internal/authz/...` integration suite (`-tags=integration -p 1 -count=1`) passes, 82 PASS lines, no skips. It ran against a local Postgres 14.23 (Homebrew) on a random port, because the Docker daemon was unreachable in the sandbox. Greps clean: no "wildcard-only today", "No grant can target a type", "slot in here" or `parent_id` in `authz.go`/`localAuthz/authz.go`. The only `owner_id` SQL use is `checkGrantOrOwn`'s.
- Observed layout: `natural_person` types.id 3 equals its type entity id 3. `authz_actor_group` types.id 9 has type entity 10, and entity 9 is an unrelated instance.
- Files: `api/internal/authz/authz.go`, `export_test.go`, `authz_test.go`, `type_target_integration_test.go`, `user_account_create_integration_test.go`, `api/localAuthz/authz.go` (comments), `api/internal/service/user_accounts.go` (comments).
- `seedEntityWithExplicitID(t, id)` lost its owner parameter and is currently unused; task 002 reuses it and its sequence-advance logic.
