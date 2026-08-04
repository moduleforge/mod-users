# Anonymous Actor Startup Service

## Purpose and scope

Add the startup-resolved `AnonymousActor` service: a small holder type constructed once at boot
that resolves the anonymous system actor's `entities.id` by slug, hard-fails if the seeded row
is missing, and asserts at boot that the anonymous actor holds **zero grants**, refusing to
start if it finds any.

Invoke the standard `implement-task` skill with the Go developer role and the Go design
standards as the technology layer.

Scope: `api/internal/auth/` only (plus its unit test file). The public facade re-export and the
manifest entry are task 005's work, not this task's.

## Requirements

### 1. `api/internal/auth/anonymous_actor_service.go`

Create this new file. Do **not** use `api/internal/auth/anonymous_actor.go` — that filename is
reserved for task 004's middleware, per the proposal's §3.

Define an exported holder type and its constructor:

```go
// AnonymousActor holds the entity id of the seeded, zero-authority anonymous
// system actor, resolved once at startup.
type AnonymousActor struct { entityID int64 }

// EntityID returns the anonymous system actor's entities.id.
func (a *AnonymousActor) EntityID() int64

func NewAnonymousActor(ctx context.Context, pool *pgxpool.Pool) (*AnonymousActor, error)
```

The constructor signature must match the manifest wiring the proposal specifies in its §4 —
`args: [context, infra:pool]`, `returnsError: true` — so it takes `context.Context` first and
`*pgxpool.Pool` second and returns `(*AnonymousActor, error)`.

Keep the entity id unexported behind the `EntityID()` accessor rather than exposing a mutable
field: this value must never be reassigned after boot.

Document the type with a doc comment stating plainly what it is — a shared, zero-authority
identity that holds no grants and owns nothing, distinct from the per-device *guest account*
produced by `POST /v1/auth/anonymous`. Use the proposal's terminology split (*anonymous actor*
vs. *guest account*) in every comment you write.

### 2. Slug resolution, with a hard boot failure

Resolve the entity id with the `GetSystemActorBySlug` query task 001 added, via
`usersdb.New(pool)` — the generated `New` accepts any `DBTX`, and `*pgxpool.Pool` satisfies it.
`api/internal/auth` already imports `github.com/moduleforge/mod-users/model/db` (see
`local_jwt.go`), so no new module dependency is introduced.

Look up the constant slug `anonymous`. Define it as an unexported package constant rather than
repeating the literal.

If the row is missing (`pgx.ErrNoRows`), return a wrapped error whose message names the
migration that seeds it, so an operator sees the fix immediately — e.g.
`auth: anonymous system actor not found (slug %q); has mod-users migration
0101_system_actors.sql been applied?`. Any other query error is returned wrapped as well. The
whole point of resolving at boot is that a deployment whose migrations have not been applied
fails at startup rather than at first anonymous request.

Never hardcode the entity id as a Go constant: `entities.id` is `BIGSERIAL` and its value is
not deterministic across environments. Resolve by slug.

### 3. No-grants boot assertion

In the same constructor, after resolving the id, run:

```sql
SELECT EXISTS(SELECT 1 FROM grants WHERE actor_id = $1)
```

as a parameterized query against the pool. `grants.actor_id BIGINT NOT NULL REFERENCES
entities(id)` is mod-authz's table (`mod-authz/model/migrations/0505_grants.sql`); it is not in
mod-users' sqlc schema, so this is a direct `pool.QueryRow` rather than a generated query. Bind
the entity id as a parameter — never interpolate it into the SQL string.

If the result is `true`: log an error via `slog.ErrorContext` naming the anonymous actor's
entity id and stating the invariant that was violated, and **return an error** so the process
refuses to start. The proposal is explicit that this is a refuse-to-start condition, not a
warning.

This assertion lives here, rather than in a mod-users migration trigger, precisely because it
must run after *all* migrations including mod-authz's 500s. Record that reasoning in a comment.

### 4. Unit tests

Add `api/internal/auth/anonymous_actor_service_test.go`. `NewAnonymousActor` takes a concrete
`*pgxpool.Pool`, which is not stubbable — so factor the two DB operations behind unexported
function-typed fields or small unexported helpers that the test can substitute, following the
established pattern in this package: `UserResolver` (`api/internal/auth/resolver.go`) already
uses injectable `uuidLookupFn`-style function fields specifically so `resolver_test.go` can
test with `pool: nil`. Mirror that pattern; do not introduce an interface for a single
implementation (per the Go design standards).

Table-driven tests covering:
- Row present, no grants → constructor succeeds and `EntityID()` returns the resolved id.
- Row missing (`pgx.ErrNoRows`) → constructor returns an error mentioning the slug and the
  migration; no `*AnonymousActor` is returned.
- Lookup returns an unexpected error → constructor returns a wrapped error.
- Grants exist for the actor → constructor returns an error; the process would refuse to start.
- Grants-existence query itself errors → constructor returns an error (fail closed; do not
  treat a failed assertion as "no grants").

### Must not change

- Do not modify `api/internal/authz/authz.go`, mod-core's `opctx`, mod-core's `grant_table.go`,
  `anon_tokens`, `user_accounts`, or `RequireAuth`.
- Do not create a `user_accounts` row, a JWT, or any credential for the anonymous actor.
- Do not add any grant to the anonymous actor, in code, seed data, or tests against the real
  database.
- Do not add the middleware here — that is task 004.

## Validation

