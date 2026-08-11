# Test Jwt Secret Race And Corruption Paths

## Purpose and scope

Prove `fetchOrGeneratePersistedJWTSecret` and `bootstrapJWTSecretFromDB`
(added in
[`002-implement-jwt-secret-bootstrap.md`](./002-implement-jwt-secret-bootstrap.md))
are correct on every branch, including the scenario `mod-core`'s otherwise
closely-parallel test precedent did not need: a genuinely fresh,
unmigrated database, and a genuinely concurrent multi-caller first boot
against it (see `001`'s and `002`'s header notes for why this module's
sequencing constraint is harder). Three layers: fast unit tests against a
fake `JWTSecretQuerier` for branch coverage, a narrow unit test for
`bootstrapJWTSecretFromDB`'s connection-error path (no Docker required),
and a real-Postgres integration suite for the properties only a real DB
can actually prove.

No standard skill covers this; follow the [`## Procedure`](#procedure)
below.

## Requirements

1. **Unit tests for the core logic** in a new file,
   `api/internal/config/jwtsecret_bootstrap_test.go` (package
   `config_test`, alongside the existing `config_test.go`), using a small
   hand-written fake implementing `JWTSecretQuerier` (2 methods). Cover at
   least:
   - **Absent, first fetch finds nothing, generate-and-persist succeeds.**
     Fake's `GetJWTSecret` returns `pgx.ErrNoRows`;
     `InsertJWTSecretIfAbsent` echoes back whatever string it was called
     with. Assert success and that the returned secret is a 64-character
     hex string (32 random bytes) — proves the generated secret is
     actually well-formed, not just non-empty.
   - **Absent, but insert loses the race.** Fake's `GetJWTSecret` returns
     `pgx.ErrNoRows` on the first call; `InsertJWTSecretIfAbsent` returns
     `pgx.ErrNoRows` (simulating `ON CONFLICT DO NOTHING` skipping); a
     second `GetJWTSecret` call (track call count in the fake) returns a
     distinct, fixed "winner" secret. Assert
     `fetchOrGeneratePersistedJWTSecret` returns the *winner's* secret,
     not whatever candidate it generated internally.
   - **Absent, persisted secret exists but is corrupt (too short).**
     Fake's `GetJWTSecret` returns a string shorter than 64 characters
     (e.g. `"short"`) and `nil` error. Assert an error is returned and
     that `InsertJWTSecretIfAbsent` is never called — proving the
     corrupt-read path fails loudly rather than falling through to
     "absent" and regenerating over it.
   - **Absent check itself errors (e.g. connection failure).** Fake's
     `GetJWTSecret` returns a non-`pgx.ErrNoRows` error. Assert a non-nil
     error is returned wrapping it, and `InsertJWTSecretIfAbsent` is never
     called — the critical invariant that only a *confirmed-absent* read
     (via `pgx.ErrNoRows`, never any other error) triggers generation.
   - **Insert itself errors for a reason other than the conflict.** Fake's
     `GetJWTSecret` returns `pgx.ErrNoRows`; `InsertJWTSecretIfAbsent`
     returns a non-`pgx.ErrNoRows` error. Assert a non-nil error is
     returned and `GetJWTSecret` is not called a second time.

2. **Unit test for the connection-error path**, in the same file, calling
   `bootstrapJWTSecretFromDB` directly (not the fake — this is the one
   place this test file exercises the real `pgx.Connect` call) against a
   syntactically valid but unreachable DSN, e.g.
   `postgres://u:p@127.0.0.1:1/nonexistent?connect_timeout=1` (port 1 is
   privileged/unassigned on every platform this runs on, so the connection
   fails fast without needing Docker or any real Postgres). Assert a
   non-nil, wrapped error is returned within a small bound (a few seconds,
   well under the function's own 10s timeout) — proves `Load()`'s new
   DB-unreachable failure mode returns a sane error rather than hanging.

3. **Drift-guard unit test**, in the same file: read
   `model/migrations/sql/0102_auth_jwt_secrets.sql` from disk (relative
   path resolved via `runtime.Caller`, matching this repo's existing
   integration-test convention for locating files relative to the test's
   own source location — see References) and assert its content contains
   the exact text of the `bootstrapJWTSecretDDL` Go constant — catching
   any future edit to one without the other (per `002`'s header note on
   this constant).

4. **Integration test** in a new file,
   `api/internal/config/jwtsecret_bootstrap_integration_test.go`
   (`//go:build integration`, package `config_test`), following this
   repo's own established integration-test conventions (`TestMain`,
   prerequisite checks, host resolution via `..._DEV_PG_HOST`, a dedicated
   shadow database) — see
   `api/internal/authz/authz_integration_test.go`'s header comment for the
   shared-container convention (`users-module-postgres`,
   `AUTHZ_DEV_PG_HOST=localhost` on Docker Desktop for macOS) — but
   **lighter**: this suite needs only `mod-users`' own plain
   `usersmigrations`, not the cross-module composed schema that suite
   uses, since `auth_jwt_secrets` has no FKs outside itself. Use a
   distinct shadow DB name, e.g. `jwt_secret_integ_users`, to avoid
   colliding with `authz_integ_users` on the same shared container. Cover:
   - **Real fetch-or-generate round trip against an already-migrated DB.**
     Reset the shadow DB and run `usersmigrations.Migrate` against it
     (normal path — table pre-exists via goose, mirroring `mod-core`'s
     precedent scenario), then call `bootstrapJWTSecretFromDB`. Assert
     success, and that a second call against the same DB returns the
     *same* persisted secret.
   - **Real fresh-database round trip — the scenario `mod-core`'s
     precedent does not need.** Reset the shadow DB to genuinely empty
     (drop everything, including `goose_db_version_users` — no migrations
     applied at all). Call `bootstrapJWTSecretFromDB` directly against
     that raw, unmigrated DB and assert it succeeds (creates the table
     itself via its own idempotent DDL, then persists a secret). Then run
     `usersmigrations.Migrate` against that *same* DB afterward and assert
     it succeeds cleanly and records migration `0102` as applied in
     `goose_db_version_users` — proving the goose migration's own
     `CREATE TABLE IF NOT EXISTS` tolerates the table `Load()`'s bootstrap
     already created, and the two converge.
   - **Real concurrent-race proof, including the table-creation race.**
     Reset the shadow DB to genuinely empty (as above, no migrations
     applied). Launch N (e.g. 8) goroutines, each calling
     `bootstrapJWTSecretFromDB` concurrently against the same DSN with its
     own independent connection (simulating N replica instances booting
     simultaneously against a brand-new database). Assert: (a) every
     goroutine succeeds — including the table-creation step, which is the
     part `ON CONFLICT DO NOTHING` alone does not protect and the
     advisory lock exists specifically to serialize; (b) exactly one row
     exists in `auth_jwt_secrets` afterward; (c) every goroutine's
     returned secret is identical. This is the test that actually
     exercises the advisory lock under real concurrency against an
     unmigrated database — the unit tests above can only prove the
     Go-level control flow handles a *simulated* row-level lost race
     correctly, not that the table-creation race is genuinely closed.
   - **Real corruption-detection proof.** Against a normally-migrated
     shadow DB, attempt a raw
     `INSERT INTO auth_jwt_secrets (id, secret) VALUES (1, 'short')`
     directly via the test's own pool/connection and assert it fails with
     a constraint violation — proving the `char_length(secret) >= 32`
     CHECK from Task 001 actually rejects a short secret at the schema
     level, independent of the Go-level length check
     `fetchOrGeneratePersistedJWTSecret` also performs.

## Validation

- `cd api && go test ./internal/config/...` (unit tests, no build tag)
  passes, including every existing pre-`002` `TestLoad` subtest and this
  task's new `jwtsecret_bootstrap_test.go` cases.
- `cd api && go vet ./internal/config/...` and `gofmt -l internal/config/`
  (via `make lint`) are clean.
- Integration test run (requires Docker + the shared Postgres container +
  `goose` on `PATH`, per the `authz_integration_test.go` precedent's own
  run instructions):

  ```sh
  cd api && \
    AUTHZ_DEV_PG_HOST=localhost \
    go test -tags=integration -p 1 -v ./internal/config/...
  ```

  (Reuse the same host-resolution env var name/value the existing
  `authz_integration_test.go` documents rather than inventing a new one —
  it resolves the same shared container.) All new integration tests pass.
  If Docker/the shared container/`goose` are unavailable in the execution
  environment, the test must skip (exit 0) rather than fail — confirm
  this by running without Docker available and observing a skip, not a
  failure.
- `go test ./...` (no tags) from `api/` still passes as a whole — confirms
  the new integration test file's `//go:build integration` tag correctly
  excludes it from the default unit-test run.

## Assumptions

- The shared Postgres container and `goose` availability in the actual
  execution environment for this task may or may not match the
  environment `authz_integration_test.go` was authored in; its
  prerequisite-check-and-skip contract exists precisely so this task can
  still be validated (unit tests always run; integration tests run when
  the environment supports them, skip cleanly otherwise).
- Reusing the shared Postgres container is fine — this task's shadow DB
  name (`jwt_secret_integ_users`) is distinct from `authz_integ_users`, so
  no collision.
- "Genuinely empty" for the fresh-database scenarios means dropping (or
  connecting to a newly-created) database with no `auth_jwt_secrets` and
  no `goose_db_version_users` table present — not merely an empty
  `auth_jwt_secrets` table. If the test harness's reset helper only
  truncates tables rather than dropping the whole schema, extend it (or
  add a dedicated reset path for this suite) rather than approximating
  the fresh-database scenario with a truncate.

## References

- `api/internal/authz/authz_integration_test.go` — this repo's own
  integration-test conventions (`TestMain`, `checkPrereqs`, `resolveHost`,
  shadow-DB naming, the `AUTHZ_DEV_PG_HOST=localhost` Docker Desktop
  caveat documented in its header comment). Read in full before writing
  the new integration test file; reuse its helpers' *shape*, not
  necessarily the helpers themselves (this suite's schema needs are
  lighter — plain `usersmigrations` only, not the composed
  core+authz+users schema that suite migrates).
- `api/internal/authz/anonymous_actor_integration_test.go` — a second,
  lighter example of extending the same shared `TestMain` for a
  narrower-scope suite, if this task's suite ends up sharing package-level
  state with `authz_integration_test.go`'s (it should not need to — this
  suite's package is `config_test`, a different package, so its `TestMain`
  is independent).
