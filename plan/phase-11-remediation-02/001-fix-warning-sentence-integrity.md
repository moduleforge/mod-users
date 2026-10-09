# Fix Warning Sentence Integrity and Wording Precision

## Purpose and scope

This task fixes findings oDuA and HIBp, recorded in this plan's `plan/findings.yaml` by the remediation round 1 review. The work targets the plan branch `plan/type-scoped-grants` and lands through the ordinary per-task loop.

It is a comment and documentation change only. Do not change any production code, SQL or test logic.

## Requirements

1. **Sentence integrity (oDuA).** In `api/internal/authz/authz.go`, the package doc (around lines 56-71) and the `Authorize` doc (around lines 187-202) each have the "Escalation paths" block pasted into the middle of an existing sentence: "(a grant on the sentinel "type" entity ... confers nothing over type entities). Grant on type entities only to ...". Move the block so it follows "...confers nothing over type entities)." (or precedes the parenthetical), so the existing sentence reads intact. Then re-read all five warning locations for sentence integrity: the package doc, the `Authorize` doc and the `checkGrantOrOwn` doc in `api/internal/authz/authz.go`, the `Authorizer` doc in `api/localAuthz/authz.go`, and the warning block in `docs/architecture.md`. Check the rendered text with `go doc` for the package and for `Authorize`.
2. **Wording precision (HIBp).** In all five locations:
   - Replace the claim that `manage`, `grant` or `update` on the actor-group and target-group type entities are equivalent escalation paths with the accurate rule: adding a member needs `grant` on the member (and, for actor groups, `update` and `grant` on the group); a bare type-level `update` only permits removing members. `manage` confers all of it.
   - Replace "the anonymous actor type" with "the anonymous system actor, an instance of `system_actor`".
3. Reflow the renamed header comments in `api/internal/authz/type_grant_integration_test.go` and `api/internal/authz/instance_semantics_integration_test.go` that exceed the files' wrap width. Comment lines only.
4. Do not name plan, phase, task or finding identifiers.

## Validation

1. `go doc` output for the package and for `Authorize` (run from `api/` with the sibling staging directory as `MODULEFORGE_SIBLINGS_DIR`) reads as grammatical prose around the warning: the sentence "(a grant on the sentinel "type" entity ... confers nothing over type entities)" is contiguous.
2. `git diff` of `api/internal/authz/authz.go`, `api/localAuthz/authz.go` and the two test files shows comment lines only; `git diff docs/architecture.md` shows prose only.
3. `grep -n "anonymous actor type" api docs` returns nothing.
4. `make -C api build test lint` passes.
5. `grep -rn "type-scoped-grants\|phase-0" docs/architecture.md api/internal/authz/authz.go api/localAuthz/authz.go` returns nothing.

## References

- Finding `oDuA` in this plan's `plan/findings.yaml` (warning paragraph splits a sentence).
- Finding `HIBp` in this plan's `plan/findings.yaml` (wording precision).
- `plan/phase-10-remediation-01/001-complete-instance-warning-and-tests.md` (the change being corrected).

## Status

Outcome: succeeded (2026-10-09). Moved the escalation-paths block after the intact parenthetical in the package and `Authorize` docs, corrected the membership-authority wording and the anonymous system actor phrasing in all five warning locations, and reflowed the two test header comments. All validation checks passed (`make -C api build test lint`, greps, `go doc`). Files: `api/internal/authz/authz.go`, `api/localAuthz/authz.go`, `docs/architecture.md`, `api/internal/authz/type_grant_integration_test.go`, `api/internal/authz/instance_semantics_integration_test.go`.
