# Public Auth Error Literals

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Convert the literal `server.Error`/`server.ErrorWithDetails` calls in the public auth handlers (`api/internal/handlers/auth/*.go`) to `apiresp` sentinels and constructors, wherever the replacement is equivalent. Equivalence and the keep-list are defined by decision D12 in the [handler inventory](../notes/handler-inventory.md#planning-decisions), and the literal-site table from task 1.1 is the worklist. Preserve the action-required envelopes and the credential-failure 401s ([common rule 3](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#4-rules-all-plans)). These routes are public: no `RequireAuth`, and no reordering beyond what the rule requires.

## Requirements

1. For each site the audit classified as "convert", do the following:
   - 500 `internal_error` becomes `apiresp.WriteError(w, r, fmt.Errorf("<handler>: <step>: %w", err))`. Keep the existing `slog` line if there is one, so no log is lost.
   - Required-field or format 400s become `apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{...}))`. Keep existing `users.*` detail codes. Where a literal had no details, add one per offending field. Record each new detail code; for example, "email and password are required" on login might use `users.email_required` and a new `users.password_required`.
   - 409 with details becomes `apiresp.Conflict(...)`.
   - Plain 404 "unknown provider" becomes `apiresp.ErrNotFound`.
   - `oidc.go`'s `Start` 400, which echoes `err.Error()` (`auth/oidc.go:158`), becomes `apiresp.ErrInvalidInput`, so it no longer echoes internal text.
2. Keep as literals, each with a one-line comment giving the reason:
   - credential-failure 401s with a specific message: login, email-code verify, password-reset confirm, and any other the audit lists;
   - any literal whose message `gui/src` or a test displays or asserts. Grep the message string before converting.

   Do not touch `api/internal/auth/middleware.go` (its six literals use 401 code `unauthorized`, which is distinct from apiresp's). Do not touch the remaining three `identities.go` ZVum carve-outs.
3. Keep every status, top-level code, header, redirect and cookie unchanged. `oidc.go`'s callback mostly redirects to the frontend; only its JSON error paths are in scope.
4. Update the tests that asserted converted messages so they assert status, code and details instead. Do not weaken the credential-401 tests.
5. Optional, only if it stays small: extend `api/scripts/check-server-error-literals.sh` to also scan `internal/handlers/auth/` with its own marker and expected count, so kept literals cannot drift. If you skip it, file a finding (`followups_add`, `plan_slug: users-conformance`).
6. Append the wire changes (planned item 11, with the exact list of new detail codes and changed messages) to the [wire-risk note](../notes/wire-risk.md#recorded-by-tasks).

## Validation

From `api/`, after the [build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree):

```bash
test -z "$(gofmt -l .)" || gofmt -l .
go vet ./...
go test ./...
make lint
grep -cE 'server\.Error(WithDetails)?\(w,' internal/handlers/auth/*.go   # equals the audit's keep-count per file
grep -n 'err.Error()' internal/handlers/auth/oidc.go                       # no response-body echo left
```

## Metadata

architectural_impact: false

## Assumptions

- Tasks 3.1-3.6 have landed. Task 3.2 already removed the decode-failure literals.
- `api/internal/handlers/auth` is a separate Go package from `api/internal/handlers`, but this task still runs after 3.6, because the plan keeps phase 3 sequential.

## Checkpoint hints

- After `register.go` and `login.go`.
- After `emailcode.go`, `reset.go` and `anonymous.go`.
- After `oidc.go`.