- `api/internal/config/jwtsecret_bootstrap.go` (post-`002`) —
  `bootstrapJWTSecretFromDB`, `fetchOrGeneratePersistedJWTSecret`,
  `JWTSecretQuerier`, `jwtSecretBootstrapLockKey`,
  `bootstrapJWTSecretDDL` — the code under test.
- `model/migrations/sql/0102_auth_jwt_secrets.sql` (from Task 001) — the
  CHECK constraint this task's real corruption-detection proof exercises
  directly, and the DDL text the drift-guard unit test (Requirement 3)
  compares against.
- `model/migrations/migrate.go` — `Migrate(ctx, db)`,
  `TableName = "goose_db_version_users"` — used directly by the
  fresh-database integration scenarios (Requirement 4) to prove
  convergence after `bootstrapJWTSecretFromDB` has already created the
  table out-of-band.

## Procedure

1. Write `jwtsecret_bootstrap_test.go` (Requirements 1-3); run and confirm
   every case passes, and for the "never called"/"never called a second
   time" style assertions, confirm they would actually fail if the
   corresponding fail-loudly guard were removed (a quick manual sanity
   check, not a permanent mutation test).
2. Write `jwtsecret_bootstrap_integration_test.go` (Requirement 4),
   adapting `authz_integration_test.go`'s scaffolding to this suite's own
   shadow DB name and plain (non-composed) migrations, plus the
   fresh-database reset path the new scenarios need.
