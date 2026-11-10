# Document CredentialProfileAvailability Status Vocabulary In sqlc Query

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

This task addresses finding IzAq (minor, correctness): the `CredentialProfileAvailability` query in `model/queries/sessions.sql` carries no comment listing its status vocabulary, so the reasons `credential_revoked` and `credential_expired` are undocumented at the sqlc source and in the generated Go API (`model/db/querier.go` and `model/db/sessions.sql.go`). The change is documentation only: a SQL comment in the query source, followed by regeneration of `model/db` with sqlc. No SQL behavior, migration, or generated logic changes.

The work targets the plan branch `plan/identity-model-impl` and lands through the ordinary per-task loop (implementation in the plan worktree, validation, then the task's report).

## Requirements

1. Add a SQL comment under `-- name: CredentialProfileAvailability :one` in `model/queries/sessions.sql` listing the result vocabulary, in this order:
   1. `'available'`;
   2. `'credential_revoked'` (`revoked_at` set; reported first);
   3. `'credential_expired'` (`expires_at <= now()`);
   4. the `ProfileAvailability` reasons (`not_held`, `inactive`, `principal_disabled`, `confined`, `credential_not_authorized`, `provider_required`, `acr_too_low`);
   5. `'not_held'` for an unknown credential.
2. Regenerate `model/db` with `cd model && sqlc generate`. Do not hand-edit generated files.

## Validation

1. `model/queries/sessions.sql` shows the comment directly under the `-- name: CredentialProfileAvailability :one` line, before the `SELECT`.
2. `model/db/querier.go` and `model/db/sessions.sql.go` carry the comment on `CredentialProfileAvailability`, and the diff under `model/db/` is comments only.
3. `make -C model verify` and `make build` pass, with `MODULEFORGE_SIBLINGS_DIR` set per the project recipe (see the sibling-resolution note in `model/Makefile`).

## References

- Finding IzAq, a finding in this plan's `plan/findings.yaml` (type:bug, plan/phase:credential-profile-liveness, priority:low).

## Status

- Outcome: succeeded (2026-11-09).
- Change: added the result-vocabulary comment under `-- name: CredentialProfileAvailability :one` in `model/queries/sessions.sql`; regenerated with `cd model && sqlc generate`. Source files: `model/queries/sessions.sql`, `model/db/querier.go`, `model/db/sessions.sql.go`.
- Diff under `model/db/` is comment-only (8 insertions, 0 deletions per file).
- Validation: `make -C model verify` (with `MODULEFORGE_SIBLINGS_DIR` set) passed; `go build ./...` in `model/` passed. Root `make build` was not run (outside the worktree-safe command set for this dispatch).
