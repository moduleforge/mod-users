# Audit And Migrate mod-users Type-Level Authorize Call Sites

## Purpose and scope

**Security task.** Audit every `Authorize` call site in mod-users, and every exported API surface that carries an authorizer. Migrate each call that passes a `types.id` to the type-level API from task 002, and add regression tests proving the bypass is closed at the service layer. Implement with the standard `implement-task` procedure, test-first. The planning-time audit is in [call site audit](../notes/call-site-audit.md). It found exactly one vulnerable site, `UserAccountService.Create` (`api/internal/service/user_accounts.go`, `typeRes.IDForSlugMust("natural_person")` → `s.az.Authorize(ctx, "create", &typeID)`). Re-verify it, because line numbers drift.

Scope: `api/internal/service/` (the helper, `user_accounts.go`, and tests), plus regression tests in `api/internal/authz/` integration tests if service wiring is practical there. Do not edit files outside mod-users.

Docker host is shared. Integration runs use only a throwaway Postgres 16+ container (unique name, random port), per the `authz_integration_test.go` header recipe. Never run `make dev.start` or touch `users-module-postgres`.

## Requirements

1. **Audit.** Re-run `grep -rn --include='*.go' -E '\.Authorize\(' api model | grep -v _test.go`, and separately trace every value derived from `IDForSlug`/`IDForSlugMust`/`types` lookups to see whether it reaches an `Authorize` target. Classify each site as entity, nil, or type. Also confirm that the exported surfaces `api/localAuthz`, `api/usersservice`, `api/handlers/handlers.go`, and `api/auth` do not themselves pass type ids. Record the final classification table in this task document's Status/notes. Any additional type-id site found must be migrated the same way. If such a site's semantics are unclear, halt and report.
2. **Helper.** Add one unexported helper in `api/internal/service`, for example `authorizeType(ctx, az coreAuthz.Authorizer, op string, typeID int64) error`. It type-asserts a structural `interface{ AuthorizeType(context.Context, string, int64) error }` (or `localAuthz.TypeAuthorizer` if importing it creates no import cycle; prefer the structural form). It calls `AuthorizeType` when the assertion succeeds, and otherwise calls `az.Authorize(ctx, op, nil)`. **It must never fall back to `&typeID`.** Its doc comment states why: decorators and test stubs may not implement `AuthorizeType`, and a nil target is the core contract's "no specific target", which is wildcard-only in mod-users' implementation.
3. **Migrate** `UserAccountService.Create` to `authorizeType(ctx, s.az, "create", typeID)`. Keep the `typeID` resolution. Authorization must still happen before any data access (AGENTS.md convention) and after input validation, as now. Update the inline comment.
4. **Regression tests.**
   - Service unit tests (no DB) for `UserAccountService.Create`, written red first against the unmodified `user_accounts.go`. Record the failures in Status/notes.
     - (a) A stub implementing both methods, where `Authorize` returns nil for any non-nil target (simulating an actor who owns entity `typeID`) and `AuthorizeType` returns `ErrForbidden`. `Create` returns `ErrForbidden`, and the stub saw no `Authorize` call with a non-nil target.
     - (b) A plain `coreAuthz.Authorizer` stub with no `AuthorizeType`. `Create` calls `Authorize` with a **nil** target, never `&typeID`. Deny that call so `Create` returns before the transaction.
     - (c) A stub allowing via `AuthorizeType`, which proceeds past authorization. Only if this is feasible without a DB. Otherwise assert ordering through (a) and (b) and note why.
     - Use or extend the existing service test stubs (`allowAllAuthorizer` in `user_accounts_upgrade_test.go`, `recordingAuthorizer` in `ssh_keys_test.go`).
   - Integration regression (throwaway Postgres) where practical. A non-wildcard actor with entity-level authority over entity id `T = types.id('natural_person')` (same construction as task 002's repro) is denied by the type-level path the service now uses. A wildcard `manage` holder is allowed. If wiring `UserAccountService` into the `internal/authz` integration package is impractical (for example, an import cycle), assert at the `authorizeType`-equivalent level against the real `Authorizer`, and record why.
   - Confirm the existing handler tests (`api/internal/handlers/user_accounts_authz_test.go`) and service tests still pass unmodified.
5. Do not change the `Authorize` call sites the audit classifies as entity or nil.

## Validation

- Status/notes contain the final audit table and the red-run evidence from requirement 4.
- `grep -rn --include='*.go' 'IDForSlugMust' api | grep -v _test.go` shows that no resolved type id flows into `.Authorize(` (verified by reading each hit), and `grep -n 'Authorize(ctx, "create", &typeID)' api/internal/service/user_accounts.go` returns nothing.
- `cd api && go build ./... && go vet ./... && go test ./...` pass. Run `make preflight` first in a worktree.
- `make lint` for `api` is clean.
- The full `-tags=integration ./internal/authz/...` suite passes against a throwaway Postgres 16 container, which is removed afterwards.
- `git diff --stat` (excluding `plan/`) is limited to `api/internal/service/` and, if used, `api/internal/authz/*_test.go`.

## Metadata

architectural_impact: true

## Assumptions

- Task 002 has landed: `(*authz.Authorizer).AuthorizeType` and `localAuthz.TypeAuthorizer` exist.
- In production, `UserAccountService.az` is the mod-users `Authorizer`, or a decorator around it. The helper's nil fallback keeps decorated wiring fail-closed.

## References

- [call site audit](../notes/call-site-audit.md): the planning-time classification.
- [type target design](../notes/type-target-design.md): "Caller pattern: assert, or else fall back to a nil target".
- `api/internal/service/user_accounts.go`: `UserAccountService`, `NewUserAccountService`, and `Create`.
- `api/internal/service/user_accounts_upgrade_test.go` and `api/internal/service/ssh_keys_test.go`: existing authorizer stubs.
- `api/cmd/server/main.go`: production wiring of `UserAccountService` (around `usersservice.NewUserAccountService`).

## Checkpoint hints

- After the audit table is recorded.
- After the red service tests are recorded.
- After the helper and the `Create` migration, with the service tests green.
- After the integration regression.