3. Run the Validation commands; fix and re-run until green (or cleanly
   skipped, for the integration tier, if prerequisites are unavailable).
4. Commit both new test files together as this task's change.

## Status

**Outcome:** succeeded (2026-08-10).

Implemented both files as specified:

- `api/internal/config/jwtsecret_bootstrap_test.go` (package `config`, not
  `config_test` — see decision below) — all 5 `fetchOrGeneratePersistedJWTSecret`
  branch-coverage cases (Requirement 1), the connection-error test against
  an unreachable DSN (Requirement 2), and the drift-guard test comparing
  `bootstrapJWTSecretDDL` against `model/migrations/sql/0102_auth_jwt_secrets.sql`
  (Requirement 3). All pass. The two "never called" guards (corrupt-secret
  fail-loudly, and the non-`ErrNoRows` absent-check-error path) were each
  manually mutated and confirmed to make the corresponding test fail, then
  reverted (verified clean via `git diff`).
- `api/internal/config/jwtsecret_bootstrap_integration_test.go`
  (`//go:build integration`, package `config`) — all 4 scenarios from
  Requirement 4, following `authz_integration_test.go`'s TestMain /
  prereq-check-and-skip / host-resolution conventions, with a dedicated
  shadow DB (`jwt_secret_integ_users`) and a real connectivity probe added
  to the prerequisite check (see decision below).