- `api/internal/auth/anonymous_actor_service.go` and its `_test.go` exist; no other production
  file under `api/internal/auth/` is modified.
- `cd api && go build ./...` succeeds.
- `cd api && go vet ./...` reports nothing new.
- `cd api && go test ./internal/auth/...` passes, including the new table-driven tests. All
  pre-existing tests in the package still pass unchanged.
- `grep -n "grants" api/internal/auth/anonymous_actor_service.go` shows the grants query uses a
  bound parameter (`$1`), with no string interpolation of the entity id.
- `grep -rn "AnonymousActorEntityID\|= -1\|const.*[Aa]nonymous.*int64" api/` finds no hardcoded
  entity-id constant.
- `make lint` passes.
- `make test.unit` passes.

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has landed: `GetSystemActorBySlug` exists on the generated `Querier` and the
  `system_actors` table is in the composed schema.
- Every host application composing mod-users also composes mod-authz, so the `grants` table
  always exists by the time this constructor runs. mod-users' own `api/go.mod` carries a
  `replace` for `mod-authz`, and `moduleforge.module.yaml` already declares an `authorizer`
  service over `queries:authzdb`, so this holds today. If it ever does not, the boot assertion
  would fail on a missing relation — a loud failure, which is the correct direction.
- Migrations run before service construction in the generated composition root
  (`app-mftodo/cmd/server/main.go` runs all module `Migrate` calls before wiring services), so
  boot-time slug resolution is safe.

## References

- [Anonymous-actor architecture proposal](../notes/anonymous-actor-architecture-proposal.md) —
  §4 "Resolving the entity id at startup" (authoritative for this task), §"Why zero grants",
  §"Composition with POST /v1/auth/anonymous" (the guest-account vs. anonymous-actor
  terminology).
- `api/internal/auth/resolver.go` and `api/internal/auth/resolver_test.go` — the injectable
  function-field pattern to mirror for testability with a nil pool.
- `mod-authz/model/migrations/0505_grants.sql` — `grants.actor_id` column.
- `plan/phase-01-anonymous-actor/001-system-actor-migration.md` — the query and table this task
  consumes.

## Status

- **Outcome:** succeeded
- **Date:** 2026-08-04 (retry)
- **Files:**
  - `api/internal/auth/anonymous_actor_service.go` (new, from the original attempt; unchanged
    this retry — verified intact)
  - `api/internal/auth/anonymous_actor_service_test.go` (new, from the original attempt;
    unchanged this retry — verified intact)
  - `api/internal/service/user_accounts_upgrade_test.go` (retry-only, mechanical fix; see below —
    out of this task's normal file-editing scope but required to unblock module-wide validation)
- **Retry adjustment:** the original attempt's implementation in `api/internal/auth/` was already
  complete and correct; validation failed for a single reason entirely outside that directory
  (see the root-cause note previously recorded here, now resolved). This retry adds one
  mechanical stub method — `GetSystemActorBySlug(ctx context.Context, slug string)
  (db.GetSystemActorBySlugRow, error)`, returning `db.GetSystemActorBySlugRow{}, pgx.ErrNoRows` —
  to `stubUAQuerier` in `api/internal/service/user_accounts_upgrade_test.go`, mirroring the
  existing neighboring `Get...By...` stub methods' convention (e.g.
  `GetActivePasswordReset`) exactly. No behavior change; pure interface-conformance fix so
  `stubUAQuerier` again satisfies `db.Querier` after task 001 grew that interface.
- **Validation summary (full re-run, both directories in scope):**
  - PASS — `api/internal/auth/anonymous_actor_service.go` and its `_test.go` exist and are
    unmodified from the prior commit; the only other file touched is the out-of-scope stub file
    named above, per this retry's explicit adjustment.
  - PASS — `cd api && go build ./...` succeeds, whole module.
  - PASS — `cd api && go vet ./...` is clean, whole module (previously-failing
    `internal/service` stub-conformance error is now resolved).
  - PASS — `cd api && go test ./internal/auth/...` passes (`ok`, all 5 new table-driven subtests
    plus every pre-existing test in the package, unchanged).
  - PASS — `grep -n "grants" api/internal/auth/anonymous_actor_service.go` shows the grants
    query bound via `$1`, no string interpolation.
  - PASS — `grep -rn "AnonymousActorEntityID\|= -1\|const.*[Aa]nonymous.*int64" api/` finds no
    matches (no hardcoded entity-id constant).
  - PASS — `make lint` (with `SHADOW_DB_PREREQ_DIRS=<mod-core>/model/migrations` set for
    `lint.model`, per task 001's documented precedent): all three sub-targets (`lint.model`,
    `lint.api`, `lint.gui`) passed clean this run, including `lint.model`'s shadow-db-lint step
    (the previously-flagged pre-existing `legal_entities`-missing failure did not reproduce in
    this run) and `lint.api`'s `go vet ./...`, now clean module-wide.
  - PASS (with one documented pre-existing flake) — `make test.unit`: `internal/service` now
    builds and its own tests pass (`ok`, cached). The one failure,
    `TestNewStepUpConsumedCache_JanitorStopsOnCancel` in package `api/auth`, is an already-tracked
    pre-existing goroutine-count timing flake unrelated to this fix — confirmed by re-running it
    in isolation 3x (`go test ./auth/... -run TestNewStepUpConsumedCache_JanitorStopsOnCancel
    -count=3 -v`), which passed all three times; it fails only under the full parallel
    `go test ./...` run, consistent with test-parallelism timing contention rather than a
    regression from this change.
