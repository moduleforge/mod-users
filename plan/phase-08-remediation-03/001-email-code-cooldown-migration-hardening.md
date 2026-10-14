# Index The Cooldown Probe And Pin The Cooldown Function Search Path

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task remediates findings `f7fc` and `6DKr`, both recorded in this plan's `plan/findings.yaml`.

- `f7fc`: the any-state cooldown `NOT EXISTS` probe in `create_email_code_with_cooldown` has no supporting index, so it scans `email_codes` rather than performing an index lookup.
- `6DKr`: the cooldown function's name resolution is not pinned, so it can be affected by a displaced or shadowing schema layout.

The work is a single additive goose migration in the `mod-users` module. It targets the plan branch `plan/users-security-hardening` and lands through the ordinary per-task loop (implement, validate, review, commit on the plan branch). It does not edit the baseline migration or any previously applied migration.

## Requirements

1. Add a new additive goose migration using the next free number in the `mod-users` 100-199 range, for example `0104`, at `model/migrations/sql/0104_email_codes_cooldown_index.sql`. Do not edit `0100_baseline.sql` or `0103`.
2. The up migration creates a non-partial index on `email_codes (user_account_id, purpose, created_at DESC)` so that the any-state cooldown `NOT EXISTS` probe is satisfied by an index lookup. The index must not carry a `WHERE` predicate.
3. The up migration pins the search path of `create_email_code_with_cooldown` with `ALTER FUNCTION ... SET search_path` to `pg_catalog`, the schema in which `email_codes` was landed, and `pg_temp`. The schema name is read at migration time via `current_schema()`, in the same way the baseline trigger does. The migration must not hardcode `public`, because that breaks the displaced-schema layout.
4. Optionally, revoke `EXECUTE` on the function from `PUBLIC`, but only if this can be done without naming an application role the module cannot know. If it cannot, record in a SQL comment inside the migration why `SECURITY INVOKER` makes the revoke unnecessary.
5. The down migration drops the index and resets the function's `search_path` to its default.

## Validation

1. Against a uniquely named throwaway `postgres:16` container started with `docker run` and removed afterwards, apply the migrations and confirm with `EXPLAIN` that the cooldown probe uses the new index. Do not run `make dev.*` or `clean.data` on the shared Docker host.
2. Run `SELECT proconfig FROM pg_proc WHERE proname = 'create_email_code_with_cooldown'` and confirm it shows the pinned `search_path`. Confirm that the function still mints codes and enforces the cooldown.
3. The `api/internal/authz` email-code integration suite, including `displaced_guard_integration_test.go`, still passes. `go vet -tags integration ./...` passes.

## References

- Finding `f7fc` in this plan's `plan/findings.yaml` (cooldown probe lacks a supporting index).
- Finding `6DKr` in this plan's `plan/findings.yaml` (cooldown function search path is not pinned).

## Status

- Outcome: succeeded (2026-10-14).
- Added `model/migrations/sql/0104_email_codes_cooldown_index.sql`: non-partial index `email_codes_cooldown_idx` on `(user_account_id, purpose, created_at DESC)`; `ALTER FUNCTION create_email_code_with_cooldown(...) SET search_path = pg_catalog, <current_schema()>, pg_temp` via a DO block; `EXECUTE` is not revoked, with a SQL comment explaining that `SECURITY INVOKER` makes it unnecessary. Down resets `search_path` and drops the index.
- Validation: EXPLAIN uses an Index Only Scan on the new index (with seqscan disabled on a tiny table); `proconfig` shows `search_path=pg_catalog, public, pg_temp`; authz integration suite (74 passes, including the displaced-schema guard and the email-code cooldown tests) and `go vet -tags integration ./...` pass. Down migration verified in a rolled-back transaction.
- Note: the composed `model/schema/migrations` dir has a duplicate goose version 100 (sibling mod-core `0100_natural_person_owner_invariant.sql` versus `0100_baseline.sql`); validation used a scratch copy with that file renumbered.
