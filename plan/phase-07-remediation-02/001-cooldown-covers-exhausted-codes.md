# Base Email Code Cooldown On Any Recent Code And Make It Atomic

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates the findings `kcCG`, `2pnK`, `9NPY`, and `uFx5`, each recorded as a finding in this plan's plan/findings.yaml. The work targets the plan branch plan/users-security-hardening and lands through the ordinary per-task loop.

Dependency: runs first; edits the same files as document 002 (`002-single-use-code-consumption.md`), so they run sequentially.

## Requirements

1. The request cooldown for a (user, purpose) considers the newest code of any consumed state (unconsumed, consumed by exhaustion, or consumed by success) created within `EmailCodeRequestCooldown`; exhausting a code must not allow a new code to be minted before the cooldown elapses. This applies to both `sendEmailCode` (`api/internal/handlers/auth/emailcode.go`) and `sendStepUpCode` (`api/internal/handlers/identities.go`).
2. The cooldown check and code insert are atomic, so concurrent requests mint at most one code per cooldown window (for example a single `INSERT ... SELECT ... WHERE NOT EXISTS (recent code for user/purpose) RETURNING`, or a per-(user, purpose) transaction-scoped advisory lock). No email is sent when no row was inserted.
3. Query changes go only in `model/queries/email_codes.sql`, with `model/db` regenerated via the repo's sqlc workflow. No edit to `0100_baseline.sql`; any new migration is additive. Hand-written fakes are updated with matching semantics.
4. Public behavior is unchanged: request endpoints still answer 204 at about 200 ms; no status code, body, or route changes.
5. Comments describing the cooldown (handler comments and the query comment) accurately describe the new semantics.

## Validation

- Unit test (both endpoints): exhaust a code with 5 wrong guesses, request again within 60 s, and assert no new code is created and no email is sent; after the cooldown, a new code is created.
- Unit test: N parallel requests within the window yield exactly one created code and one email (run with `-race`).
- `cd model && sqlc` regeneration is clean (a rerun yields no diff); `cd api && go build ./... && go vet ./... && go test -race ./internal/handlers/... && make lint` pass.
- Execute the new SQL once against a throwaway `postgres:16` container with a unique name, removed afterwards (never make `dev.*` or `clean.data`): exhausted-then-request inside the window inserts no row; concurrent inserts yield one row.

## Metadata

architectural_impact: false

## References

- finding `kcCG`
- finding `2pnK`
- finding `9NPY`
- finding `uFx5`

## Status

Outcome: succeeded (2026-10-14).

- Added migration `model/migrations/sql/0103_email_code_cooldown_function.sql` (additive): plpgsql `create_email_code_with_cooldown` takes a per-(user, purpose) `pg_advisory_xact_lock`, then does `INSERT ... WHERE NOT EXISTS` over codes of any state created within the cooldown (a plain single-statement insert is not race-safe under READ COMMITTED).
- `model/queries/email_codes.sql`: new `CreateEmailCodeWithCooldown` query; `GetLatestEmailCodeCreatedAt` removed; `model/db` regenerated (rerun clean).
- Handlers `api/internal/handlers/auth/emailcode.go` and `api/internal/handlers/identities.go` use the atomic query; `pgx.ErrNoRows` means skip, no email sent. Comments updated.
- Fakes and new tests: exhausted-code cooldown and 20-way concurrent mint-once for both endpoints.
- Validation: `go build`, `go vet`, `go test -race ./internal/handlers/... ./internal/service/...`, `make lint` pass (log `.flow/validation-logs/03-go.log`). Function run against a throwaway `postgres:16` container (removed): exhausted-then-request inserted no row; 20 concurrent calls yielded one row; after cooldown a row is inserted.
