# Implement Jwt Secret Bootstrap

## Purpose and scope

Add a fetch-or-generate-and-durably-persist path for `JWT_SECRET` to
`api/internal/config`, wired into `Load()` so that when the env var is
absent, the resolved secret comes from Postgres instead of failing boot
outright. Depends on
[`001-add-auth-jwt-secrets-schema.md`](./001-add-auth-jwt-secrets-schema.md)
(needs the generated `GetJWTSecret`/`InsertJWTSecretIfAbsent` method
shapes) and on Phase 3's
[`001-add-database-url-env-fallback.md`](../phase-03-db-url-fallback/001-add-database-url-env-fallback.md)
(this task's bootstrap path consumes `cfg.DB.URL`, which Phase 3's
`resolveDBURL()` populates).

**The hard constraint this task must satisfy, restated:** `Load()`'s
exported signature (`func Load() (*Config, error)`, zero args) must not
change — every composing app's `moduleforge.app.yaml` declares
`cfg: {constructor: config.Load, args: []}`, and this task changes no
manifest, in this repo or any other. All new behavior is internal to
`Load()`'s body. `Load()` gains a new internal DB dependency (only
exercised when `JWT_SECRET` is unset and `cfg.DB.URL` is non-empty); it
was previously pure/network-free.

No standard skill covers this; follow the [`## Procedure`](#procedure)
below.

## Requirements

1. **New file `api/internal/config/jwtsecret_bootstrap.go`** with a
   narrow `JWTSecretQuerier` interface and the unit-testable core logic,
   mirroring `mod-core`'s `FieldKeyQuerier`/`fromPersistedOrGenerated`
   shape (see References) but adapted to this table's `string`-typed
   column and this module's harder sequencing constraint:

   ```go
   // JWTSecretQuerier is the minimal persistence contract
   // fetchOrGeneratePersistedJWTSecret needs. Satisfied structurally by
   // *usersdb.Queries (github.com/moduleforge/mod-users/model/db) — this
   // package already imports that package (see provider_merge.go), so no
   // new intra-module dependency is introduced.
   type JWTSecretQuerier interface {
       GetJWTSecret(ctx context.Context) (string, error)
       InsertJWTSecretIfAbsent(ctx context.Context, secret string) (string, error)
   }

   const jwtSecretByteLen = 32 // 256 bits; hex-encoded to a 64-char string.

   // fetchOrGeneratePersistedJWTSecret fetches the persisted secret if one
   // exists, or generates and durably persists a new one. Concurrent
   // first-boot callers are safe at the row level (ON CONFLICT DO
   // NOTHING); the caller (bootstrapJWTSecretFromDB) additionally holds a
   // session-level advisory lock around the whole sequence, needed
   // because — unlike mod-core's analogous case — the target table may
   // not exist yet the first time this runs (see 001's header note),
   // which ON CONFLICT alone does not protect against. A persisted
   // secret that fails the length check is a fail-loudly error, never
   // silently regenerated.
   func fetchOrGeneratePersistedJWTSecret(ctx context.Context, q JWTSecretQuerier) (string, error) {
       secret, err := q.GetJWTSecret(ctx)
       switch {
       case err == nil:
           if len(secret) < jwtSecretByteLen*2 { // hex-encoded length
               return "", fmt.Errorf("jwtsecret: persisted secret is %d chars, want >= %d (corrupt or truncated)", len(secret), jwtSecretByteLen*2)
           }
           return secret, nil
       case errors.Is(err, pgx.ErrNoRows):
           // fall through to generate-and-persist
       default:
           return "", fmt.Errorf("jwtsecret: read persisted secret: %w", err)
       }

       candidate := make([]byte, jwtSecretByteLen)
       if _, rerr := rand.Read(candidate); rerr != nil {
           return "", fmt.Errorf("jwtsecret: generate secret: %w", rerr)
       }
       encoded := hex.EncodeToString(candidate)

       inserted, err := q.InsertJWTSecretIfAbsent(ctx, encoded)
       switch {
       case err == nil:
           return inserted, nil // we won the race; DB echoes back what we inserted
       case errors.Is(err, pgx.ErrNoRows):
           // ON CONFLICT DO NOTHING skipped our row: another caller won.
           winner, rerr := q.GetJWTSecret(ctx)
           if rerr != nil {
               return "", fmt.Errorf("jwtsecret: re-fetch secret after lost race: %w", rerr)
           }
           return winner, nil
       default:
           return "", fmt.Errorf("jwtsecret: persist generated secret: %w", err)
       }
   }
   ```

2. **Same file, the DB-connecting outer wrapper** — the part that only an
   integration test (Task 003) can meaningfully exercise, since it opens a
   real connection:

   ```go
   // jwtSecretBootstrapLockKey is a Postgres session-level advisory-lock
   // key, namespaced via FNV-1a hash of a distinctive string so it does
   // not collide with any other advisory lock use on the same database.
   // No other advisory lock use was found in mod-core, mod-audit,
   // mod-authz, mod-tags, mod-tasks, or mod-users at the time this was
   // written (see plan/overview.md's research) — still, hash rather than
   // hardcode a small int to keep the collision probability negligible if
   // that ever changes.
   var jwtSecretBootstrapLockKey = func() int64 {
       h := fnv.New64a()
       _, _ = h.Write([]byte("mod-users:auth_jwt_secrets:bootstrap"))
       return int64(h.Sum64())
   }()

   // bootstrapJWTSecretDDL must stay byte-for-byte identical to the Up
   // block of model/migrations/sql/0102_auth_jwt_secrets.sql (see that
   // file's own header comment for why IF NOT EXISTS is required here).
   // A drift-guard test in generate_test.go asserts this file's content
   // contains this exact string.
   const bootstrapJWTSecretDDL = `CREATE TABLE IF NOT EXISTS auth_jwt_secrets (
     id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
     secret     TEXT NOT NULL CHECK (char_length(secret) >= 32),
     created_at TIMESTAMPTZ NOT NULL DEFAULT now()
   );`

   // bootstrapJWTSecretFromDB opens its own short-lived, dedicated
   // connection (not the pool the composing app's generated main.go
   // builds later — that pool does not exist yet; config.Load() is the
   // first call in every generated main.go) using dbURL — the value
   // resolveDBURL() already produced — and fetches-or-generates-and-
   // persists the JWT secret. Bounded by a fixed timeout so a boot with
   // an unreachable database fails fast rather than hanging.
   func bootstrapJWTSecretFromDB(ctx context.Context, dbURL string) (string, error) {
       ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
       defer cancel()

       conn, err := pgx.Connect(ctx, dbURL)
       if err != nil {
           return "", fmt.Errorf("jwtsecret: connect: %w", err)
       }
       defer conn.Close(context.Background())

       if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", jwtSecretBootstrapLockKey); err != nil {
           return "", fmt.Errorf("jwtsecret: acquire advisory lock: %w", err)
       }
       defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", jwtSecretBootstrapLockKey)

       // Ensure the table exists: config.Load() runs before this
       // module's own migrations apply (see 001's header note), so on a
       // truly fresh database this table does not exist yet. Idempotent;
       // safe to run every boot, and safe under the advisory lock above
       // even though CREATE TABLE IF NOT EXISTS alone is not safe against
       // genuinely concurrent, unserialized callers.
       if _, err := conn.Exec(ctx, bootstrapJWTSecretDDL); err != nil {
           return "", fmt.Errorf("jwtsecret: ensure table: %w", err)
       }

       q := usersdb.New(conn) // *pgx.Conn satisfies db.DBTX
       return fetchOrGeneratePersistedJWTSecret(ctx, q)
   }
   ```

   New imports this file introduces to `api/internal/config`:
   `crypto/rand`, `encoding/hex`, `errors`, `hash/fnv`, `time`,
   `github.com/jackc/pgx/v5` (already an `api/go.mod` dependency, used
   elsewhere in this module — e.g. `provider_merge.go`'s
   `pgx/v5/pgtype`), and `usersdb "github.com/moduleforge/mod-users/model/db"`
   (this package already imports this exact package in
   `provider_merge.go` — confirm the import alias matches that file's
   convention, `db "github.com/moduleforge/mod-users/model/db"`, rather
   than introducing a second alias for the same import path in the same
   package).

3. **Wire into `Load()`.** In `config.go`, after the `cfg := &Config{...}`
   literal is built (so `cfg.DB.URL` and `cfg.LocalAuth.JWTSecret` are both
   populated from their respective env reads) and before the
   `validate(cfg, parseErrors)` call, add:

   ```go
   if cfg.LocalAuth.JWTSecret == "" && cfg.DB.URL != "" {
       secret, err := bootstrapJWTSecretFromDB(context.Background(), cfg.DB.URL)
       if err != nil {
           parseErrors = append(parseErrors, fmt.Sprintf("JWT_SECRET: bootstrap from database failed: %v", err))
       } else {
           cfg.LocalAuth.JWTSecret = secret
       }
   }
   ```

   The `cfg.DB.URL != ""` guard is deliberate: when `DB_URL` /
   `MFAPP_DATABASE_URL` are *also* both unset, this skips the DB attempt
   entirely, so `validate()`'s existing aggregated-missing-fields path
   (unchanged) is what fires — this is what keeps the existing
   `"missing required fields produces aggregated error"` test passing
   unmodified (same reasoning as Phase 3's task, for the same test).
   When the DB attempt is made and fails, `cfg.LocalAuth.JWTSecret` stays
   `""`, so `validate()`'s own "missing required environment variables"
   check *also* fires for `JWT_SECRET` — a deliberate, accepted minor
   redundancy (two lines about the same root cause is more informative
   than confusing; do not suppress it by special-casing `validate()`).
   `config.go` needs a new `"context"` import for this call site.

4. **Update doc comments.** `Load()`'s package-level doc comment and
   `LocalAuthConfig.JWTSecret`'s field comment should describe the new
   fetch-or-generate-persist fallback (env var always wins; DB is only
   consulted when both the env var and the resolved DB URL... no — when
   the env var is absent and the resolved DB URL is present; corrupt
   persisted state fails loudly).

## Validation

- `cd api && go build ./...` succeeds.
- `cd api && make lint` (go vet + gofmt check) passes.
- `cd api && go test ./internal/config/...` passes — every *existing*
  `TestLoad` subtest in `config_test.go` continues to pass unmodified,
  proving `Load()`'s behavior is unchanged for every caller that already
  sets `JWT_SECRET` explicitly. (New tests for the bootstrap path itself
  are
  [`003-test-jwt-secret-race-and-corruption-paths.md`](./003-test-jwt-secret-race-and-corruption-paths.md)'s
  job, not this task's — this task only needs the existing suite to stay
  green plus the package to build and typecheck with the new code
  present.)
- `grep -n "bootstrapJWTSecretFromDB\|fetchOrGeneratePersistedJWTSecret\|JWTSecretQuerier" api/internal/config/jwtsecret_bootstrap.go api/internal/config/config.go` shows the new symbols wired through both files.
- `grep -n "MFAPP_DATABASE_URL\|DB_URL" api/internal/config/config.go` still
  shows `resolveDBURL` (from Phase 3) as the only place `cfg.DB.URL` is
  populated — confirms this task did not duplicate Phase 3's logic.
- Manual read-through confirms `Load()`'s exported signature is unchanged
  (`func Load() (*Config, error)`) and `api/config/config.go`'s facade
  (`func Load() (*Config, error) { return inner.Load() }`) needed no edit.
- Manual read-through confirms the `bootstrapJWTSecretDDL` Go constant's
  text is byte-for-byte identical to migration `0102`'s Up block (Task
  003 turns this into an automated drift-guard test; this task's own
  validation is the manual check, since the automated one does not exist
  yet at this point in the sequence).

## Metadata

architectural_impact: true

## Assumptions

- `mfgen`, every composing app's generated `main.go`, and every composing
  app's `moduleforge.app.yaml` are out of this task's scope — this task's
  job ends at `mod-users`' own `Load()` implementation, which by design
  requires no change anywhere else.
- The 10-second connect/query timeout is a reasonable default for a
  same-cluster or same-host Postgres reachable at boot; if the real
  execution environment's shared Postgres container is meaningfully
  slower to accept connections, adjust the constant rather than removing
  the timeout — an unbounded hang here would block every composing app's
  boot indefinitely on a DB outage, worse than today's immediate
  "missing JWT_SECRET" fail-fast.

## References

- `api/internal/config/config.go` — `Load()`, `validate()`,
  `LocalAuthConfig` — the file this task modifies (already read in full
  during planning).
- `api/internal/config/provider_merge.go` — this package's existing
  precedent for importing `mod-users/model/db` and accepting a narrow
  query interface (`OIDCProviderQuerier`) rather than the full `Querier`;
  this task's `JWTSecretQuerier` follows the identical narrowing
  rationale.
- `model/db/db.go` — confirms `*pgx.Conn` satisfies `DBTX`
  (`Exec`/`Query`/`QueryRow`), so `usersdb.New(conn)` in Requirement 2 is
  valid without a pool.
- `model/migrations/sql/0102_auth_jwt_secrets.sql` (from Task 001) — the
  DDL this task's `bootstrapJWTSecretDDL` constant must stay identical to.
- `mod-core/worktrees/plan/catalog-readiness/plan/phase-01-durable-field-key/002-implement-auto-generate-cipher.md`
  (sibling project, read-only reference) — the closely-parallel precedent
  for `FieldKeyQuerier`/`fromPersistedOrGenerated`, and for factoring
  shared decode/validate logic so behavior for the explicit-env-var case
  is provably unchanged (not directly applicable here in the same shape,
  since `JWT_SECRET` — unlike `CORE_FIELD_KEY_HEX` — has no format to
  validate; the env-var branch here is a bare non-empty check that
  already existed in `Load()` before this task).
- `plan/overview.md`'s "Key research findings" — the confirmed real-
  `main.go` call ordering (`config.Load()` before the pool, before
  migrations) that this task's design directly addresses.

## Procedure

1. Implement Requirement 1 (`jwtsecret_bootstrap.go`'s unit-testable core).
2. Implement Requirement 2 (same file's DB-connecting wrapper).
3. Implement Requirement 3 (`Load()` wiring in `config.go`).
4. Update doc comments (Requirement 4).
5. Run the Validation commands; fix and re-run until green.
6. Commit all changed files together as this task's change.

## Checkpoint hints

- After Requirement 1 compiles and typechecks (it has no DB dependency —
  a quick sanity check with an inline fake, thrown away or kept as the
  seed of Task 003's real test file, is a reasonable mid-task check).
- After Requirement 2 compiles (it cannot be exercised without a real
  Postgres; compiling cleanly is the bar for this checkpoint).
- After Requirement 3 (the `Load()` wiring), before running full
  Validation.

## Status

**Outcome:** succeeded (2026-08-10).

- Added `api/internal/config/jwtsecret_bootstrap.go` with
  `JWTSecretQuerier`, `fetchOrGeneratePersistedJWTSecret`,
  `jwtSecretBootstrapLockKey`, `bootstrapJWTSecretDDL`, and
  `bootstrapJWTSecretFromDB` exactly per Requirements 1–2.
- Wired `Load()` in `api/internal/config/config.go` exactly per
  Requirement 3's diff (new `"context"` import; the
  `cfg.LocalAuth.JWTSecret == "" && cfg.DB.URL != ""` guard before
  `validate(cfg, parseErrors)`).
- Updated doc comments on `Load()` and `LocalAuthConfig.JWTSecret` per
  Requirement 4.
- **Import-alias contradiction resolved:** Requirement 2's illustrative
  code block writes `usersdb.New(conn)`, but its own "New imports this
  file introduces" note explicitly instructs matching
  `provider_merge.go`'s existing alias (`db
  "github.com/moduleforge/mod-users/model/db"`) rather than introducing
  a second alias for the same import path in the same package. Followed
  the explicit instruction: this file imports the same path as `db` (not
  `usersdb`) and calls `db.New(conn)`; the `JWTSecretQuerier` doc comment
  was adjusted to match.
- **Pre-existing `go vet` failure fixed (folded-in, single file):**
  `api/internal/service/user_accounts_upgrade_test.go`'s `stubUAQuerier`
  did not implement the two `db.Querier` methods Task 001 added
  (`GetJWTSecret`, `InsertJWTSecretIfAbsent`), which pre-existing gap
  broke `go vet ./...` (and therefore `cd api && make lint`) for the
  whole module — including this task's own required validation. Added
  the two missing trivial stub methods (mirroring the file's existing
  zero-value/nil-error pattern for unused Querier methods). This is the
  only fix outside this task's own two files.
- Validation:
  - `cd api && go build ./...` — passed.
  - `cd api && make lint` (`go vet ./...` +
    `check-server-error-literals`) — passed.
  - `cd api && go test ./internal/config/...` — passed; every existing
    `TestLoad` subtest passed unmodified.
  - `grep -n "bootstrapJWTSecretFromDB\|fetchOrGeneratePersistedJWTSecret\|JWTSecretQuerier" api/internal/config/jwtsecret_bootstrap.go api/internal/config/config.go` — shows all three symbols wired through both files.
  - `grep -n "MFAPP_DATABASE_URL\|DB_URL" api/internal/config/config.go` —
    confirms `resolveDBURL` (Phase 3) remains the sole populator of
    `cfg.DB.URL`.
  - Manual read-through confirms `Load()`'s exported signature is
    unchanged and `api/config/config.go`'s facade needed no edit.
  - Manual (scripted) byte-for-byte comparison confirms
    `bootstrapJWTSecretDDL`'s text exactly matches migration `0102`'s Up
    block's `CREATE TABLE ...;` statement.
- Assumptions from `## Assumptions` relied on as written (10s bootstrap
  timeout; no `mfgen`/`main.go`/`moduleforge.app.yaml` changes needed).

Affected files (repo-relative):
- `api/internal/config/jwtsecret_bootstrap.go`
- `api/internal/config/config.go`
- `api/internal/service/user_accounts_upgrade_test.go`
- `plan/phase-04-jwt-secret-bootstrap/002-implement-jwt-secret-bootstrap.md`
