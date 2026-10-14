# Attempts Column And Queries

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Backlog item GDol, part 1: schema and query support for counting failed verification attempts per code and for a request cooldown. Design in [attempt limiting design](../notes/attempt-limiting-design.md). Depends on phase 3 (migration `0101` exists). Scope: `model/migrations/sql`, `model/queries/email_codes.sql`, regenerated `model/db`, and any fake query implementations that must satisfy the regenerated `Querier` interface.

## Requirements

1. Add `model/migrations/sql/0102_email_codes_failed_attempts.sql`: Up adds `failed_attempts INT NOT NULL DEFAULT 0` to `email_codes`; Down drops it.
2. In `model/queries/email_codes.sql` add `ClaimEmailCodeAttempt :one` (atomic increment, bounded by a max-attempts parameter, sets `consumed_at` when the bound is reached, returns no row when the code is already consumed or exhausted) and a query returning the creation time of the newest unconsumed, unexpired code for (user, purpose) for the cooldown check (a `:one` that may return no rows). Exact SQL per the design note.
3. Regenerate with the repo's sqlc workflow (`make -C model` target; check `model/Makefile`). Commit regenerated `model/db/*.go` as part of the task output.
4. Update every hand-written fake/mock of the query interface in api tests (grep for `GetActiveEmailCode` in `*_test.go`, for example `identities_stepup_test.go` and tests under `api/internal/handlers/auth`) so the packages still compile; implement the new fake methods with real semantics (increment, exhaustion).
5. Verify the migration and queries against a throwaway Postgres 16 (unique container name, removed afterward; never `make dev.*` or `clean.data`): the claim query increments, exhausts at the bound, and returns no row afterward.

## Validation

- Migration applies on a fresh database and up/down round-trips.
- `cd model && go build ./...`; `cd api && go build ./... && go vet ./...` pass.
- `cd api && go test ./...` still passes with updated fakes (behavior unchanged until task 002).
- sqlc regeneration is clean (rerun yields no diff).

## Metadata

architectural_impact: false

## Checkpoint hints

- After the migration
- After queries and regeneration
- After updating fakes

## Status

Outcome: succeeded (2026-10-14).

- Added `model/migrations/sql/0102_email_codes_failed_attempts.sql`; added `ClaimEmailCodeAttempt` (param `max_attempts`) and `GetLatestEmailCodeCreatedAt` to `model/queries/email_codes.sql`; existing email_codes queries now also select `failed_attempts` so they still return `db.EmailCode`. Regenerated `model/db`.
- Fakes updated: `api/internal/handlers/identities_stepup_test.go` (real increment/exhaustion semantics), `api/internal/handlers/auth/guards_test.go`, `api/internal/service/user_accounts_upgrade_test.go`.
- Validation: model and api build, vet, `go test ./...` pass; sqlc regen yields no further diff. DB check on throwaway postgres:16: 0102 up/down round-trips, claim increments, exhausts at bound, returns no row afterward. The full baseline needs external mod-core tables, so verification used the email_codes table definition plus 0101 and 0102 on a stub user_accounts table.
