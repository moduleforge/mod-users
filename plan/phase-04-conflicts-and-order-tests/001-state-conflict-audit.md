# State Conflict Audit

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Classify every rejection that a mod-users handler or service raises after authorization: a 400, an `ErrInvalidInput`, a 404 for a referenced value, or an unexpected 500 that is really a state conflict. Each one becomes one of: a stateless 400 that should have moved earlier, a 409 (`apiresp.Conflict`), a 422 (`apiresp.Unprocessable`), a legitimate post-authorization 404, or a documented late 400. Follow audit axis 5 in [common section 2](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#2-per-handler-conformance-audit-procedure) and the planned classification in decision D15 of the [handler inventory](../notes/handler-inventory.md#planning-decisions).

## Requirements

1. Start from the "Post-authz errors" column of the [audit table](../notes/handler-inventory.md#audit-table), then re-grep `api/internal/handlers` and `api/internal/service` for `ErrInvalidInput`, `InvalidInput(`, `ErrNotFound`, `pgx.ErrNoRows` and unique-violation handling. Record one line per site in the audit table: route, site, old status, new status, and reason.
2. Implement the planned D15 classifications if the source confirms them:
   - **`PUT /user-accounts/{uuid}`, email unique violation.** Today this is a 500. Map it to `svc.ErrEmailTaken` (409 `users.email_taken`) after authorization, matching `Create`. Match the constraint name exactly, not every 23505 (sibling-plan lesson; see `/Users/zane/playground/moduleforge/mod-core/api/httpapi/apps.go`, `writeAppsMutationError`).
   - **`POST /apps/{uuid}/user-accounts`, a body `user_uuid` naming no account.** Use 422 via `apiresp.Unprocessable(FieldError{Field: "user_uuid", Code: "users.user_account_unknown", Message: ...})`.
   - **Path `{user_account_uuid}` misses on Remove/UpdateRoles.** These stay 404.
3. Classify, and do not silently re-status:
   - Remove/UpdateRoles on a non-member (silent success today);
   - the `StartLink` provider registry miss (404) and the `LinkAuthorizeURL` 400;
   - `RemovePassword` when no password exists;
   - the oidc provider `GET` unknown id;
   - any capability gap.

   If you think one of them should change, file a finding (`followups_add`, `plan_slug: users-conformance`) and leave the code alone.
4. Tests for every status you change, including a negative test for a unique violation on a different constraint, which must stay 500.
5. Append the wire changes (planned items 5 and 7, the 409/422 parts) to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go vet -tags=integration ./...
go test ./...
make lint
```

If Docker is available, run the Postgres integration suite (AGENTS.md recipe). Otherwise say so.

## Metadata

architectural_impact: false

## Assumptions

- Phase 3 has landed.
- The GUI's `useApiError` (from `@moduleforge/core-gui`) treats `unprocessable_entity` as an inline code, so a new 422 needs no GUI change.

## Status

Outcome: succeeded (2026-10-11).

- Audit: one line per post-authorization site is appended to `plan/notes/handler-inventory.md` under "Post-authorization classification (task 4.1)". Only two statuses changed.
- `PUT /user-accounts/{uuid}` and `POST /user-accounts`: a unique violation on `user_accounts_email_key` now maps to `svc.ErrEmailTaken` (409 `users.email_taken`) via `isEmailTakenViolation` (`errors.As` plus exact constraint name) in `api/internal/service/user_accounts.go`. Create's 409 never fired before (followup 4Olf); it is fixed by the same helper. Other 23505 constraints stay 500.
- `POST /apps/{uuid}/user-accounts`: a body `user_uuid` naming no account is 422 `users.user_account_unknown` (`api/internal/handlers/apps.go`). Path `{user_account_uuid}` misses on Remove/UpdateRoles stay 404. Non-member Remove/UpdateRoles silent success is pinned by tests, not re-statused.
- Classified, no change: `StartLink` registry 404 (config state, post-step-up) and 500 for internal faults, `RemovePassword` no-credential idempotent 204, oidc provider `GET` unknown id 404 (after credential), `Unlink` 409-before-404 (F6), ssh-key revoke key miss masked 403 (F5 decided). Findings filed: `Unlink` ordering (v2BM) and OIDC link-mode insert race (8VhV).
- Tests: `api/internal/service/user_accounts_conflict_test.go`, `api/internal/handlers/apps_membership_conflict_test.go`, `api/internal/authz/user_account_update_conflict_integration_test.go`; `fakeTx` in `user_accounts_upgrade_test.go` gained an `execErr` hook.
- Wire notes appended to `plan/notes/wire-risk.md`.
- Validation: gofmt clean, `go vet` (plain and `-tags=integration`), `go test ./...`, `make lint` all pass. Postgres integration suite (`-tags=integration -p 1 ./internal/authz/...`, throwaway container) passed with the new tests running (not skipped); `internal/config` integration suite not run.
