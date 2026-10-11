# Full Validation

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This is the plan-wide validation pass:

- the full Go suite, including the Postgres integration suites;
- the module lint;
- the GUI typecheck and tests;
- checklist greps;
- a final read-through confirming every audit-table row is closed.

Follow the "Validation" item of [common section 6](/Users/zane/playground/moduleforge/mod-core/plan/waves/http-error-precedence-conformance/common-conformance-instructions.md#6-standard-per-plan-task-template). Fix nothing larger than a typo. File anything else as a finding (`followups_add`, `plan_slug: users-conformance`).

## Requirements

1. From `api/` ([build setup](../notes/handler-inventory.md#building-and-validating-from-a-task-worktree)), run:
   - `test -z "$(gofmt -l .)"`
   - `go vet ./...`
   - `go vet -tags=integration ./...`
   - `go test ./...`
   - `make lint`

   From `model/`, run `go test ./...`.
2. Run the Postgres integration suites: the AGENTS.md throwaway-container recipe for `./internal/authz/...`, and the same container with `AUTHZ_DEV_PG_HOST`/`AUTHZ_DEV_PG_PORT` for `./internal/config/...`. Use a unique container name and a random port, and run `docker rm -f` afterwards. If Docker is unavailable, report `partial`; do not skip silently.
3. GUI: if the dispatch permits dependency installation, run `bun install --frozen-lockfile` at the worktree root, then `cd gui && bun run typecheck && bun test` (see the GUI notes in the build setup). Also run root `make test` and report its result. If installation is not permitted, report the GUI checks and `make test` as not run.
4. Checklist greps, in `api/` non-test code. Each gives zero hits unless stated otherwise:
   - `grep -rn 'server.Decode(r,' .`
   - `grep -rn 'json.NewDecoder' internal/handlers/`
   - `grep -rn 'writeAuthzError\|writeServiceError\|writeCoreServiceErr' .`
   - `grep -rn 'func parseUUIDParam\|func parseUUIDPathParam' internal/handlers/`
   - `grep -rn 'AllowNotFound' internal/handlers/`
   - `grep -c 'r.Use(coreAuthz.RequireAuth)'` over the four authenticated `*_routes.go` files totals 8; `oidcconfig_routes.go` and `auth/routes.go` have none.
   - `scripts/check-server-error-literals.sh` reports 3/3.
   - Every `GetUserAccountByUUID` call in `internal/service/` is inside the masked helper or the documented `LoadByUUID`.
5. Read the [audit table](../notes/handler-inventory.md#audit-table) and mark each row closed, deferred (with a finding id), or open (report it). Also confirm that every followup in [Existing followups this plan touches](../notes/handler-inventory.md#existing-followups-this-plan-touches) has its stated disposition, so the manager can close it.
6. Confirm that `go.mod`, `go.sum`, `bun.lock` and `versions.lock.yaml` are unchanged versus the plan base: `git diff a2d704c -- '*.mod' '*.sum' bun.lock versions.lock.yaml` is empty.

## Validation

- Every command in Requirements 1-3 exits 0, or the report explains each non-zero exit or not-run.
- The report includes the checklist results and the closed audit table.

## Metadata

architectural_impact: false

## Assumptions

- Tasks 5.1 and 5.2 have landed.
