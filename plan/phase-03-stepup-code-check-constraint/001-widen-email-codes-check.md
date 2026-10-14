# Widen email_codes Check

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Backlog item bc1h. `sendStepUpCode` in `api/internal/handlers/identities.go` inserts `purpose='credential_change'`, but `model/migrations/sql/0100_baseline.sql` allows only `login` and `verify_email`. The insert fails, is only logged, and the step-up request still answers 204 without a code. Fix with a new additive migration; do not edit the baseline.

## Requirements

1. Add `model/migrations/sql/0101_email_codes_credential_change.sql` (goose Up and Down). Up: drop the `email_codes` purpose CHECK and re-add it allowing `('login','verify_email','credential_change')`. Look up the auto-generated constraint name (`email_codes_purpose_check`) and confirm it against a live database; use `DROP CONSTRAINT IF EXISTS`. Down: delete any `credential_change` rows (they would violate the narrower check) then restore the original CHECK.
2. Confirm `model/migrations/migrate.go` picks up the new file (check how it embeds/globs) and that `make -C model compose` includes it.
3. Verify against a live database. Start a throwaway Postgres: `docker run -d --rm --name mod-users-pg-<unique suffix> -e POSTGRES_PASSWORD=x -p <free port>:5432 postgres:16`, apply the composed migrations (core, authz, users) with goose, then confirm: inserting a `credential_change` row succeeds, an invalid purpose still fails, and Down then Up round-trips. Stop and remove the container when done. Never run `make dev.start`, `dev.restart`, `dev.stop`, or `clean.data`.
4. If an integration test harness exists (`make test.integration`), add or extend a test that exercises `CreateEmailCode` with `credential_change` and `GetActiveEmailCode`. If none is practical, document the manual verification in the task report.
5. Make `sendStepUpCode` failure visible only if trivially possible without changing the 204 contract; otherwise leave behavior as is.
6. Run `sqlc` generation check: the CHECK change should produce no diff in `model/db`; confirm.

## Validation

- Migration applies cleanly on a fresh Postgres 16 and on top of an already-migrated 0100 database.
- Manual or automated insert of `credential_change` succeeds; insert of `bogus` fails with a check violation.
- `cd api && go test ./...` and `cd model && go build ./...` pass.
- The throwaway container is removed (`docker ps -a` shows no leftover container of that name).

## Metadata

architectural_impact: false

## Checkpoint hints

- After writing the migration file
- After live-database verification

## Status

Outcome: succeeded (2026-10-14).

- Added `model/migrations/sql/0101_email_codes_credential_change.sql`: Up drops and re-adds `email_codes_purpose_check` allowing `login`, `verify_email`, `credential_change`; Down deletes `credential_change` rows then restores the narrow check. Constraint name confirmed against live postgres:16.
- `model/migrations/migrate.go` embeds `sql/*.sql` and `make -C model compose` copies `migrations/sql/*.sql`, so the file is picked up with no code change.
- Live verification (throwaway container, since removed): core + authz + users 0100 applied, then 0101 on top of 0100; `credential_change` and `login` inserts succeed, `bogus` fails with a check violation; Down then Up round-trips (credential_change rejected after Down, accepted after Up).
- No integration harness covers `CreateEmailCode`/`GetActiveEmailCode` (`api` test.integration is scoped to `./internal/authz/...` against a shared container), so manual verification above stands in.
- `sqlc generate` produced no diff in `model/db`. `cd api && go test ./...` and `cd model && go build ./...` pass.
- Requirement 5 (surface `sendStepUpCode` failure) left unchanged to keep the 204 contract.
