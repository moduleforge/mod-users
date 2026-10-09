# Complete Instance Warning and Strengthen Instance Tests

## Purpose and scope

This task fixes findings n4t2 and 16do, recorded in this plan's `plan/findings.yaml` by the phase 8 review. The work targets the plan branch `plan/type-scoped-grants` and lands through the ordinary per-task loop.

Scope is limited to the two findings: make the mandatory instance-semantics warning name the concrete escalation paths, and replace a vacuous test case with a real one while removing plan shorthand from test names. Do not change production authorization code.

## Requirements

1. **Warning completeness (n4t2).** In every location that carries the instance-semantics warning, add one or two sentences covering the paths below:
   - `api/internal/authz/authz.go`: the package doc, the `Authorize` doc comment and the `checkGrantOrOwn` doc comment (comment lines only; do not touch code).
   - `docs/architecture.md`: the warning block in the decision that describes instance semantics.
   - `api/localAuthz/authz.go`: the `Authorizer` doc comment (comment lines only).

   The added points:
   - `manage`, `grant` or `update` on the type entities of `authz_actor_group` and `authz_target_group` confers authority over every group of that kind, which is a path to groups that hold wildcard grants and therefore to privilege escalation. Verify which mod-authz operations gate group membership edits before wording this.
   - The same applies to every other type that has instances (name `app`, `system_actor` and the anonymous actor type where they exist).
   - The `assume` of every user account that `manage` on the `natural_person` type entity confers includes accounts that themselves hold wildcard or admin grants, so that grant is effectively full admin.

   Keep the three locations consistent with each other, keep the existing wording otherwise, and do not name plan, phase, task or finding identifiers.
2. **Real no-subtype-walk test (16do part 1).** In `api/internal/authz/instance_semantics_integration_test.go`, the "no child walk" subtest only passes because the exclusion denies the `legal_entity` type entity. Replace it with a real test: extend `registerTestType` to take a parent type (keeping existing callers working), register a concrete child type under a concrete parent type, seed an instance of the child, grant on the parent's type entity, and assert the grant does not reach the child's instance. Also assert the converse (a grant on the child type's entity does not reach an instance of the parent type).
3. **Extra guard (16do part 2).** Add a subtest on the test-only type (whose `types.id` differs from its `types.entity_id`) that grants through an actor group and a nested type-only target group, and asserts the instance is allowed and an instance of another type is not.
4. **Descriptive names (16do part 3).** Rename "Q1", "Q2" and "ilu6" shorthand in the file header, test function names, subtest names and comments in `instance_semantics_integration_test.go`, `type_grant_integration_test.go` and `type_target_integration_test.go` to descriptive names (instance semantics, no parent type walk, type id never read as an entity id). Keep function names unique within the package and keep the tests behaving identically.
5. No production code, SQL or migration changes.

## Validation

1. `grep -n "Q1\|Q2\|ilu6" api/internal/authz/*.go` returns no plan shorthand in the three test files (other matches, if any, are justified in the report).
2. Mutation check: temporarily make the `checkGrantOrOwn` seed also include the entity of the target type's parent type; the new no-subtype-walk test must fail. Restore the code afterwards and confirm `git diff` shows no change to non-comment production lines.
3. The full integration suite (`go test -tags=integration -p 1 -count=1 ./internal/authz/...`) passes against the composed schema, using the sibling plan-branch mode in the design note. `make -C api build test lint` passes.
4. `git diff -- api/internal/authz/authz.go api/localAuthz/authz.go` changes comment lines only.
5. `grep -rn "type-scoped-grants\|phase-0" docs/architecture.md api/internal/authz/authz.go api/localAuthz/authz.go` returns nothing.

## References

- Finding `n4t2` in this plan's `plan/findings.yaml` (warning omits escalation paths).
- Finding `16do` in this plan's `plan/findings.yaml` (vacuous child-walk case, shorthand names).
- `plan/notes/users-type-grant-design.md` (the Q1 section and required warning).
- `plan/phase-08-type-grant-instance-semantics/001-instance-semantics-arm.md` and `002-instance-semantics-warning-docs.md` (the work being refined).

## Status

- Outcome: succeeded (2026-10-09).
- Validation: shorthand grep clean across `api/internal/authz/*.go`; mutation (parent-type entity added to the `checkGrantOrOwn` seed) failed the no parent type walk and no subtype walk subtests and was restored (no non-comment production diff); full integration suite and `make -C api build test lint` passed against the composed plan-branch schema (throwaway local Postgres, Docker unreachable); `authz.go` and `localAuthz/authz.go` diffs are comment-only; no plan identifiers in the warned files.
- Files: `api/internal/authz/authz.go`, `api/localAuthz/authz.go`, `docs/architecture.md`, `api/internal/authz/instance_semantics_integration_test.go`, `api/internal/authz/type_grant_integration_test.go`, `api/internal/authz/type_target_integration_test.go`, `api/internal/authz/user_account_create_integration_test.go`.
