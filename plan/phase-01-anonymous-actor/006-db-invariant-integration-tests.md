# Database Invariant Integration Tests

## Purpose and scope

Prove, against a real Postgres with the composed schema applied, the database-enforced
invariants that make the anonymous system actor provably zero-authority: it can never own an
entity, can never join an actor group, can never hold a login identity, and — reaching past
`Authorize`'s `effectiveActor` early-return — is denied with `ErrForbidden` rather than
`ErrUnauthenticated`.

Invoke the standard `implement-task` skill. This is mixed Go + SQL work; the assertions are
about database behavior, so read the SQL design standards' testing-conventions section as well
as the Go standards.

Scope: integration tests only. No production code changes.

## Requirements

### 1. Placement and harness

Add the tests to the existing integration suite in `api/internal/authz/`, either as new test
functions in `authz_integration_test.go` or — preferred, for reviewability — as a new file
`api/internal/authz/anonymous_actor_integration_test.go` in the same `authz_test` package,
reusing that file's `TestMain`, `checkPrereqs`, `resolveHost`, and seeding helpers.

That suite already does exactly what is needed: `//go:build integration`, migrating mod-users'
composed schema (`model/schema/migrations`, core + authz + users, produced by
`model/Makefile`'s `compose` target) resolved relative to the test file's own location, and
skipping with a clear message when the composed directory or the database is absent. Do not
build a second harness.

Read the long header comment at the top of `authz_integration_test.go` before starting. In
particular: on a Docker Desktop for macOS host the shared `users-module-postgres` container is
reachable at `localhost`, **not** at the container's docker-network IP, so the suite must be run
with `AUTHZ_DEV_PG_HOST=localhost`. That comment says this cost prior tasks real time; do not
rediscover it.

Resolve the anonymous actor's entity id in the tests by slug
(`SELECT entity_id FROM system_actors WHERE slug = 'anonymous'`), never by a hardcoded id.

### 2. The assertions

This task owns the database half of the test list the proposal enumerates in its
"Migration / rollout notes — Phase 1" step 5.

**(a) Seeded state.** Exactly one `system_actors` row exists, with slug `anonymous`; its
`entities` row has `owner_id IS NULL` (the self-own default did not fire for this type); its
`types` row is concrete with parent slug `entity`.

**(b) Ownership guard on INSERT.** `INSERT INTO entities (fundamental_type_id, owner_id)` with
`owner_id` set to the anonymous actor's entity id raises. Assert on the exception, and prefer
asserting the Postgres error message contains
`a system actor may not own an entity` so a future refactor that silently drops the trigger is
caught. Use a type that never self-owns (e.g. `corporation`) as the inserted row's type, so the
only reason for failure is the guard.

**(c) Ownership guard on UPDATE.** `UPDATE entities SET owner_id = <anon entity id> WHERE id =
<some other entity>` raises. Note the expected message: `entities_no_system_actor_owner` sorts
alphabetically **before** `entities_owner_immutable`, and Postgres fires same-event row triggers
in name order, so the system-actor message is what surfaces — not `owner_id is immutable after
insert`. Assert accordingly, and comment the ordering fact in the test so the expectation is not
mistaken for a bug.

**(d) A normal entity is unaffected.** Inserting and updating an ordinary entity with a normal
(non-system-actor) `owner_id`, and inserting one with `owner_id NULL`, both still succeed. The
new trigger must not have made `entities` writes fail in general. This is the regression guard
for a `BEFORE INSERT OR UPDATE` trigger that fires on every row.

**(e) Actor-group membership is rejected.** `INSERT INTO authz_actor_group_members (group_id,
member_id)` with `member_id` = the anonymous actor's entity id raises the `0502` type-check
trigger's exception (`only legal_entity subtypes ... and authz_actor_group are allowed as actor
group members`). Seed a real `authz_actor_group` first, using the suite's existing helpers where
they exist. This is a structural guarantee that comes free from parenting `system_actor` under
`entity` rather than `legal_entity`; pin it so a future re-parenting is caught.

**(f) No login identity is possible.** `INSERT INTO user_accounts (account_holder, ...)` with
`account_holder` = the anonymous actor's entity id fails the FK to `legal_entities(entity_id)`.
This is the structural half of "no JWT can resolve to it": the local-issuer fast path in
`api/internal/auth/resolver.go` maps a JWT subject UUID to a `user_accounts` row, and none can
ever exist for this entity. (Task 004 owns the request-level half — a JWT for a nonexistent
account yields `ErrUserGone` → 401.)

**(g) `Authorize` returns `ErrForbidden`, not `ErrUnauthenticated`.** Build the real
`*localAuthz.Authorizer` the way the existing suite does, put the anonymous actor's entity id on
the context with `opctx.WithActor`, and call `Authorize`:
- with a `nil` target and a registered operation → `errors.Is(err, ErrForbidden)`, and
  explicitly **not** `ErrUnauthenticated`. Passing `effectiveActor`'s early-return
  (`authz.go:110-113`) is the entire point of the mechanism.
- with a real target entity the anonymous actor neither owns nor holds a grant on →
  `ErrForbidden`.
Assert `!errors.Is(err, ErrUnauthenticated)` explicitly in both cases, not just
`errors.Is(err, ErrForbidden)` — the distinction is the deliverable.

**(h) Zero platform authority, both paths.** The anonymous actor holds no grants
(`SELECT count(*) FROM grants WHERE actor_id = <anon id>` is `0`) and is in no actor group. If
the existing suite's list-side symmetry helper is reusable (see
`TestInteg_OwnerPredicate_ListSingleRowSymmetry`), assert that a generated
`accessible_<slug>_ids_for_actor(<anon id>, ...)` call returns the empty set for a slug that has
one — confirming the proposal's "both paths agree by construction". Do **not** add
`system_actor` to any access-function slug list to make this work; use an existing slug such as
`corporation`.

### Must not change

- No production code. If a test reveals a production defect, halt and report rather than fixing
  it inside a test task.
- Do not add `system_actor` to `authzSlugs` in `api/cmd/server/main.go` or to any
  `setup.ApplyFuncs` slug list, in production code or in test setup.
- Do not create a grant for the anonymous actor, even transiently inside a test. If a test needs
  to prove the boot assertion fires, that belongs in task 002's unit tests with a stubbed query,
  not against the real database.
- Do not modify `api/internal/authz/authz.go`, `anon_tokens`, or `user_accounts`.

## Validation

- The new tests carry `//go:build integration` and live in `api/internal/authz/` alongside the
  existing suite.
- `cd api && AUTHZ_DEV_PG_HOST=localhost go test -tags=integration -p 1 -v ./internal/authz/...`
  passes, with every assertion (a) through (h) present as a named test. Enumerate the test names
  in the task report.
- The pre-existing integration tests in that package still pass unchanged.
- The suite still skips cleanly (rather than failing) when `model/schema/migrations` is absent or
  no database is reachable — run it once without a database to confirm the skip path still works.
- `cd api && go vet -tags=integration ./internal/authz/...` clean.
- `make test.unit` still passes (the new file is excluded by the build tag).
- `grep -rn "system_actor" api/cmd/server/main.go` returns no match.
- `git diff --stat` shows changes confined to the integration test file(s).

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has landed and `make -C model compose` has been run, so the composed migrations
  directory contains `0101_system_actors.sql`.
- A Postgres instance is available (`make dev.start`), and `AUTHZ_DEV_PG_HOST=localhost` is set
  on macOS hosts.
- This task depends only on task 001 and may run concurrently with tasks 002, 004, and 005.
- `mod-authz`'s `authz_actor_groups` / `authz_actor_group_members` and `grants` tables are part
  of the composed schema (they are — the compose target copies `AUTHZ_DIR/migrations/*.sql`).

## References

- [Anonymous-actor architecture proposal](../notes/anonymous-actor-architecture-proposal.md) —
  §1 (the three structural guarantees: never self-owns, invisible to list paths, cannot join an
  actor group), §2 (the ownership guard), §"The authz duality — both paths, no special-casing"
  (why `ErrForbidden` and why the list path returns the empty set), §"Security considerations"
  item 6 (never loginable), §"Migration / rollout notes — Phase 1" step 5. **Authoritative.**
- `api/internal/authz/authz_integration_test.go` — the harness, its header comment (host
  resolution, composed-migrations prerequisite), the seeding helpers, and
  `TestInteg_OwnerPredicate_ListSingleRowSymmetry` as the model for assertion (h).
- `mod-core/model/migrations/0013_entity_ownership.sql` — the alphabetical trigger-ordering
  convention that determines which exception message surfaces in assertion (c).
- `mod-authz/model/migrations/0502_authz_actor_group_members.sql` — the member-type trigger and
  its exact exception text for assertion (e).
- `plan/phase-01-anonymous-actor/001-system-actor-migration.md` — the migration under test.
