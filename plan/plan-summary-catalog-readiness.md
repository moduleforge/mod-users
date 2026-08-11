# Plan Summary: catalog-readiness

## What was planned and why

This is `mod-users`' leg of a three-project federated plan (shared slug
`catalog-readiness` across `mod-core`, `mod-users`, and `app-mftodo`). The
federating plan is `app-mftodo`'s own `catalog-readiness` plan; its
`plan/notes/config-and-secrets-cross-repo-blocker.md` traced two of its
items to code that lives in this repo, not `app-mftodo`'s own — the user's
recorded answer there ("Do the cross repo change to mod-users/mod-core")
is what puts this work in `mod-users`' own `project_root`.

**Scope, exactly:**

- **A. Database connection-string env var.** `api/internal/config/config.go`
  (behind the public `api/config` facade's `Load()`) reads the Postgres
  connection string via a hardcoded `os.Getenv("DB_URL")`. Give it a
  preference-with-fallback: read `MFAPP_DATABASE_URL` when set, else
  `DB_URL` — both **permanently** supported (see
  [Deferred and flagged](#deferred-and-flagged) for why this plan does
  *not* adopt the change request's suggested deprecate-and-remove framing).
- **B. Durable `JWT_SECRET` bootstrap.** The same `config.go`, inside
  `Load()`, validates `JWT_SECRET` as `required` and fails the whole
  process before any DB pool exists anywhere in a generated composition
  root. Give `Load()` the ability to fetch-or-generate-and-durably-persist
  `JWT_SECRET` from Postgres when the env var is absent — using `Load()`'s
  own dedicated, short-lived DB connection (opened with the same resolved
  URL from item A), since no pool exists yet at this point in every real
  generated `main.go` this plan inspected. Explicit env var always wins;
  a persisted-but-corrupt secret fails loudly rather than silently
  regenerating.

`Load()`'s exported signature (`func Load() (*Config, error)`, zero args)
is unchanged — this is the load-bearing compatibility property that keeps
every composing app's `moduleforge.app.yaml` (`constructor: config.Load,
args: []`) working unmodified, with no coordinated change required in
`app-mftodo`, `app-mfdemo`, `app-mfmanager`, or any other composing app.

**Explicitly out of scope:** any change to `app-mftodo`, `mod-core`, or
`mfgen` (each is a separate repo with its own dispatch); any change to the
`docs/mf-standards` git submodule — the user's answer also calls for a
`docs-mf-standards` content update, but that is a separate repository, out
of this project's `project_root`, and is returned via
`flagged_for_manager` rather than actioned here (matching the sibling
`mod-core` plan's own precedent for the identical constraint).

### Key research findings (read these first)

**A's blast radius is real, not hypothetical.** The change request asked
whether any app besides `app-mftodo` composes `mod-users` and still sets
`DB_URL` — yes: `app-mfdemo`'s `moduleforge.app.yaml` declares
`cfg: {constructor: config.Load, args: []}` identically to `app-mftodo`'s,
and **`app-mfmanager` itself** — the very platform that will inject
`MFAPP_DATABASE_URL` into the *managed apps it deploys* — composes
`mod-users` too, and uses `DB_URL` for **its own** bootstrap Postgres
connection (`app-mfmanager/.env.example:86`,
`deploy/local/docker-compose.yml:114`, `deploy/prod/.env.example:44`).
`MFAPP_DATABASE_URL` is specifically the name `app-mfmanager` injects into
the apps *it* deploys via its scoped-Postgres-role provisioner
(`app-mfmanager/.env.example:524-548`) — a different topology than
`app-mfmanager`'s own self-hosted bootstrap connection, which will
continue to be named `DB_URL` indefinitely, by design, since it isn't one
of the apps `app-mfmanager` deploys into itself. A same-day, no-fallback
rename would break `app-mfdemo` and `app-mfmanager` outright, not just
every yet-to-migrate `app-mftodo`-like consumer. See
[Deferred and flagged](#deferred-and-flagged) for why this reframes A's
design away from the change request's suggested deprecate-and-remove
window.

**B's sequencing problem is real and does not resolve the way mod-core's
did.** Reading `app-mftodo/cmd/server/main.go` directly confirms:
`cfg, err := config.Load()` (line 66) runs before `pool, err :=
localdb.New(ctx, cfg)` (line 71), which runs before every module's
migrations (lines 84-107, including `usersmigrations.Migrate` at line
103). Unlike `mod-core`'s `fieldcrypto.NewFromEnv()` (line 127, safely
after both the pool and every module's migrations — see
`mod-core`'s own `catalog-readiness` plan), `mod-users`' `JWT_SECRET` check
sits inside `Load()`, before any of that exists. This has two consequences
this plan's design must (and does) address, neither of which `mod-core`
needed to:

1. `Load()` itself must open its own short-lived, dedicated DB connection
   (not the pool the composing app builds later) to attempt the bootstrap.
2. **The target table may not exist yet.** Because `Load()` runs before
   this module's own migrations do, a truly fresh database has no
   `auth_jwt_secrets` table at the moment `Load()` needs it — unlike
   `mod-core`'s `field_crypto_keys`, which always already exists by the
   time `fieldcrypto.NewFromEnv()` runs. Phase 4's design resolves this by
   making the migration's own DDL idempotent (`CREATE TABLE IF NOT
   EXISTS`, executed literally both by goose later and by `Load()`'s own
   bootstrap path immediately) and by wrapping the whole bootstrap
   sequence in a Postgres session-level advisory lock — needed because
   `CREATE TABLE IF NOT EXISTS` alone is not safe against genuinely
   concurrent first-boot callers (a documented Postgres catalog-race
   caveat), unlike the row-level `ON CONFLICT DO NOTHING` guard alone,
   which mod-core's simpler case could rely on because its table already
   existed.

Both of these are deliberate, justified *mechanism* divergences from
`mod-core`'s design, not a break from the *pattern* the sibling plan asked
this plan to consider: both modules still land on "a small owned table
with a singleton-row + CHECK-constraint guard, migrated via this module's
own `model/migrations/`." See
[001-add-database-url-env-fallback.md](./phase-03-db-url-fallback/001-add-database-url-env-fallback.md)
and
[001-add-auth-jwt-secrets-schema.md](./phase-04-jwt-secret-bootstrap/001-add-auth-jwt-secrets-schema.md)
for the concrete design.

### Phase 3 — `db-url-fallback` (Database URL Env Var Fallback)

One task:

1. [`001-add-database-url-env-fallback.md`](./phase-03-db-url-fallback/001-add-database-url-env-fallback.md)
   — `cfg.DB.URL` resolves `MFAPP_DATABASE_URL` when set, else `DB_URL`,
   permanently (no removal plan); `validate()`'s required-field label
   updated to name both; `.env.example` documents both. Unit tests for
   precedence, fallback, and the (unmodified, still-passing) both-absent
   aggregated-error case.

### Phase 4 — `jwt-secret-bootstrap` (Durable JWT Secret Bootstrap)

Three strictly sequential tasks (each depends on the previous), and this
phase as a whole depends on Phase 3 landing first (its bootstrap path
consumes the `cfg.DB.URL` value Phase 3's fallback resolves):

1. [`001-add-auth-jwt-secrets-schema.md`](./phase-04-jwt-secret-bootstrap/001-add-auth-jwt-secrets-schema.md)
   — new migration `model/migrations/sql/0102_auth_jwt_secrets.sql`
   (idempotent `CREATE TABLE IF NOT EXISTS`, deliberately, for the reason
   above) for a private, single-row `auth_jwt_secrets` table, plus the two
   sqlc queries (`model/queries/auth_jwt_secrets.sql`) and regenerated
   `model/db/` code the implementation task needs.
2. [`002-implement-jwt-secret-bootstrap.md`](./phase-04-jwt-secret-bootstrap/002-implement-jwt-secret-bootstrap.md)
   — the new bootstrap path in `api/internal/config`: a narrow
   `JWTSecretQuerier` interface + unit-testable
   `fetchOrGeneratePersistedJWTSecret` (mirrors mod-core's
   `fromPersistedOrGenerated` shape), plus the DB-connecting outer wrapper
   `bootstrapJWTSecretFromDB` (dedicated `pgx.Connect`, session-level
   advisory lock, idempotent table-ensure, then the fetch-or-generate-persist
   call), wired into `Load()` right after `cfg.DB.URL` is resolved and
   before `validate()` runs. `Load()`'s exported signature does not change.
3. [`003-test-jwt-secret-race-and-corruption-paths.md`](./phase-04-jwt-secret-bootstrap/003-test-jwt-secret-race-and-corruption-paths.md)
   — unit tests (fake-querier-backed, every branch) plus a real-Postgres
   integration suite proving the DB-level guards actually hold, including
   the scenario `mod-core`'s precedent did not need: a truly unmigrated,
   fresh database, and a genuinely concurrent multi-caller first boot
   against it.

### Phase 5 — `doc-updates` (Documentation Updates)

Registered per the architectural-implications check: this plan adds
significant new tracked state (a new table) and changes `Load()`'s
observable behavior/failure-mode contract (new env var precedence, new
DB-dependent failure mode when `JWT_SECRET` is absent). One task:

1. [`001-update-architecture-docs.md`](./phase-05-doc-updates/001-update-architecture-docs.md)
   — update `docs/architecture.md` (Data model table, Runtime service
   dependencies / a new configuration-and-secrets-bootstrap note),
   `AGENTS.md`'s Database migrations section, and `.env.example`'s
   trailing comments, to describe both changes as shipped.

## What shipped

### Phase 03 — Database URL Env Var Fallback

1. **Add MFAPP_DATABASE_URL / DB_URL Fallback** (`001-add-database-url-env-fallback.md`, tier `sonnet-med`) — Added a resolveDBURL() helper to api/internal/config/config.go that prefers MFAPP_DATABASE_URL and falls back to DB_URL (with a slog.Debug note on the fallback path, deliberately not phrased as deprecation), wired it into Load()'s DBConfig.URL field, and relabeled validate()'s required-field check to "MFAPP_DATABASE_URL / DB_URL" — the label still contains "DB_URL" so the existing aggregated-error test keeps passing unmodified, as designed. Updated the DBConfig.URL field comment and Load()'s doc comment to describe the new precedence, and added a corresponding explanatory comment block to .env.example above the existing DB_URL= line, without removing or altering that default. Added three new TestLoad subtests covering precedence, fallback, and the neither-set error case (asserting both names appear in the error). All Requirements/Validation items pass; go build, make lint, and go test ./internal/config/... are all green, and the grep check confirms both the new symbol and env-var name appear in both target files. Only the files named in the task doc's Assumptions were touched.
   Commit `5ff2493`, merged at `e5b307afb26ea281334ef524112ed40af6b7a297`.

### Phase 04 — Durable JWT Secret Bootstrap

1. **Add Auth JWT Secrets Schema** (`001-add-auth-jwt-secrets-schema.md`, tier `sonnet-high`) — Added the auth_jwt_secrets migration (goose, deliberately CREATE TABLE IF NOT EXISTS per the task doc's header note — load-bearing for Task 002's fresh-database bootstrap path) and its paired sqlc query file (GetJWTSecret, InsertJWTSecretIfAbsent), then regenerated model/db/ via sqlc generate. All task-doc validation checks pass including manual verification of the goose Down reversal and cd api && go build ./.... make lint needed SHADOW_DB_PREREQ_DIRS pointed at mod-core/model/migrations to run at all — a pre-existing, already-tracked tooling gap unrelated to this change.
   Commit `6f65a50`, merged at `5aa7a9414f06210f6dabf4804a65068c4b997bca`.

2. **Implement JWT Secret Bootstrap** (`002-implement-jwt-secret-bootstrap.md`, tier `sonnet-high`) — Implemented the JWT-secret DB bootstrap per the task doc's prescriptive code: jwtsecret_bootstrap.go carrying JWTSecretQuerier, fetchOrGeneratePersistedJWTSecret, and bootstrapJWTSecretFromDB (advisory lock, idempotent DDL byte-identical to migration 0102). Wired into Load() with exported signature unchanged. Resolved a task-doc inconsistency (import alias) in favor of the explicit prose instruction (db alias, matching provider_merge.go). Fixed a pre-existing go-vet gap from task 001 (stubUAQuerier missing two new Querier methods) as a single-file carve-out since it blocked this task's own make lint validation. All builds/vet/lint/tests green; DDL constant verified byte-for-byte identical to the migration via scripted comparison.
   Commit `b851668`, merged at `9d17ffae9d932c31d082d251ccecd09776b38346`.

3. **Test JWT Secret Race And Corruption Paths** (`003-test-jwt-secret-race-and-corruption-paths.md`, tier `sonnet-high`) — Implemented all three requirement layers: (1) fake-Querier unit tests covering all five branches of fetchOrGeneratePersistedJWTSecret; (2) a connection-error unit test and a drift-guard test verifying bootstrapJWTSecretDDL matches migration 0102 byte-for-byte; (3) a real-Postgres integration suite proving fetch-or-generate round trip, fresh/unmigrated-database bootstrap, an 8-goroutine concurrent race (verified to converge on one row/one secret), and a real CHECK-constraint rejection. Discovered and empirically verified that mod-users' own plain migrations are not self-contained as the task doc assumed (migration 0100 depends on core-model tables outside this module) and designed a clean-skip mechanism for the two affected scenarios. All non-integration validation green; integration tier verified to skip cleanly (not fail) under every prerequisite-missing condition present in this sandbox.
   Commit `c8fe54d`, merged at `86a4de987b12c6776a37994bb371a94917f9f92d`.

### Phase 05 — Documentation Updates

1. **Update Architecture Docs** (`001-update-architecture-docs.md`, tier `sonnet-high`) — Updated docs/architecture.md: added an auth_jwt_secrets row to the Data model table, and a new Configuration and secrets bootstrap subsection describing the permanent MFAPP_DATABASE_URL/DB_URL precedence and Load()'s fetch-or-generate-persist JWT_SECRET fallback, framed per gate finding F1 as a new pre-pool, per-boot Postgres dependency. Per F2, added a paragraph to the API layer section naming config.Load()'s raw pgx.Connect bootstrap as the one deliberate exception to the module's requires.infra:pool-mediated Postgres access pattern. Reviewed AGENTS.md, .env.example, docs/mod-users-spec.md, README.md, next-steps.md and found no contradicting claims needing edits.
   Commit `138baa7`, merged at `eca7d2f7e56e6f82c0048192ed386b7d2caffaf0`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Follow-up items

- **`ZYJw`** — **phase-04 task 001's landing left a pre-existi** — phase-04 task 001's landing left a pre-existing go-vet gap (stubUAQuerier in api/internal/service/user_accounts_upgrade_test.go missing two Querier methods) that this task had to fix in-scope because it blocked this task's own make lint validation — task 001's own validation only ran go build, not go vet, so the gap wasn't caught there.

- **`2jiE`** — **Task doc Requirement 2's code block used a di** — Task doc Requirement 2's code block used a different import alias (usersdb) than its own accompanying prose instructed (db) — resolved per the prose instruction; worth correcting the task-doc template's illustrative snippet for future readers.

- **`zFMq`** — **Empirically verified limitation in the task d** — Empirically verified limitation in the task doc's Requirement 4 assumption: mod-users' own plain migrations (usersmigrations.Migrate, applying 0100-0102 as one embedded set) are NOT self-contained as characterized ("since auth_jwt_secrets has no FKs outside itself") — migration 0100_schema.sql carries FKs into core-model tables (legal_entities, apps) that a lighter, non-composed shadow DB never creates. Reproduced directly: usersmigrations.Migrate against a genuinely bare DB fails with SQLSTATE 42P01 on 0100_schema.sql. This blocks a real (non-skipped) run of the migrated-DB round-trip scenario and the convergence tail of the fresh-database scenario in any environment with working DB credentials — currently they skip via a detected-42P01 guard rather than fail. Manager should decide: (a) revise Requirement 4 to use the composed core+authz+users schema for those two scenarios (mirroring authz_integration_test.go rather than mod-core's self-contained precedent), or (b) accept the current skip-on-missing-prerequisite behavior as permanent. The concurrent-race and corruption-detection scenarios are unaffected and always exercise real Postgres behavior whenever the DB is reachable.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 03 — Database URL Env Var Fallback

- [x] [001-add-database-url-env-fallback.md](./phase-03-db-url-fallback/001-add-database-url-env-fallback.md) — tier `sonnet-med` · branch `plan/catalog-readiness-03-001` · commit `5ff2493` · merge `e5b307afb26ea281334ef524112ed40af6b7a297`

### Phase 04 — Durable JWT Secret Bootstrap

- [x] [001-add-auth-jwt-secrets-schema.md](./phase-04-jwt-secret-bootstrap/001-add-auth-jwt-secrets-schema.md) — tier `sonnet-high` · branch `plan/catalog-readiness-04-001` · commit `6f65a50` · merge `5aa7a9414f06210f6dabf4804a65068c4b997bca`
- [x] [002-implement-jwt-secret-bootstrap.md](./phase-04-jwt-secret-bootstrap/002-implement-jwt-secret-bootstrap.md) — tier `sonnet-high` · branch `plan/catalog-readiness-04-002` · commit `b851668` · merge `9d17ffae9d932c31d082d251ccecd09776b38346`
- [x] [003-test-jwt-secret-race-and-corruption-paths.md](./phase-04-jwt-secret-bootstrap/003-test-jwt-secret-race-and-corruption-paths.md) — tier `sonnet-high` · branch `plan/catalog-readiness-04-003` · commit `c8fe54d` · merge `86a4de987b12c6776a37994bb371a94917f9f92d`

### Phase 05 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-05-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `plan/catalog-readiness-05-001` · commit `138baa7` · merge `eca7d2f7e56e6f82c0048192ed386b7d2caffaf0`
