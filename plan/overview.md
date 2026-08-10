# Catalog Readiness — mod-users: `MFAPP_DATABASE_URL` fallback and durable `JWT_SECRET` bootstrap

## Purpose and scope

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

## Current status

**`complete`.** Investigation of the real code
(`api/internal/config/config.go`, `api/internal/config/config_test.go`,
`api/internal/config/provider_merge.go`, `api/internal/db/pool.go`,
`api/localdb/pool.go`, `model/migrations/`, `model/db/`,
`moduleforge.module.yaml`) and of the real generated `app-mftodo/cmd/server/
main.go` (read-only reference, confirmed via the sibling plan's own
research and re-confirmed directly here) resolved every open design
question with enough confidence to fully decompose the work — no research
delegation blocks task breakdown. Two design points diverge from the
change request's own suggested framing and are called out explicitly
under [Deferred and flagged](#deferred-and-flagged), returned via
`flagged_for_manager` since neither blocks starting this phase. Phase 3
(`db-url-fallback`) begins first; Phase 4 (`jwt-secret-bootstrap`) depends
on it (its bootstrap path reads the `cfg.DB.URL` value Phase 3's fallback
resolves). A Phase 5 `doc-updates` phase follows per the
architectural-implications check (new tracked state, and `Load()`'s
observable behavior/failure-mode contract changes).

## Overview

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

## Deferred and flagged

Returned via `flagged_for_manager` (not `user_questions` — neither blocks
this phase's task breakdown, which is fully resolved):

- **Permanent dual-name support, not a deprecation window.** The change
  request's own suggested design for item A was "prefer
  `MFAPP_DATABASE_URL`, fall back to `DB_URL` with a deprecation log line,
  remove the fallback in a later version." This plan does not adopt the
  removal half: `app-mfmanager`'s own conventions (see "Key research
  findings" above) show `DB_URL` is not a legacy name being phased out —
  it is the permanent, generic name for a self-hosted/non-catalog Postgres
  connection, while `MFAPP_DATABASE_URL` is specifically the name
  `app-mfmanager`'s catalog-deploy engine injects into the apps *it*
  manages. Both names denote genuinely different, permanently-coexisting
  deployment topologies, not an old-vs-new pair — so this plan's task
  design supports both indefinitely, with no removal milestone, and no
  "deprecation" framing in the log line it emits (a neutral
  which-source-was-used debug note, not a warning). Worth the user's or
  module owner's explicit sign-off, since it diverges from the request's
  own suggested framing.
- **A new DB-dependent failure mode inside `Load()`.** Previously,
  `Load()` was pure and network-free. When `JWT_SECRET` is absent and the
  resolved DB URL is present but the database is unreachable, `Load()` can
  now fail for connectivity reasons rather than only "missing env var"
  reasons. Every composing app's generated `main.go` already treats any
  `Load()` error as fatal (`log.Fatalf("init cfg: %v", err)`), so this is a
  fail-fast, not a silent behavior change, but it is a new I/O dependency
  inside a function every composing app calls first, worth the module
  owner's awareness.
- **Cross-module pattern consistency, resolved with a justified mechanism
  divergence.** The sibling `mod-core` plan flagged, as its own open
  question, whether `mod-users`' analogous `JWT_SECRET` work should mirror
  its owned-table pattern. This plan's answer is yes at the pattern level
  (small owned table, singleton row, CHECK-constraint guard, migrated via
  `model/migrations/`) but no at the mechanism level: this plan's table
  migration is deliberately idempotent (`CREATE TABLE IF NOT EXISTS`,
  which `mod-core`'s bare `CREATE TABLE` is not), and its bootstrap path
  additionally uses a Postgres session-level advisory lock that
  `mod-core`'s design has no counterpart for — both required specifically
  because `Load()` runs before this module's own migrations apply, which
  is not true of `mod-core`'s cipher-init call. Worth the user's or module
  owners' awareness that the two modules' mechanisms differ even though
  their patterns now match, and why.
- **`docs-mf-standards` content update.** Out of this project's
  `project_root` (a separate git submodule repository); not actioned by
  this plan, matching the sibling `mod-core` plan's identical precedent
  for the identical constraint. Needs its own dispatch against that repo
  directly, informed by this plan's and `mod-core`'s shipped conventions.
