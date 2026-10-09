# Fix List Migration Comments and Test Gaps

## Purpose and scope

This task fixes finding QpDq, recorded in this plan's `plan/findings.yaml` by the phase 9 review. The work targets the plan branch `plan/type-scoped-grants` and lands through the ordinary per-task loop.

Scope is two comment fixes and two test additions. Do not change production code or SQL.

## Requirements

1. **Handler comment.** In `api/internal/handlers/user_accounts.go`, extend the `List` handler comment so it also states that the result includes accounts held by corporations, that the grant is exact-type (`natural_person`'s type entity), and the trust guidance (grant `list` or `manage` on that type entity only to principals trusted with every account's email). A pointer to the `UserAccountService.List` doc comment is acceptable for the details, but the three points must be recognisable here. Comment lines only.
2. **Route comment.** In `api/internal/handlers/account_routes.go`, the comment above the `/user-accounts` list route says list/create require wildcard admin. Reword it: both are authorized at the type level (a wildcard grant or a grant on `natural_person`'s type entity). Comment lines only.
3. **Corporation-held account (test).** In `api/internal/authz/user_account_list_integration_test.go`, seed one `user_accounts` row whose `account_holder` is a corporation (use the existing corporation seeding helpers and the schema's `user_accounts` constraints; read the migrations to find a valid insert) and assert in the allow cases that its email appears in the result for a `list` holder. This pins the documented exposure. Deny cases keep asserting an empty result.
4. **Operation filter (test).** In `api/internal/authz/authz_integration_test.go`, in the type-grant subtest of `TestInteg_OwnerPredicate_ListSingleRowSymmetry`, add a fixture whose grant operation does not satisfy `read` (for example `create`, or `update`, on `corporation`'s type entity) and pin, via the expected-instances field, that both the list side and the single-row side return no instances for it. Keep the existing fixtures unchanged.
5. Do not name plan, phase, task or finding identifiers in code comments or docs.

## Validation

1. Mutation checks (revert each afterwards, and confirm `git diff` of non-comment production lines is empty):
   - Make `SearchUserAccounts` results exclude corporation-held accounts in a throwaway edit of the test's expectation path (or filter in the service temporarily): the new corporation-held assertion fails.
   - Temporarily drop the operation filter on the type-grant arm (`g.operation_id = ANY($3)` for the seeded type-entity target) in `checkGrantOrOwn`: the new non-read-operation fixture fails.
2. The full `-tags=integration` run of `./internal/authz/...` passes against the composed schema using the sibling plan-branch mode in the design note. `make -C api build test lint` passes.
3. `git diff` of `api/internal/handlers/*.go` shows comment lines only.
4. `grep -rn "type-scoped-grants\|phase-0" api/internal/handlers/user_accounts.go api/internal/handlers/account_routes.go` returns nothing.

## References

- Finding `QpDq` in this plan's `plan/findings.yaml` (stale comments and test gaps).
- `plan/phase-09-type-grant-list-symmetry-and-list-migration/001-production-type-grant-symmetry.md` and `002-user-account-list-authorize-type.md` (the work being refined).
- `plan/notes/users-type-grant-design.md` (design and sibling-build recipe).
