# User Account Masked Miss

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Close the user-account existence oracle. Today `UserAccountService.Get`, `Update`, `Delete` and `Assume` (`api/internal/service/user_accounts.go:470`, `:498`, `:588`, `:674`) load the account by UUID and return 404 before authorizing. Replace the fetch-then-authorize with one masked helper, per decision D2 in the [handler inventory](../notes/handler-inventory.md#planning-decisions). Its semantics mirror `AuthorizeResolve` ([common section 3](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#3-helper-reference-final-signatures), [common rule 5](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#4-rules-all-plans)). Reuse the same helper in `SSHKeyService.loadAuthorizedAccount`. This covers project followups CuAF(a), 433h and tbmN, and part of 1eYt.

## Requirements

1. Add the unexported helper described in D2 to `api/internal/service`. Behavior in order:
   1. `RequireAuthenticated`.
   2. A nil authorizer fails closed.
   3. Look the account up.
   4. A miss calls `Authorize(op, &0)`, discards the result and returns `apiresp.ErrForbidden`.
   5. Other DB errors are wrapped.
   6. A hit returns `Authorize(op, &accountHolder)` unchanged.

   Document it as the local equivalent of `AuthorizeResolve` for a non-entity UUID.
2. Use it in `Get` (`read`), `Update` (`update`), `Delete` (`delete`) and `Assume` (`assume`). In `Assume`, a missing actor becomes `ErrUnauthenticated` through the helper's `RequireAuthenticated`, and the sudo-account lookup (`GetUserAccountByAccountHolder`) moves after authorization. Keep each method's signature.
3. In `SSHKeyService.loadAuthorizedAccount` (`service/ssh_keys.go:93`), route the miss through the same helper, or the same semantics, so a miss makes the target-0 `Authorize` call. Map a `GetEntityByID` `ErrNoRows` to the masked 403. Keep the archived-holder masking and the operation names.
4. Handlers: delete the `pgx.ErrNoRows` to 404 branch in `UserAccountsHandler.Get` (`handlers/user_accounts.go:152`) and the `coreservice.ErrNotFound` to 404 branch in `AssumeHandler.Assume` (`handlers/assume.go:70`). Errors flow to `apiresp.WriteError`.
5. Tests:
   - for each of the 4 methods, a missing UUID and an existing-but-forbidden UUID both return `ErrForbidden`, and over HTTP give 403 with identical `error.code` and `error.message`;
   - a recording authorizer proves a miss calls `Authorize(op, 0)` exactly once;
   - `Assume` with no actor returns `ErrUnauthenticated`;
   - an ssh-key miss now records the target-0 call.

   Flip tests that asserted the old 404 or `ErrNotFound` (`api/internal/handlers/user_accounts_authz_test.go`, `assume_test.go`, `api/internal/service/*_test.go`). Grep the `-tags=integration` suites in `api/internal/authz/` for such assertions and update them.
6. Run the Postgres integration suite (AGENTS.md recipe) if Docker is available; otherwise say so.
7. Update the doc comments that promise `ErrNotFound` (for example `Assume`'s "Returns coreservice.ErrNotFound when the target UUID does not exist"). Append the wire changes (planned items 2, 12 and 13) to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go vet -tags=integration ./...
go test ./...
make lint
grep -n 'GetUserAccountByUUID' internal/service/user_accounts.go   # only inside the helper (and LoadByUUID, which is unauthorized by design; report its callers)
grep -n 'ErrNoRows' internal/handlers/user_accounts.go internal/handlers/assume.go   # no 404 mapping left
```

## Metadata

architectural_impact: false

## Assumptions

- Phase 1 has landed.
- `LoadByUUID` (`user_accounts.go:717`) loads without authorization by design. Report its callers, and do not change it unless a caller exposes the result over HTTP before authorizing. If one does, halt and report.

## Checkpoint hints

- After the helper and the four `UserAccountService` methods, with the service tests green.
- After the handler and ssh-key changes and the flipped tests.

## Status

- Outcome: succeeded (2026-10-11).
- Validation: gofmt clean; `go vet ./...` and `go vet -tags=integration ./...` clean; `go test ./...` green; `make lint` green; Postgres integration suite (`./internal/authz/...`, throwaway postgres:16 container) ran with 132 passing subtests/tests and 0 skips. Both greps pass (`GetUserAccountByUUID` only in `authorizeUserAccount` and `LoadByUUID`; no `ErrNoRows` in the two handlers).
- Source: `api/internal/service/user_accounts.go` (new `authorizeUserAccount`), `api/internal/service/ssh_keys.go`, `api/internal/handlers/user_accounts.go`, `api/internal/handlers/assume.go`; tests in `api/internal/service/user_accounts_masked_miss_test.go`, `api/internal/handlers/user_accounts_masked_miss_test.go`, `ssh_keys_test.go`, `user_accounts_upgrade_test.go`.
- `LoadByUUID` callers: none in non-test code (only the handler-test `userAccountServicer` interface and its stub); nothing exposes it over HTTP, so it is unchanged.
- Decisions: Assume's admin lookup uses `opctx.EffectiveActorEntityID` (manager decision F4); ssh-key revoke key miss stays a masked 403 (F5). ssh `loadAuthorizedAccount` now goes through the helper, so it requires an authenticated context and authorizes before the archived-holder check; the existing unit tests gained an authenticated context.
- Wire changes appended to the wire-risk note.
