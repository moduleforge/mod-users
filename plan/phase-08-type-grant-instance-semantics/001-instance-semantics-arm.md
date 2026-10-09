# Instance Semantics Arm

## Purpose and scope

Implement the optional Q1 "instance semantics" in mod-users' entity-level `Authorize`: a grant over a type's entity also confers the same operation over every instance of exactly that type. This is one extra seed in `checkGrantOrOwn`'s `TargetChain`, with the in-code warning and tests. The shape, its properties and the required warning are in the [Q1 section of the design note](../notes/users-type-grant-design.md#q1-instance-semantics-optional-last-phase).

This is a standard implementation task: follow the `implement-task` procedure.

Files in scope:

- `api/internal/authz/authz.go`
- a new `api/internal/authz/instance_semantics_integration_test.go`
- `api/internal/authz/authz_test.go`, only if a unit test needs adjusting

## Requirements

1. **The arm.** Extend `checkGrantOrOwn`'s SQL so `TargetChain` is seeded with the target **and** the entity of the target's exact `fundamental_type_id` (`types.entity_id`). The target-group walk then starts from both. Exclude the type-entity seed when the target is itself a type entity (`EXISTS (SELECT 1 FROM types WHERE entity_id = $2)`). Keep the ownership arm, the actor chain and the one-round-trip shape unchanged. The design note's SQL is a guide. Any equivalent form is fine, as long as the semantics hold.
2. **No other behaviour changes.**
   - `AuthorizeType` is unchanged: an instance grant still never satisfies a type-level check.
   - Nil-target `Authorize` is unchanged (wildcard-only).
   - No parent or child type walk.
3. **In-code warning.** Update the package doc, the `Authorize` doc comment and the `checkGrantOrOwn` doc comment (including its SQL sketch) to describe the arm. Include the warning points listed in the [design note](../notes/users-type-grant-design.md#required-warning-documented), in short form: instance-wide scope per operation closure, `manage` meaning full control including `assume` and `grant`, `list` implying `read`, type-only groups, no subtypes, never type entities, who should hold `grant` on a type entity, and the list-side gap until mod-core's arm lands.
4. **Integration tests** in the new file, using task 6.002's helpers (`typeEntityID`, `seedTargetGroup`, `addTargetGroupMember`, `registerTestType`). Every group stays kind-pure.

   | Case | Expected |
   |---|---|
   | `read` on `corporation`'s type entity: `Authorize(read, corp instance)` | allowed |
   | Same actor: `Authorize(update, corp instance)` | `ErrForbidden` |
   | Same actor: `Authorize(read, a natural_person instance)` | `ErrForbidden` |
   | `list` on `corporation`'s type entity: `Authorize(read, corp instance)` | allowed (`list` implies `read`) |
   | `manage` on `natural_person`'s type entity: `Authorize(assume, another user's entity)` | allowed. This pins the documented warning. |
   | `read` on a type-only group containing `corporation`'s type entity: `Authorize(read, corp instance)` | allowed |
   | Q2: `read` on `legal_entity`'s type entity: `Authorize(read, corp instance)` | `ErrForbidden` |
   | Grant via an actor group | allowed |
   | `grant` on the sentinel `type`'s entity: `Authorize(grant, corporation's type entity)` | `ErrForbidden` (type entities excluded) |
   | `read` on the sentinel `type`'s entity: `Authorize(read, corporation's type entity)` | `ErrForbidden` |
   | Direct `read` grant on `corporation`'s type entity: `Authorize(read, &corporationTypeEntity)` | allowed (the existing direct-target path) |
   | Instance grant on a corp instance: `AuthorizeType(create, corporation)` | `ErrForbidden` (unchanged) |
   | Test-only type (its `types.id` differs from its type entity's id): `read` on its type entity, then `Authorize(read, an instance of the test type)` | allowed. This proves the seed joins through `types.entity_id`, not `types.id`. |
   | Same actor: `Authorize(read, a corporation instance inserted at id == testType.types.id)` | `ErrForbidden` |
   | Target id with no `entities` row | `ErrForbidden`, no error |
   | Existing owner-predicate and list/single-row symmetry tests | still pass |

5. **Symmetry note.** Do not add a list/single-row symmetry assertion for type-grant holders. mod-core's matching `GrantTableGenerator` arm has not landed. Record that gap in the Status section, so the manager can attach the assertion to whichever arm lands second.

## Validation

- The sibling precondition greps in the [design note](../notes/users-type-grant-design.md#building-against-the-sibling-plan-branches) match after `make -C model compose`.
- The full `./internal/authz/...` integration suite passes against a throwaway Postgres (AGENTS.md recipe), with `-count=1`.
- `make -C api build`, `make -C api test` and `make -C api lint` pass.
- `grep -n "parent_id" api/internal/authz/authz.go` returns nothing.
- The diff of `AuthorizeType` and `checkTypeGrant` is empty: `git diff -U0 -- api/internal/authz/authz.go` shows no hunk inside either function, comments included.
- `git diff --stat -- . ':(exclude)plan'` touches only the files in [Purpose and scope](#purpose-and-scope).

## Metadata

architectural_impact: true

## Assumptions

- Phases 6 and 7 have landed.
- The manager has confirmed this optional phase is wanted. It is unconditional once landed: there is no configuration switch.
- mod-core's matching list-side arm, and mod-authz's `integrationAuthorizer` stand-in update, are planned elsewhere. This task does not wait for them.

## References

- [Users type grant design note, Q1 section](../notes/users-type-grant-design.md#q1-instance-semantics-optional-last-phase): the shape, the exclusion rationale and the warning.
- mod-core's [Q1 cost estimate](../../../../../../mod-core/worktrees/plan/type-scoped-grants/plan/notes/type-entity-design.md#instance-semantics-q1-cost-estimate): the list-side arm this must agree with.
- `api/internal/authz/authz.go:340-419`: `checkGrantOrOwn`.
- `api/internal/authz/authz_integration_test.go:834`: `TestInteg_OwnerPredicate_ListSingleRowSymmetry`.

## Checkpoint hints

- After the SQL change and doc comments, with the existing suite green.
- After the new integration matrix passes.

## Status

- Outcome: succeeded (2026-10-09).
- Changed: `api/internal/authz/authz.go` (TargetChain seeded with the target's exact-type entity via `entities.fundamental_type_id` -> `types.entity_id`, skipped when the target is itself a type entity; package, `Authorize` and `checkGrantOrOwn` docs carry the warning); new `api/internal/authz/instance_semantics_integration_test.go` (full task matrix plus mutation-checked exclusion and `types.id`-vs-`types.entity_id` tests). `AuthorizeType` and `checkTypeGrant` untouched.
- Validation: sibling precondition greps matched after `make -C model compose`; `./internal/authz/...` integration suite passed (`-count=1`, local throwaway Postgres 14, Docker unreachable); `make -C api build|test|lint` passed; no `parent_id` in `authz.go`; no diff hunk in `AuthorizeType`/`checkTypeGrant`.
- Symmetry gap: no list/single-row symmetry assertion was added for type-grant holders, because mod-core's `GrantTableGenerator` instance arm has not landed. The manager should attach the assertion to whichever arm lands second.
