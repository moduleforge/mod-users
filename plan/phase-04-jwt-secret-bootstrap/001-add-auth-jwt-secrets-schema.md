# Add Auth Jwt Secrets Schema

## Purpose and scope

Add a new, mod-users-owned migration and sqlc query pair for a private,
single-row `auth_jwt_secrets` table — the persistence home for the HS256
JWT signing secret (`cfg.LocalAuth.JWTSecret`) when `JWT_SECRET` is not
supplied via the environment. This task creates the schema and generated
Go query code only; the fetch-or-generate-and-persist logic that uses
these queries is
[`002-implement-jwt-secret-bootstrap.md`](./002-implement-jwt-secret-bootstrap.md).

**Read this before writing the migration:** unlike `mod-core`'s analogous
`field_crypto_keys` table (which always already exists by the time
`mod-core`'s cipher-init call runs, since that call happens after both the
DB pool and every module's migrations in a generated `main.go`), this
module's `JWT_SECRET` bootstrap runs *inside* `config.Load()` — the very
first call in every generated `main.go`, before the pool and before any
migration. On a truly fresh database, `auth_jwt_secrets` will not exist
yet the first time `Load()`'s bootstrap path needs it. Task 002's
bootstrap path therefore creates the table itself, defensively, using this
same migration file's own idempotent DDL — which is why, unlike a normal
first-ever goose migration, **this one's `CREATE TABLE` must be
`CREATE TABLE IF NOT EXISTS`, not a bare `CREATE TABLE`.** Do not "fix"
this to match `mod-core`'s bare-`CREATE TABLE` precedent — the two cases
are not the same, and the idempotency is load-bearing for Task 002's
fresh-database path (see that task's Requirements, and
`plan/overview.md`'s "Key research findings").

No standard skill covers schema/codegen work end-to-end here; follow the
[`## Procedure`](#procedure) below, written specifically for this task.

## Requirements

1. **New migration** at `model/migrations/sql/0102_auth_jwt_secrets.sql`
   (the next free number after `0101_system_actors.sql`). Goose format
   (`-- +goose Up` / `-- +goose Down`), matching the style of the existing
   files in this directory:

   ```sql
   -- +goose Up

   -- auth_jwt_secrets is a private, single-row table holding the HS256
   -- JWT signing secret (see api/internal/auth.IssueLocalJWT et al.) when
   -- JWT_SECRET is not supplied via the environment (see
   -- api/internal/config.Load's JWT-secret bootstrap path,
   -- bootstrapJWTSecretFromDB / fetchOrGeneratePersistedJWTSecret). The
   -- id = 1 CHECK plus PRIMARY KEY caps this table at one row ever:
   -- concurrent first-boot callers race on that constraint (see
   -- InsertJWTSecretIfAbsent's ON CONFLICT (id) DO NOTHING below), so
   -- every caller converges on the same secret rather than each
   -- independently generating one and picking arbitrarily. The
   -- char_length CHECK is a defense-in-depth guard against a corrupt or
   -- truncated secret ever being persisted through this code path;
   -- the Go bootstrap path independently validates length on every read
   -- and fails loudly rather than regenerating if a persisted secret is
   -- too short.
   --
   -- CREATE TABLE IF NOT EXISTS, deliberately, unlike a normal first-ever
   -- migration: config.Load() runs before this module's own migrations
   -- apply in every generated composition root (unlike mod-core's
   -- analogous field_crypto_keys, whose cipher-init call runs after both
   -- the pool and migrations), so Load()'s own bootstrap path may have
   -- already created this table, idempotently, using this exact DDL,
   -- before goose ever gets here. See plan/overview.md's "Key research
   -- findings" for the full reasoning.
   --
   -- Deliberately not modeled as an entity (no FK to entities.id): this is
   -- bootstrap/operational data, the same reasoning that keeps
   -- goose_db_version_users a bare table.
   CREATE TABLE IF NOT EXISTS auth_jwt_secrets (
     id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
     secret     TEXT NOT NULL CHECK (char_length(secret) >= 32),
     created_at TIMESTAMPTZ NOT NULL DEFAULT now()
   );

   -- +goose Down

   DROP TABLE IF EXISTS auth_jwt_secrets;
   ```

2. **New sqlc query file** at `model/queries/auth_jwt_secrets.sql`:

   ```sql
   -- name: GetJWTSecret :one
   SELECT secret FROM auth_jwt_secrets WHERE id = 1;

   -- name: InsertJWTSecretIfAbsent :one
   INSERT INTO auth_jwt_secrets (id, secret)
   VALUES (1, $1)
   ON CONFLICT (id) DO NOTHING
   RETURNING secret;
   ```

   `GetJWTSecret` returns `pgx.ErrNoRows` when no secret has been
   persisted yet. `InsertJWTSecretIfAbsent` returns `pgx.ErrNoRows` when
   the `ON CONFLICT DO NOTHING` clause skips the insert — i.e. another
   caller already holds the row — which Task 002's caller must distinguish
   from a genuine error and handle by re-fetching the winner's secret. No
   `sqlc.yaml` override is needed: `TEXT` maps to Go `string` by sqlc's
   default mapping.

3. **Regenerate `model/db/`** via `cd model && sqlc generate` (or
   `make gen`). This adds `GetJWTSecret` and `InsertJWTSecretIfAbsent` to
   the generated `Queries` struct and `Querier` interface in
   `model/db/querier.go`, plus any new row/param types (or a new
   `model/db/auth_jwt_secrets.sql.go` file, sqlc's usual per-query-file
   split). Commit the generated output — `model/db/` is committed to the
   repo per `AGENTS.md`'s code-generation conventions, never hand-edited.

## Validation

- `cd model && make verify` — goose validate + sqlc compile — succeeds
  against the new migration and query file.
- `cd model && make lint` — applies migrations (including the new one) to
  an ephemeral Postgres via Docker; confirms `0102_auth_jwt_secrets.sql`
  applies cleanly on top of `0100`-`0101` and the `-- +goose Down` block
  reverses cleanly.
- `git diff --stat` shows exactly: the new migration file, the new query
  file, and the sqlc-regenerated files under `model/db/` — no hand-edits
  to any other `model/db/*.go` file.
- `grep -n "auth_jwt_secrets" model/db/querier.go` shows both
  `GetJWTSecret` and `InsertJWTSecretIfAbsent` in the `Querier` interface.
- `cd api && go build ./...` still succeeds (the local `replace` on
  `mod-users/model` in `api/go.mod` picks up the regenerated `model/db/`
  package immediately).
- Manual read-through confirms the migration's `CREATE TABLE` uses
  `IF NOT EXISTS` (per this task's header note) — this is easy to
  "correct" away by a reviewer unfamiliar with the reasoning; the header
  comment in the migration file itself should make the reason
  self-evident without needing this task doc.

## Metadata

architectural_impact: true

## Assumptions

- `model/db/` regeneration order/content is deterministic given sqlc
  v1.31.1 (the version recorded in existing generated-code header
  comments); no manual reconciliation should be needed beyond running
  `sqlc generate`.
- The table is named `auth_jwt_secrets` (plural, matching this repo's
  `auth_local`/`auth_oidc_identities` naming convention) even though it
  will only ever hold one row — consistent with existing convention rather
  than a special-cased singular name.
- The `secret` column stores the exact string `cfg.LocalAuth.JWTSecret`
  will be set to (a hex-encoded random value, generated by Task 002) —
  there is no separate encode/decode step on read, unlike `mod-core`'s
  `field_crypto_keys.key_bytes` (which stores raw `BYTEA` decoded from a
  hex-string env var). `TEXT`, not `BYTEA`, is therefore the correct
  column type here.

## References

- `model/migrations/sql/0101_system_actors.sql`,
  `model/migrations/sql/0100_schema.sql` — style precedent for goose
  migration files in this repo.
- `model/migrations/migrate.go` — `Migrate(ctx, db)` /
  `TableName = "goose_db_version_users"`; unchanged by this task, included
  for context on how the new migration gets applied at composing-app
  startup (and on *when*, relative to `config.Load()` — see this task's
  header note).
- `model/sqlc.yaml` — confirms no `db_type` override exists or is needed
  for `TEXT` (default `string` mapping).
- `model/db/db.go` — the generated `DBTX`/`Queries`/`New(db DBTX)` shape
  this task's new queries slot into unchanged; `New` accepts anything
  satisfying `DBTX` (`Exec`/`Query`/`QueryRow`), which both `*pgxpool.Pool`
  and a bare `*pgx.Conn` satisfy — relevant to Task 002's dedicated
  single-connection bootstrap path.
- `mod-core/worktrees/plan/catalog-readiness/plan/phase-01-durable-field-key/001-add-field-crypto-keys-table.md`
  (sibling project, read-only reference) — the closely-parallel precedent
  this task's shape follows, with the two deliberate divergences called
  out in this task's header note.

## Procedure

1. Write the migration file (Requirement 1), including its header comment
   explaining the `IF NOT EXISTS` deviation.
2. Write the query file (Requirement 2).
3. Run `cd model && sqlc generate` (Requirement 3); inspect the diff for
   unexpected changes to unrelated generated files.
4. Run the Validation commands above; fix and re-run until green.
5. Commit the migration file, query file, and regenerated `model/db/`
   output together as this task's change.

## Checkpoint hints

- After writing the migration file and confirming `make verify` accepts
  it.
- After writing the query file and running `sqlc generate`, before moving
  on to validate the full `model/` build.
