# User Account List Authorize Type

## Purpose and scope

Migrate `UserAccountService.List` from `Authorize(ctx, "list", nil)` to the type-level check `authorizeType(ctx, s.az, "list", <natural_person types.id>)`, so a type-level `list` grant on `natural_person`'s type entity authorizes listing user accounts. This resolves followup `4dka`. The user decided to include it, accepting that **a type-level `list` grant exposes every account's email to its holder**. That warning must be documented wherever the rule is stated.

This is a standard implementation task: follow the `implement-task` procedure.

Files in scope:

- `api/internal/service/user_accounts.go` (`List` and its doc comment; the `authorizeType` doc comment if it names `Create` as the only caller)
- a new `api/internal/service/user_accounts_list_authz_test.go` (unit tests)
- a new `api/internal/authz/user_account_list_integration_test.go` (integration tests)
- `api/internal/handlers/user_accounts.go` (the `List` doc comment only, which says "(admin)")
- `docs/architecture.md`
- `docs/mod-users-spec.md`

## Requirements

1. **The code change.** In `List`, replace the nil-target `Authorize` call with `authorizeType(ctx, s.az, "list", s.typeRes.IDForSlugMust("natural_person"))`, exactly as `AuthorizeCreate` does for `create`. Authorization still happens before any query. Nothing else in `List` changes: `SearchUserAccounts` stays unscoped, and pagination is unchanged.
   - The `authorizeType` fallback is unchanged. An authorizer without `AuthorizeType` falls back to `Authorize(ctx, "list", nil)`, which is wildcard-only and fail-closed. Never pass `&typeID` to `Authorize`.
   - Rewrite the `List` doc comment and the inline "admin-only" comment. State that `list` is a type-level check on `natural_person`, satisfied by a wildcard grant or by a grant on `natural_person`'s type entity (directly or through type-only target groups, by the actor or its actor groups). Add the warning in short form: such a grant returns every user account, with its email, because the search is not scoped per row.
2. **The warning content.** Every place that documents the rule (the doc comment, `docs/architecture.md`, `docs/mod-users-spec.md`) must say at least:
   - A type-level `list` grant on `natural_person` (or `manage`, which implies it) lets its holder list **every** user account, including each account's email. The result is not filtered per row.
   - That includes accounts whose holder is a **corporation**. `user_accounts.account_holder` references `legal_entities`, so a holder can be a corporation, and the search does not filter by holder type. The grant names `natural_person`, but it exposes every account.
   - It is **exact-type**: a grant on `legal_entity`'s or `entity`'s type entity does not authorize it, and neither does any grant on an account or person instance, nor ownership of one's own entity.
   - Grant `list` or `manage` on `natural_person`'s type entity only to principals trusted with every account's email address.
   - With Q1 instance semantics landed, the same grant already confers `read` on every natural_person instance (`list` implies `read`). Link the Q1 decision's warning rather than restating it. If the manager dropped Q1, omit this point.
