# Authorization Engine Tests

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Write the unit and integration test matrix for the engine rewritten in task 001 (`rewrite-authorization-engine`). The matrix proves these semantics:

- the owned-scope arm
- the per-operation own arm
- the assurance gate
- `AuthorizeTypeIn`
- parity with mod-core's generated `accessible_*` list functions, which is the single-row half of followup `rHzV`

Suggested role: `plugins/flow/roles/developer-go.md`. No standard skill covers this task.

Scope: test files under `api/internal/authz/`, namely:

- `authz_test.go`
- `authz_integration_test.go`
- `type_grant_integration_test.go`
- `type_target_integration_test.go`
- `instance_semantics_integration_test.go`
- new owned-scope and own-arm suites

The task changes no production code. A defect found in `authz.go` is reported with a failing test, not fixed here, unless the fix is a one-line correction plainly within API surface 6.2. In that case, state the fix in the report.

Dependency: requires task 001. It can run in parallel with tasks 003, 004 and 005.

## Requirements

1. **Migrate existing suites.** Bring the five existing suites named above up to the new semantics:
   - grants carry `scope`
   - the actor is a profile
   - no `assume`

   Remove every `t.Skip` that task 001 added naming this task.
2. **Owned-scope suite (new file, for example `owned_scope_integration_test.go`).**
   - An exact `target_type_id` match passes for an instance of that type and fails for a subtype instance.
   - `include_subtypes` passes for a subtype instance.
   - `target_type_id` NULL passes for every type the owner owns.
   - The grant never reaches the owner's own record.
   - A grant held through an actor group passes.
   - A grant on owner A never reaches entities owned by owner B.
3. **Own-arm suite (new file, for example `own_arm_integration_test.go`).**
   - The own arm applies only when the actor is a user's personal profile. A party profile, a service account's profile and the anonymous profile get no own authority.
   - It applies only for operations with `ownership_confers`. Cover at least `read`, `update` and `credentials`, which confer, and `manage`, `impersonate` and `login`, which do not.
   - `impersonate` is never conferred on an entity the personal profile owns.
4. **Assurance gate.**
   - `ACR` below `min_acr` yields `users.step_up_required`.
   - The refusal satisfies the shared mod-core `apiresp` action-required type (`errors.As`), including when wrapped, and `apiresp.WriteError` renders it as 409 with action `users.step_up_required`.
   - `AuthTime` older than `max_auth_age` on `impersonate` and `credentials` yields the same error, and fresh `AuthTime` passes.
   - The system-actor context (`opctx.WithSystemActor`) and the anonymous context skip the gate.
   - A client-credentials context (`CredentialID != 0`, `SessionID == 0`) is gated.
5. **`AuthorizeTypeIn` matrix.** Cover each arm in isolation:
   - wildcard
   - exact type-entity grant
   - a type-entity grant on a supertype, which must not pass unless the engine's documented semantics say so
   - owned-scope with and without `include_subtypes`
   - the own arm with `ownerID` equal to and different from `profile_own_subject(actor)`
   - the deny case
6. **Parity with generated list functions.** Install mod-core's generator output for the test slugs on the same fixtures through `setup.ApplyFuncs`, with the slug list per U18 (including `user` and `profile`). For every (actor, operation, entity) triple in the owned-scope and own-arm fixtures, assert that `Authorize` and membership in the corresponding `accessible_*_ids_for_actor` result agree.
7. Unit tests in `authz_test.go` use the `export_test.go` stubs, including the owned-scope stub, and mod-core's `AuthContext`-based identities in place of `ordertest.Actor`, `ordertest.Sudo` and `ordertest.SudoOnly`.

## Validation

- `make test` passes.
- The authz integration suite passes against a throwaway Postgres per `AGENTS.md` (`go test -tags=integration -p 1 -count=1 ./internal/authz/...`).
- `make lint` passes.
- `grep -rn 't.Skip' api/internal/authz/ | grep -i 'authorization-engine-tests\|task 002'` returns nothing.
- `grep -rnE 'ordertest\.(Actor|Sudo|SudoOnly)\b|assume' api/internal/authz/*_test.go` returns nothing.
- The report lists each requirement 2 to 6 case with the test function that covers it.

## Assumptions

- Task 001 has landed. The engine compiles and existing suites are green or skipped with a pointer to this task.
- mod-core's access-function generator (mod-core phase 3) and `setup.ApplyFuncs` are available through the sibling links.

## References

- [API surface section 6.2](../notes/mod-users-api-surface.md#62-localauthz-binding): the semantics under test.
- [API surface section 6.6](../notes/mod-users-api-surface.md#66-test-helpers): test helpers.
- [Decisions](../notes/decisions-and-resolutions.md#planning-decisions): U14 and U18.
- [Work packages](../notes/work-packages.md#phase-users-authorization-engine): package 2.
- `AGENTS.md`, section "Authz integration tests against a throwaway Postgres".

## Checkpoint hints

- After the five existing suites are migrated and green.
- After the owned-scope and own-arm suites.
- After the assurance-gate and `AuthorizeTypeIn` matrix.
- After the parity suite.
