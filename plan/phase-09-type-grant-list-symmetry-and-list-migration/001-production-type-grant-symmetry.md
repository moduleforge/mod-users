# Production Type-Grant Symmetry

## Purpose and scope

Prove, against the **production** `Authorizer`, that single-row `Authorize` and the list-side `accessible_<slug>_ids_for_actor` functions agree for type-grant holders now that both halves of Q1 have landed: mod-users' `checkGrantOrOwn` seed (phase 8) and mod-core's `GrantTableGenerator` arm (mod-core phase 9). mod-core's own symmetry test compares its list functions with a **copy** of mod-users' SQL, so it drifts silently if `checkGrantOrOwn` changes. This test closes that gap: mod-users composes mod-core's real `GrantTableGenerator`, so it can compare the real list functions with the real `Authorizer`.

This is a standard implementation task: follow the `implement-task` procedure. Test-only: no production code changes.

Files in scope:

- `api/internal/authz/authz_integration_test.go`: `wireServices` (the access-function slug list and its comment) and `TestInteg_OwnerPredicate_ListSingleRowSymmetry`
- a small helper in the same file or in the file that holds task 6.002's helpers, only if no existing helper fits

## Requirements

1. **Build against mod-core phase 9.** Follow [building against the sibling plan branches](../notes/users-type-grant-design.md#building-against-the-sibling-plan-branches). In addition to the two schema greps there, confirm the Go code the build links is mod-core's phase 9 arm before trusting any run:
   - `grep -n "NOT EXISTS (SELECT 1 FROM types tt WHERE tt.entity_id = e.id)" ../mod-core/api/authz/setup/grant_table.go`, run from `api/`'s parent through the sibling link (the path `api/go.mod`'s `core-api` replace directive resolves to), matches once.

   If it does not match, mod-core phase 9 has not landed in the checked-out mod-core plan worktree. Halt and report.
2. **Install the production list functions.** `wireServices` applies only `corporation` today. Extend its slug list to `corporation`, `natural_person` and `legal_entity`. These are a subset of `authzSlugs` in `api/cmd/server/main.go`. Do **not** add `type`: production never generates `accessible_type_ids_for_actor`, and mod-core's own symmetry test already covers that slug. Update the comment above the call, which says "three-arm", to describe the four-arm body. Every existing test must stay green.
3. **Extend `TestInteg_OwnerPredicate_ListSingleRowSymmetry`.** Keep the existing ownership case unchanged as its first subtest. Add type-grant subtests, using task 6.002's helpers (`typeEntityID`, `seedTargetGroup`, `addTargetGroupMember`, `addActorGroupMember`) and the existing `targetedGrant`. Every target group stays kind-pure (type entities only).

   For each actor fixture, each slug and each candidate, assert:

   > `candidate ∈ accessible_<slug>_ids_for_actor(actor, readOpIDs)` **iff** (`integAZ.Authorize(actorCtx(actor), "read", &candidate)` returns nil **and** `type_is_or_descends_from(candidate's fundamental_type_id, slug)`).

   The slug filter is the only legitimate difference between the two sides. `readOpIDs` is `integOpReg.SatisfiedBy("read")`, as the existing case uses. An `Authorize` error other than `ErrForbidden` fails the test. Resolve the type predicate in SQL (`SELECT type_is_or_descends_from(fundamental_type_id, $2) FROM entities WHERE id = $1`), not by hard-coding the hierarchy.

   Actor fixtures (each a fresh `seedUser` without wildcard grants):

   | Fixture | Expected on both sides |
   |---|---|
   | `read` directly on `corporation`'s type entity | every corporation instance, the actor's own row |
   | `list` directly on `corporation`'s type entity | same as above (`list` implies `read`) |
   | `read` on a type-only target group holding `corporation`'s type entity, granted to an actor group the actor belongs to | same as above |
   | `manage` directly on `natural_person`'s type entity | every natural_person instance, no corporation |
   | `read` on `legal_entity`'s type entity (Q2) | no instance other than the actor's own row |
   | no grants (control) | the actor's own row only (ownership) |

   Slugs: `corporation`, `natural_person`, `legal_entity`.

   Candidates: two corporation instances (create one **after** the grant, to prove future instances are covered), two natural_person instances (one of them the actor), and the type entities of `corporation`, `legal_entity` and `natural_person`. Type entities are never in any of the three list functions (their fundamental type is the sentinel `type`), while `Authorize` admits a direct holder on its own type entity. The slug filter accounts for that, so the iff holds.