3. **Unit tests** in the new service test file, in the style of `user_accounts_create_authz_test.go` (reuse its stub authorizers and `testNaturalPersonTypeID` where they fit; it is in the same package). Use a stub `db.Querier` that records whether `SearchUserAccounts` was called (embed the interface and override that one method).
   - With an authorizer that implements `AuthorizeType` and allows: `List` calls `AuthorizeType("list", testNaturalPersonTypeID)` exactly once, never calls `Authorize` with a non-nil target, and returns the stub rows.
   - With `AuthorizeType` denying: the error propagates unchanged (`errors.Is` the stub's error) and `SearchUserAccounts` is never called.
   - With an authorizer that has only `Authorize`: `List` calls `Authorize("list", nil)` exactly once (the nil-target fallback), and a denial there propagates without a query.
   - An authorizer whose `Authorize` allows every non-nil target (an entity-authority holder, like `entityOwnerAuthorizer`) and denies nil is still denied by `List` through the fallback.
4. **Integration tests** in the new file, wiring the real `UserAccountService` to `integAZ` and a real `db.Querier` over `integPool`. Use task 6.002's helpers (`typeEntityID`, `seedTargetGroup`, `addTargetGroupMember`, `addActorGroupMember`) and the existing `seedUser`/`targetedGrant`. Keep every target group kind-pure. Seed at least two other users with distinct emails first.

   | Actor | Expected |
   |---|---|
   | `list` directly on `natural_person`'s type entity | allowed; the result contains the other users' accounts **with their emails**. This pins the documented warning. |
   | `manage` directly on `natural_person`'s type entity | allowed |
   | `list` on a type-only target group holding `natural_person`'s type entity, granted to an actor group the actor belongs to | allowed |
   | `create` only on `natural_person`'s type entity | `ErrForbidden` (`create` does not imply `list`) |
   | `list` on `legal_entity`'s type entity (Q2) | `ErrForbidden` |
   | `list` on `corporation`'s type entity | `ErrForbidden` |
   | `list` and `read` directly on another user's natural_person entity (an instance) | `ErrForbidden` |
   | ordinary user with no grants (owns only their own entity) | `ErrForbidden` |
   | wildcard `manage` holder | allowed (unchanged) |

   Denied cases must also prove no rows leak (the returned slice is empty or nil).
5. **`docs/architecture.md`.**
   - D12: replace the parenthetical about `UserAccountService.List` (phase 7 left it saying `List` stays on a nil target and wildcard-only). State that `List` now goes through `authorizeType` with `list` on `natural_person`, like `Create`.
   - Add a clearly marked **Warning** in D12, or immediately after it, with every point in requirement 2.
   - Fix any other statement in the file that says listing user accounts is wildcard-only or admin-only.
6. **`docs/mod-users-spec.md`**, Security requirements, "Authorization" bullet: add one or two sentences stating that listing user accounts is authorized at the type level (`list` on `natural_person`, wildcard or type grant), followed by the warning in short form (every account and its email, including corporation-held accounts), with a link to the architecture decision. Fix the "Admin-only endpoints" wording only as far as needed to stay accurate.
7. **Handler comment.** `List handles GET /v1/user-accounts (admin).`: say it requires `list` on `natural_person` at the type level. No handler code change.
8. Do not name plan, phase, task or followup identifiers in code or docs. Use inline links, not "see" pointers.

## Validation

- `grep -n 'Authorize(ctx, "list", nil)' api/internal/service/user_accounts.go` returns nothing, and `grep -n 'authorizeType(ctx, s.az, "list"' api/internal/service/user_accounts.go` matches once.
- `grep -n "&typeID" api/internal/service/user_accounts.go` returns only comment lines.
- The sibling precondition greps in the [design note](../notes/users-type-grant-design.md#building-against-the-sibling-plan-branches) match after `make -C model compose`.
- The full `-tags=integration ./internal/authz/...` suite passes against a throwaway Postgres (AGENTS.md recipe), with `-count=1`.
- `make -C api build`, `make -C api test` and `make -C api lint` pass.
- `grep -n -i "warning" docs/architecture.md` shows the List warning, and a manual read confirms every point in requirement 2 is in `docs/architecture.md`, in short form in `docs/mod-users-spec.md`, and in the `List` doc comment.
- `grep -n "List.*wildcard-only\|stays on a nil target" docs/architecture.md docs/mod-users-spec.md` returns nothing.
- `git diff -- api/internal/handlers/user_accounts.go` changes comment lines only.
- `git diff --stat -- . ':(exclude)plan'` touches only the files in [Purpose and scope](#purpose-and-scope).

## Metadata

architectural_impact: true

## Assumptions

- mod-users phases 6 and 7 have landed: `AuthorizeType` honours type grants, and D12 describes the arm. Phase 8 has landed, or the manager dropped it.
- This task does **not** depend on mod-core phase 9. It needs only the phase 6 arm, so it may run before task 001.
- The user's decision is binding: `List` uses a type-level `list` grant on `natural_person`, and the email exposure is accepted and documented, not mitigated. Do not add a per-row filter, a holder-type filter or a configuration switch. If you find a reason the migration is unsafe beyond what this document states, halt and report.
- There is no `GET /v1/user-accounts` list path in `api/openapi.yaml` today (the spec notes the `/v1/users` drift). Do not add one.

## References

- [Production-side symmetry and the List migration](../notes/users-type-grant-design.md#production-side-symmetry-and-the-list-migration): the design and the exposure analysis.
- Followup `4dka` in mod-users' `plan/followups.yaml`: the original finding.
- `api/internal/service/user_accounts.go`: `authorizeType`, `AuthorizeCreate`, `List`.
- `api/internal/service/user_accounts_create_authz_test.go`: the unit-test pattern and stub authorizers.
- `api/internal/authz/user_account_create_integration_test.go`: the service-over-real-Authorizer pattern.
- `model/queries/user_accounts.sql`: `SearchUserAccounts`, which is unscoped.
- `model/migrations/sql/0100_baseline.sql`: `user_accounts.account_holder` references `legal_entities`.
- [Instance semantics warning docs](../phase-08-type-grant-instance-semantics/002-instance-semantics-warning-docs.md): the Q1 warning this one links to.

## Checkpoint hints

- After the code change and unit tests, with `make -C api test` green.
- After the integration tests pass.
- After the docs.

## Status

Outcome: succeeded (2026-10-09). `UserAccountService.List` now calls `authorizeType(ctx, s.az, "list", natural_person id)` before any query; the doc comments, `docs/architecture.md` (D12 warning) and `docs/mod-users-spec.md` carry the exposure warning. Validation: unit tests, `-tags=integration ./internal/authz/...` (-count=1, throwaway local Postgres), `make -C api build|test|lint` all pass; sibling precondition greps matched.

Files: `api/internal/service/user_accounts.go`, `api/internal/service/user_accounts_list_authz_test.go`, `api/internal/authz/user_account_list_integration_test.go`, `api/internal/handlers/user_accounts.go` (comment only), `docs/architecture.md`, `docs/mod-users-spec.md`.
