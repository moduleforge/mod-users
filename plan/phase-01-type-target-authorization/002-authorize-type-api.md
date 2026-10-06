# Reproduce Type-Target Confusion And Add AuthorizeType

## Purpose and scope

**Security task.** First reproduce, with failing tests in mod-users' own `api/internal/authz` tests, that a type-level authorization check is answered by entity-level ownership and grants. Then fix it at the API level by adding a distinct type-level entry point, `(*Authorizer).AuthorizeType`, and an exported `localAuthz.TypeAuthorizer` capability interface. Leave `Authorize` entity-only and source-compatible. Implement with the standard `implement-task` procedure, test-first. The design contract is [type target design](../notes/type-target-design.md). Follow it, and halt and report if it proves wrong.

Scope: `api/internal/authz/authz.go`, `api/internal/authz/export_test.go` (if test hooks are needed), `api/internal/authz/authz_test.go`, `api/internal/authz/authz_integration_test.go` (or a new `type_target_integration_test.go` in the same package), and `api/localAuthz/authz.go`. Call-site migration is task 003. Do not touch it here.

Docker host is shared. Run integration tests only against a throwaway Postgres 16+ container (unique name, random port) using the recipe task 001 added to the `authz_integration_test.go` header. Never run `make dev.start` or touch `users-module-postgres`.

## Requirements

1. **Red first: reproduce the root cause.** Before changing production behavior:
   - Add `AuthorizeType(ctx, operation string, typeID int64) error` as a temporary **naive delegate**, `return a.Authorize(ctx, operation, &typeID)`. This is exactly what every type-level call site does today.
   - Write the unit tests and the integration tests below against it, run them, and confirm they **fail** for the confusion cases. Record the failing test names and the key assertion output in this task document's Status/notes. This is the reproduction evidence the phase gate's security review will read.
   - Only then replace the delegate with the real implementation (requirement 3).
