# Add SSH Key Storage

## Purpose and scope

Add the storage layer for SSH public keys. That means one new goose migration, which creates the `mod_users` schema and the `mod_users.ssh_public_keys` table, plus one new sqlc query file and the regenerated `model/db/` code. No `api/` code changes in this task.

No standard skill covers this. Follow the [SSH key design note](../notes/ssh-key-design.md), sections D1–D4 and "Table shape", which is the fixed contract.

## Requirements

1. **Migration.** Create `model/migrations/sql/0103_ssh_public_keys.sql`, the next number in mod-users' 100–199 range.
   - `-- +goose Up`:
     - `CREATE SCHEMA IF NOT EXISTS mod_users;`
     - the table exactly as in the design note's "Table shape" section: the columns, the `key_type` CHECK allow-list, the fingerprint CHECK, and the label length CHECK
     - `ssh_public_keys_active_fingerprint_uq`: a partial **unique** index on `(fingerprint_sha256) WHERE archived_at IS NULL`
     - `ssh_public_keys_user_account_active_idx`: an index on `(user_account_id) WHERE archived_at IS NULL`
   - All DDL uses `IF NOT EXISTS` so re-application is a no-op, per `building-modules.md#migrations-must-stay-safely-re-appliable`.
   - Every object reference is schema-qualified: `mod_users.ssh_public_keys`, `public.user_accounts(id)`.
   - No `GRANT` statements.
   - `-- +goose Down`: follow `0100_schema.sql`'s form and write `-- intentionally omitted — forward-only per building-modules.md`. Do not drop the schema.
   - Add a header comment explaining three things:
     - why this table lives in `mod_users` while the module's older tables stay in `public` (design note D1)
     - that the partial unique index is the global-uniqueness invariant and the resolver's lookup index (D2)
     - that revocation archives the row rather than deleting it (D3)
2. **Queries.** Create `model/queries/ssh_public_keys.sql`. Every table reference is schema-qualified. The queries:
   - `InsertSSHPublicKey :one`. Inserts `(user_account_id, key_type, public_key, fingerprint_sha256, label)` and returns the full row. It must **not** pre-check for duplicates. The unique index is the enforcement, and task 003 maps the `23505` violation.
   - `ListActiveSSHPublicKeysByUserAccount :many`. Filters `user_account_id = $1 AND archived_at IS NULL`, orders by `created_at ASC, id ASC`, and paginates with `LIMIT`/`OFFSET` parameters.
   - `CountActiveSSHPublicKeysByUserAccount :one`. Counts the same active set, for the list `total`.
   - `GetActiveSSHPublicKeyByUUIDForUserAccount :one`. Filters `uuid = $1 AND user_account_id = $2 AND archived_at IS NULL`, for revoke's before-snapshot.
   - `ArchiveSSHPublicKey :execrows`. Runs `UPDATE ... SET archived_at = now() WHERE uuid = $1 AND user_account_id = $2 AND archived_at IS NULL`. Zero rows affected means "not found for this account", which task 003 masks to 403.
   - `ResolveActiveSSHPublicKey :one`. Selects `ua.account_holder` from `mod_users.ssh_public_keys k JOIN public.user_accounts ua ON ua.id = k.user_account_id WHERE k.fingerprint_sha256 = $1 AND k.public_key = $2 AND k.archived_at IS NULL`. Do not join mod-core's `entities` table; sqlc cannot see it (design note D4). Task 003 performs the account-holder archival check separately.
3. **Codegen.** Run `cd model && sqlc generate` and commit the regenerated `model/db/` files.
   - sqlc prefixes non-`public` table models with the schema name, which likely yields `ModUsersSshPublicKey`. Keep that, or add a `rename` override in `model/sqlc.yaml`, and record which you chose in the Status section, since task 003 consumes these names.
   - If sqlc cannot resolve `mod_users.`-qualified names or `public.user_accounts`, find the minimal fix and record it. For example, sqlc may need the `CREATE SCHEMA` statement present in the schema input, which it is here. Do not fall back to an unqualified or `public` table without halting and reporting.
4. Do not modify any existing migration, query, or table.

## Validation

- `model/migrations/sql/0103_ssh_public_keys.sql` and `model/queries/ssh_public_keys.sql` exist. `git diff --stat` shows no changes to `0100`–`0102` or any other existing query file.
- `grep -nE '\b(ssh_public_keys|user_accounts)\b' model/migrations/sql/0103_ssh_public_keys.sql model/queries/ssh_public_keys.sql` shows every occurrence schema-qualified (`mod_users.` or `public.`), apart from index and constraint *names*.
- `cd model && sqlc generate` succeeds, and a second run produces no diff.
- `cd model && go build ./...` succeeds, and `make build.api` still builds.
- Migration application against a real database, when the dev Postgres with mod-core migrations applied is reachable (`make dev.start`; see followups `EMIS` and `MwXo` for host caveats):
  - `make -C model test.integration` (goose up) succeeds.
  - Running it a second time is a no-op.
  - In `psql`, `\d mod_users.ssh_public_keys` shows both partial indexes and the three CHECK constraints.
  - Inserting two active rows with the same `fingerprint_sha256` fails with `23505`.
  - Archiving one of them and inserting again succeeds.

  If no database is reachable, say so explicitly in the Status section rather than claiming this passed.
- `make lint.model` passes, or fails only with the pre-existing `legal_entities`-missing shadow-db error (followup `nAXW`). Record which.

## Metadata

architectural_impact: true

## Assumptions

- mod-core's `public.user_accounts`-adjacent tables (`legal_entities`, `entities`, `apps`) are applied before this module's migrations (`migrations.after: [core]`).
- `gen_random_uuid()` is available, as it already is for `user_accounts.uuid`.
- This is the first table in the ecosystem under a `mod_*` schema. `mod-tokens` created an empty `mod_tokens` schema only. Surprises in sqlc or goose around schema qualification are plausible. Record any in Status.

## References

- [SSH key design note](../notes/ssh-key-design.md): D1–D4 and "Table shape" are this task's contract.
- `/Users/zane/playground/moduleforge/docs-mf-standards/building-modules.md#data-ownership-and-schema-conventions`: schema, qualification, idempotency, and archival conventions, with the canonical example migration.
- `/Users/zane/playground/moduleforge/docs-mf-standards/architecture/schema-ownership-design.md`: rationale for the standard.
- `/Users/zane/playground/moduleforge/mod-tokens/model/migrations/1100_schema.sql`: the only existing `CREATE SCHEMA IF NOT EXISTS mod_*` precedent.
- `model/migrations/sql/0100_schema.sql`: sibling credential table `auth_oidc_identities`, and the Down-section form.
- `model/queries/auth_oidc_identities.sql`: query style precedent.
- `model/sqlc.yaml`: sqlc configuration, including `omit_unused_structs: true`.
- AGENTS.md "Database migrations" and "Code generation (sqlc)".

## Checkpoint hints

- After the migration file applies cleanly.
- After the query file and `sqlc generate` succeed.
