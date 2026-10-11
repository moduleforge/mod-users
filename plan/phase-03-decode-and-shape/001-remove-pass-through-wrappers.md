# Remove Pass Through Wrappers

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Delete the three error-writer wrappers that only forward to `apiresp.WriteError`, per decision D13 in the [handler inventory](../notes/handler-inventory.md#planning-decisions) and the "delete pass-through error wrappers" item of [common section 6](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#6-standard-per-plan-task-template). This is a mechanical refactor with no behavior change. It closes project followup 6ZEz.

## Requirements

1. Delete these wrappers and replace every call with `apiresp.WriteError(w, r, err)`:
   - `writeAuthzError` (`api/internal/handlers/errors.go`; delete the file);
   - `writeServiceError` (`api/internal/handlers/user_accounts.go:49`);
   - `writeCoreServiceErr` (`api/internal/handlers/self.go:155`).

   Callers today are in `apps.go`, `assume.go`, `identities.go`, `oidc_providers.go`, `self.go` and `user_accounts.go`.
2. Fix any comment that names a deleted wrapper. One example is the ZVum comments in `identities.go` and `oidc_providers.go` that say "mirroring the writeServiceError/svc.ErrEmailTaken precedent". Reword them to describe the precedent without the deleted name. Keep the `followup ZVum` marker text exactly, because `api/scripts/check-server-error-literals.sh` keys on it. The comment at `api/internal/service/user_accounts.go` that mentions `writeServiceError` is updated the same way.
3. No status, code or message may change. If a test depended on a wrapper by name, update the test without changing what it asserts.

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go test ./...
make lint                       # carve-out check still reports 4/4
grep -rn 'writeAuthzError\|writeServiceError\|writeCoreServiceErr' --include='*.go' .   # no hits
test ! -e internal/handlers/errors.go
```

## Metadata

architectural_impact: false

## Assumptions

- Phase 2 has landed. Its handler edits in `user_accounts.go` and `assume.go` may have already removed some calls.

## Status

- Outcome: succeeded (2026-10-11).
- Validation: gofmt clean; `go vet ./...` and `go vet -tags=integration ./...` pass; `go test ./...` passes (no FAIL lines); `make lint` passes with `check-server-error-literals` reporting 4/4 carve-outs; no `writeAuthzError`/`writeServiceError`/`writeCoreServiceErr` references remain in `*.go`; `api/internal/handlers/errors.go` removed. Logs under `.flow/validation-logs/`.
- Integration suite not run: the change is behavior-neutral and the task does not change service behavior.
- Files affected: `api/internal/handlers/errors.go` (deleted), `api/internal/handlers/user_accounts.go`, `api/internal/handlers/self.go`, `api/internal/handlers/apps.go`, `api/internal/handlers/assume.go`, `api/internal/handlers/identities.go`, `api/internal/handlers/oidc_providers.go`, `api/internal/handlers/user_accounts_authz_test.go`, `api/internal/service/user_accounts.go`.
- Decisions: test functions renamed `TestWriteServiceError_Mapping` to `TestWriteError_ServiceErrorMapping` and `TestWriteAuthzError_Mapping` to `TestWriteError_AuthzUnclassifiedMapsTo500`, since the names referenced deleted wrappers; assertions unchanged. ZVum comments reworded to cite the `svc.ErrEmailTaken` precedent without the deleted name; the `followup ZVum` marker text is unchanged.