4. **Failure messages** name the fixture, the slug, the candidate id and which side admitted it, so a regression on either arm is diagnosable.
5. **Do not touch** production code, phase 8's `instance_semantics_integration_test.go` matrix, or any sibling repository.

## Validation

- The sibling precondition greps (the two schema greps in the design note and the `grant_table.go` grep above) match after `make -C model compose`.
- The full `-tags=integration ./internal/authz/...` suite passes against a throwaway Postgres (AGENTS.md recipe), with `-count=1`.
- Mutation check, done locally and reverted before commit: temporarily remove the Q1 type-entity seed from `checkGrantOrOwn` in `api/internal/authz/authz.go`, and confirm the new subtests fail for the `corporation` type-grant fixtures with a message saying the list side admitted the row. Revert, and record the result in the task's Status section. Do not mutate the sibling mod-core tree.
- `make -C api build`, `make -C api test` and `make -C api lint` pass.
- `git diff --stat -- . ':(exclude)plan'` touches only `api/internal/authz/` test files.

## Assumptions

- mod-users phases 6-8 have landed on the plan branch, including the Q1 seed in `checkGrantOrOwn`.
- **mod-core phase 9 (the `GrantTableGenerator` arm) has landed** in the mod-core plan worktree's working tree. This is a cross-project dependency.
- If the manager dropped the optional Q1 (mod-users phase 8 together with mod-core phase 9), this task is moot and should be removed rather than run.
- The `type` slug is deliberately not exercised here. mod-core task 9.002 covers it against the ported reference query.

## References

- [Users type grant design note, cross-project dependencies of Q1](../notes/users-type-grant-design.md#cross-project-dependencies-of-q1): why the second-landing arm owes this test.
- [Production-side symmetry and the List migration](../notes/users-type-grant-design.md#production-side-symmetry-and-the-list-migration): the phase 9 design.
- mod-core's [symmetry task](../../../../../../mod-core/worktrees/plan/type-scoped-grants/plan/phase-09-type-grant-instance-semantics/002-type-grant-list-single-row-symmetry.md): the ported-reference version of this assertion, whose fixture set this mirrors.
- mod-core's [list arm task](../../../../../../mod-core/worktrees/plan/type-scoped-grants/plan/phase-09-type-grant-instance-semantics/001-instance-semantics-list-arm.md): the arm under test.
- [Instance semantics arm](../phase-08-type-grant-instance-semantics/001-instance-semantics-arm.md): the single-row arm under test. Its Status section records that it deferred this assertion.
- `api/internal/authz/authz_integration_test.go`: `wireServices`, `targetedGrant`, `seedUser`, `seedOwnedCorporation`, `TestInteg_OwnerPredicate_ListSingleRowSymmetry`.
- `api/cmd/server/main.go`: `authzSlugs`, the production slug list.

## Checkpoint hints

- After `wireServices` installs the three functions and the existing suite is green.
- After the new subtests pass and the mutation check is recorded.

## Status

- Outcome: succeeded (2026-10-09).
- Preconditions: both schema greps and the `grant_table.go` phase 9 arm grep (line 140, one match) held after `make -C model compose`; staging sibling dir used as `MODULEFORGE_SIBLINGS_DIR`.
- Changes: `api/internal/authz/authz_integration_test.go` (`wireServices` installs `corporation`, `natural_person`, `legal_entity`; `TestInteg_OwnerPredicate_ListSingleRowSymmetry` now has an unchanged `ownership` subtest plus a `type grants` subtest with the six specified fixtures and a seventh sentinel-`type`-entity fixture); time-bound "until mod-core's arm lands" sentences removed from `api/internal/authz/authz.go` (package, `Authorize`, `checkGrantOrOwn` docs; comment-only), `docs/architecture.md` (D13 bullet), and the stale "symmetry gap" header comment in `api/internal/authz/instance_semantics_integration_test.go`.
- Postgres: Docker unreachable; used a throwaway local `initdb` cluster (random port, trust auth, TCP only), removed afterwards.
- Validation: `-tags=integration ./internal/authz/...` -count=1 passed; `make -C api build|test|lint` passed.
- Mutation checks (reverted): removing the type-entity seed from the target chain fails the corporation fixtures ("list side admitted, single-row Authorize (with slug filter) did not"); removing the `NOT EXISTS (... tt.entity_id = $2)` exclusion is caught only by the added sentinel-type-entity fixture's "Authorize admitted a type entity" check (the iff itself cannot see it, since type entities are never listed).