2. **Tests (they must exist and pass after the fix):**
   - Unit tests (`authz_test.go`, stubbed, no DB), built with `NewWithStubOpReg` plus `SetGrantOrOwnFn`:
     - (a) **Owner-of-entity-equal-to-typeID denied.** No wildcard grant. The grant-or-own stub returns `true` exactly when `targetEntityID == typeID`, which simulates owning, or holding a grant over, entity N where N equals the type id. `AuthorizeType(ctx, "create", typeID)` returns `ErrForbidden`, and the grant-or-own stub is **never invoked** (assert on a call counter).
     - (b) **Wildcard holder allowed.** The wildcard stub returns `true`, so the result is `nil`.
     - (c) **No actor.** The result is `ErrUnauthenticated`.
     - (d) **Sudo actor.** The wildcard check receives the sudo actor id, as `TestAuthorize_SudoActor_WildcardDoesNotEscalate` does for `Authorize`.
     - (e) **Unknown operation.** A non-admin gets `ErrForbidden`, and a wildcard-manage holder is allowed, mirroring `Authorize`'s unknown-slug fallback.
     - (f) **Bad typeID.** `typeID <= 0` returns `ErrForbidden`, even for a wildcard holder.
     - (g) **DB error.** A wildcard-check DB error propagates rather than being swallowed into a denial.
     - (h) `list` behaves the same way as `create`.
   - Integration tests (real composed schema, throwaway Postgres):
     - **Confusion repro.** Resolve `T = types.id` for `authz_actor_group` and for `natural_person` (both are in mod-users' composed schema). Build a non-wildcard actor `U` that has entity-level authority over entity id `T`:
       - If entity `T` exists, insert a targeted grant `(U, create, target_id = T)` with the existing `targetedGrant` helper. This exercises the `TargetChain` arm.
       - If entity `T` does not exist, insert an entity row with explicit `id = T` owned by `U`. Use a corporation-typed raw insert, since `owner_id` must be set at INSERT time and is immutable afterwards. Then advance the `entities` id sequence past `max(id)` (`setval(pg_get_serial_sequence('entities','id'), …)`) so later inserts cannot collide. This exercises the ownership arm.
       - Cover the ownership arm deterministically if you can. Otherwise document which arm each run exercises, and why.
       - Assert that `AuthorizeType(ctxU, "create", T)` returns `ErrForbidden`, and that a second, unrelated non-admin user is also forbidden.
     - **Characterization.** Assert that `Authorize(ctxU, "create", &T)` **still returns nil**, because `Authorize` treats `T` as entity `T` by contract. Comment that this is why callers must use `AuthorizeType`. This pins that `Authorize`'s entity semantics did not change.
     - **Wildcard.** A wildcard `manage` holder and a wildcard `create` holder are both allowed by `AuthorizeType` for `create`.
3. **Implementation** (`api/internal/authz/authz.go`):
   - Factor the shared prelude out of `Authorize` into one unexported helper used by both `Authorize` and `AuthorizeType`, so the two cannot drift: resolve the effective actor, run `SatisfiedBy` with the unknown-slug wildcard-manage fallback, then run the wildcard check. `Authorize`'s observable behavior must stay identical. Every existing unit and integration test passes **unmodified**.
   - `AuthorizeType` follows the design note's semantics exactly: `typeID <= 0` returns `ErrForbidden`, a wildcard grant allows, and anything else returns `ErrForbidden`. **It must never call `checkGrantOrOwn`/`checkGrantOrOwnDispatch`, and must never compare `typeID` against `entities.id`, `entities.owner_id`, or `grants.target_id`.** DB errors propagate.
   - Rewrite the package doc, the `Authorize` doc comment, and the nil-target-branch comment. That comment currently says type-level checks "fall through to checkGrantOrOwn", which documents the bug. The new text must state:
     - `Authorize`'s target is always an `entities.id`;
     - passing a `types.id` is a caller bug, and `Authorize` cannot detect it;
     - type-level `create`/`list` must use `AuthorizeType`, or callers holding only a `coreAuthz.Authorizer` must assert `TypeAuthorizer` and otherwise fall back to `Authorize(ctx, op, nil)`, **never** to `&typeID`;
     - no grant can target a type (`grants.target_id` references `entities`), so type-level authority is wildcard-only today.
4. **Exported surface** (`api/localAuthz/authz.go`): add the `TypeAuthorizer` interface with a doc comment covering the assert-or-nil-fallback consumer contract, and `var _ TypeAuthorizer = (*Authorizer)(nil)`. `Authorizer` stays a type alias, so `AuthorizeType` is reachable through it automatically. Keep `New` unchanged.
5. Do not change the mod-core interface, any schema or migration, or any file outside mod-users.

## Validation

- Status/notes in this task document record the red run: test names plus the failing assertion lines from the naive-delegate stage.
- `cd api && go build ./... && go vet ./... && go test ./internal/authz/... ./localAuthz/...` pass. Run `make preflight` first in a worktree.
- `make -C api lint` (or the repo's `make lint` scoped to `api`) is clean.
- Full `-tags=integration ./internal/authz/...` suite against a throwaway Postgres 16 container passes, both the new and the pre-existing tests. The container is removed afterwards.
- `grep -n 'checkGrantOrOwn' api/internal/authz/authz.go` shows no call reachable from `AuthorizeType`. Reviewer reads the function body.
- `grep -n 'fall through to checkGrantOrOwn' api/internal/authz/authz.go` returns nothing.
- `git diff` shows no change to existing test assertions in `authz_test.go`, `authz_integration_test.go`, `anonymous_actor_integration_test.go`, or `ssh_keys_integration_test.go`, apart from additions. A shared-prelude refactor must not need test edits.
- `git diff --stat` (excluding `plan/`) is limited to `api/internal/authz/` and `api/localAuthz/`.

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has landed: the integration `TestMain` honors `AUTHZ_DEV_PG_HOST`/`AUTHZ_DEV_PG_PORT` and skips the shared-container check.
- In a freshly reset integration DB, type ids are small, and only a few entities are seeded by migrations (for example the `anonymous` system actor). So entity `T` may or may not exist depending on test order, which is why requirement 2 handles both arms.
- `Authorizer.opReg` is a concrete `*authzapi.OperationRegistry`. Unit tests already work around this with `NewWithStubOpReg`/`buildStubOpReg` in `export_test.go`.

## References

- [type target design](../notes/type-target-design.md): root cause, decision, rejected alternatives.
- `api/internal/authz/authz.go`: `Authorize`, `checkWildcardGrant`, `checkGrantOrOwn`, and the dispatch hooks.
- `api/internal/authz/export_test.go`: the `SetWildcardGrantFn`, `SetGrantOrOwnFn`, and `NewWithStubOpReg` hooks.
- `api/internal/authz/authz_integration_test.go`: `seedUser`, `seedWildcardGrant`, `targetedGrant`, `seedOwnedCorporation`, `actorCtx`.
- mod-authz `model/migrations/0505_grants.sql` (`grants.target_id REFERENCES entities(id)`) and `0506_authz_create_operation.sql` (`manage` implies `create`).
- `docs-mf-standards/architecture/authorization-design.md`, call-shape table: the ambiguous convention.
- Origin finding `ilu6` (app-mfmanager, plan `managed-app-home-nav`, plan-scope store): the end-to-end reproduction.

## Checkpoint hints

- After the red tests are recorded against the naive delegate.
- After the shared-prelude refactor, with all existing tests still green.
- After the real `AuthorizeType`, the `localAuthz.TypeAuthorizer` interface, and the doc rewrites.

## Status

Outcome: succeeded (2026-10-05).

Red run (against the temporary naive delegate `return a.Authorize(ctx, op, &typeID)`), logs in `.flow/validation-logs/01-red-unit.log` and `02-red-integration.log`:

- Unit: `TestAuthorizeType_EntityOwnerOfIDEqualToTypeID_Denied/{create,list}`: `expected ErrForbidden for entity-N owner with typeID==N, got: <nil>` and `grant-or-own check must never be invoked by AuthorizeType, got 1 call(s)`. `TestAuthorizeType_BadTypeID`: `typeID=0 / -1 should return ErrForbidden even for a wildcard holder, got: <nil>`.
- Integration (types.id authz_actor_group=8, natural_person=3; ownership arm exercised in that run because entities 8 and 3 did not yet exist): `TestInteg_TypeTarget_EntityAuthorityDoesNotAnswerTypeCheck/{authz_actor_group,natural_person}`: `AuthorizeType(create, 8) for entity-8 authority holder: got <nil>, want ErrForbidden` (same for list and for id 3). `TestInteg_TypeTarget_OwnershipArm_Deterministic`: `AuthorizeType(create, 13) where 13 is an entity owned by the actor: got <nil>`. `TestInteg_TypeTarget_TargetedGrantArm_Deterministic`: `AuthorizeType(create, 15) with a targeted grant over entity 15: got <nil>`. `TestInteg_TypeTarget_WildcardHolders_Allowed`: bad typeID 0 / -1 allowed for wildcard manager.
- Arm coverage: the two deterministic tests pin each arm of `checkGrantOrOwn` regardless of DB state. In the full-suite run the type-id repro used the targeted-grant arm (entities 8 and 3 already existed); in the filtered red run it used the ownership arm.

After the fix: full unit suite, full `-tags=integration ./internal/authz/...` suite (throwaway postgres:16 container, removed afterwards), `go build`, `go vet`, `make -C api lint` all pass. Existing test files are unmodified except for additions to `authz_test.go`.

Files: `api/internal/authz/authz.go`, `api/internal/authz/authz_test.go`, `api/internal/authz/type_target_integration_test.go`, `api/localAuthz/authz.go`.

Decisions: shared `authorizePrelude` helper returns `(actor, opIDs, done, err)`; `AuthorizeType` checks actor presence (ErrUnauthenticated) and `typeID <= 0` (ErrForbidden) before any DB work, so an unauthenticated caller with a bad id still gets the 401-class error. `export_test.go` needed no change.