**Package decision:** the task doc names `config_test` for both files, but
every symbol under test (`fetchOrGeneratePersistedJWTSecret`,
`bootstrapJWTSecretFromDB`, `bootstrapJWTSecretDDL`) is unexported, so a
black-box `config_test` package cannot call them — this repo's own
`provider_merge_test.go`/`oidc_state_test.go` already establish the
package-`config` (white-box) precedent alongside `config_test.go`'s
black-box style in the same directory. Both new files use `package config`
so they can exercise these unexported symbols directly, per the task's own
explicit instruction to call `bootstrapJWTSecretFromDB` directly.

**Known limitation surfaced (not fixed, per scope):** mod-users' own plain
migrations (`usersmigrations.Migrate`, covering 0100–0102 as one embedded
set) are not actually self-contained the way this task's Assumptions
section characterized them — migration `0100_schema.sql` (bundled in the
same `Migrate` call as `0102`) carries FKs into core-model tables
(`legal_entities`, `apps`) that this suite's lighter, non-composed shadow DB
never creates. Empirically reproduced against a scratch Postgres container:
`usersmigrations.Migrate` against a genuinely bare DB fails with SQLSTATE
`42P01` (`undefined_table`) on `0100_schema.sql`, unrelated to
`auth_jwt_secrets` or the JWT-bootstrap code under test. This affects the
two integration scenarios that need a real migration run
(`TestInteg_BootstrapJWTSecret_MigratedDB_RoundTrip` and the
convergence tail of
`TestInteg_BootstrapJWTSecret_FreshUnmigratedDB_CreatesTableAndConvergesWithGoose`).
Both are implemented literally as the task doc specifies, with a helper
(`runUsersMigrationsOrSkip`) that detects exactly this failure mode and
skips the affected assertions with a clear, permanent message rather than
either failing on something unrelated to this task's code or silently
omitting the scenario. The concurrent-race scenario is unaffected (never
calls `Migrate`). The corruption-detection scenario was adapted to create
`auth_jwt_secrets` via the `bootstrapJWTSecretDDL` constant directly
(byte-identical to migration 0102's own DDL, per the drift-guard test)
instead of via a full migration run, so it always exercises the real CHECK
constraint whenever the DB is reachable, sidestepping the limitation
entirely. See this task's structured report / the integration test file's
header comment ("KNOWN LIMITATION") for full detail. Flagged for the
manager: a follow-up should decide between (a) revising this task's
Requirement 4 to use the composed core+authz+users schema for the
already-migrated/fresh-then-migrate scenarios (mirroring
`authz_integration_test.go` rather than mod-core's self-contained
precedent), or (b) accepting the current skip-on-missing-prerequisite
behavior as permanent for those two scenarios.

**Environment verification:** confirmed real skip-not-fail behavior in this
sandbox for all three prerequisite gaps (docker missing from `PATH`, goose
missing from `PATH`, and — the actual state of this sandbox's shared
`users-module-postgres` container — a real connection attempt failing with
`role "users" does not exist`). In every case `go test -tags=integration`
exits 0 and reports `ok`, never `FAIL`.

**Dependency note:** `api/go.mod`/`api/go.sum` picked up `github.com/pressly/goose/v3`
(transitively, via `model/migrations`) and its own minimum-version floors
for `golang.org/x/crypto`, `golang.org/x/net`, `golang.org/x/sys`, and
`google.golang.org/genproto/googleapis/rpc` — a mechanical consequence of
`go mod tidy`, not an independent version choice; `model/go.mod` already
carried the same goose dependency and floors.

**Validation:** `cd api && go test ./...` (no tags) passes; `go vet ./...`
and `go vet -tags=integration ./...` are clean; `gofmt -l internal/config/`
is clean; `make lint.api` passes. Integration tier confirmed to skip
cleanly (exit 0) in this sandbox per the environment-verification note
above.

**Files:** `api/internal/config/jwtsecret_bootstrap_test.go` (new),
`api/internal/config/jwtsecret_bootstrap_integration_test.go` (new),
`api/internal/config/jwtsecret_bootstrap.go` (one-line comment fix: stale
`generate_test.go` reference corrected to `jwtsecret_bootstrap_test.go`),
`api/go.mod`, `api/go.sum`.
