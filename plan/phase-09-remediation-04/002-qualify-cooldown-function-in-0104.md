# Schema-Qualify The Cooldown Function In The 0104 Migration

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates finding `jwyA` in this plan's `plan/findings.yaml`: the `model/migrations/sql/0104_email_codes_cooldown_index.sql` migration references the cooldown function by an unqualified name, so its `ALTER FUNCTION` and the `RESET search_path` in its Down path do not reliably target the function in the schema that the pinned `search_path` uses.

The work targets the plan branch `plan/users-security-hardening` and lands through the ordinary per-task loop (implement, validate, review, and merge as for any other plan task). It is limited to qualifying the function reference in migration 0104. Migrations 0100 through 0103 are not edited.

## Requirements

1. The Up `ALTER FUNCTION` in `model/migrations/sql/0104_email_codes_cooldown_index.sql` targets `%I.create_email_code_with_cooldown(...)`, with the schema taken from `current_schema()`, which is the same schema used in the pinned `search_path`.
2. The Down `RESET search_path` likewise targets the function in `current_schema()`, using a `DO` block with `format` and `%I`, rather than an unqualified name.
3. No hardcoded `public` schema is introduced. The displaced-schema layout continues to work.
4. Migrations 0100 through 0103 are not edited.

## Validation

1. Against a uniquely named throwaway `postgres:16` container started with `docker run` and removed afterwards, apply the Up and Down migrations and confirm that `proconfig` shows the pinned `search_path` after Up and clears it after Down. Do not run `make dev.*` or `clean.data` on the shared Docker host.
2. The `api/internal/authz` email-code integration suite, including `displaced_guard_integration_test.go`, passes.
3. `go vet -tags integration ./...` passes.

## References

1. Finding `jwyA` in this plan's `plan/findings.yaml`: migration 0104 references the cooldown function by unqualified name in its `ALTER FUNCTION` and Down `RESET search_path`.

## Status

succeeded (2026-10-14). Up `ALTER FUNCTION` and Down `RESET search_path` in `model/migrations/sql/0104_email_codes_cooldown_index.sql` now use `%I.create_email_code_with_cooldown(...)` with `current_schema()` via `DO`/`format`. Validation: throwaway postgres:16 showed `search_path=pg_catalog, public, pg_temp` after Up and empty proconfig after Down; `go test -tags integration ./internal/authz/` ok; `go vet -tags integration ./...` clean. Note: the composed schema dir from sibling mod-core contained a duplicate version 0100 file, so validation used a copy excluding `0100_natural_person_owner_invariant.sql`.
