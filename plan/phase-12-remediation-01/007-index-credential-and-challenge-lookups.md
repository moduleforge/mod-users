# Add Missing Indexes For Password And Challenge Lookups

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task fixes findings `86kP` (optimization) and `Pjuz` (optimization). `ListActivePasswordsByPrincipal` seq-scans identifier-bound passwords because the only `principal_id` index on `cred_password` is partial on unbound rows, and `auth_challenges` lacks indexes on its four foreign-key columns. The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop.

Suggested role: `plugins/flow/roles/developer-sql.md`

## Requirements

1. `0100_baseline.sql` adds `cred_password_principal_id_idx` ON `mod_users.cred_password` (`principal_id`) WHERE `active`.
2. `0100_baseline.sql` adds partial indexes on `mod_users.auth_challenges` `principal_id`, `identifier_id`, `session_id` and `credential_id` (each `WHERE col IS NOT NULL`), covering the `ON DELETE CASCADE` lookups and the open-challenge queries; queries are rewritten to a form the indexes serve if needed.

## Validation

1. `cd model && make verify` passes; the authz and challenges integration tests pass.
2. `EXPLAIN` on `ListActivePasswordsByPrincipal` and `GetLatestOpenChallenge` against a seeded database shows an index scan.

## References

- Finding `86kP` in this plan's `plan/findings.yaml` (optimization, medium): the only `principal_id` index on `cred_password` is partial on unbound rows, so `ListActivePasswordsByPrincipal` seq-scans.
- Finding `Pjuz` in this plan's `plan/findings.yaml` (optimization, medium): `auth_challenges` has no usable index on its four foreign-key columns.

## Status

Outcome: succeeded (2026-10-25).

- Added `cred_password_principal_id_idx` (partial on `active`) and four partial `auth_challenges` FK indexes in `model/migrations/sql/0100_baseline.sql`.
- Rewrote `GetLatestOpenChallenge` and `ConsumeOpenChallengesBySubject` in `model/queries/challenges.sql` to the `COALESCE(col, 0) = COALESCE(arg, 0)` form that `auth_challenges_cooldown_idx` serves; regenerated `model/db/`.
- Validation: `make verify` passed; authz, config and integration suites passed against a throwaway Postgres; EXPLAIN shows index scans (`cred_password_principal_id_idx`, `auth_challenges_cooldown_idx`). The password plan needed `enable_seqscan=off` because the seeded table has only 91 rows.
