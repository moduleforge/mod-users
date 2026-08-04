# Anonymous system actor (mod-users)

## Purpose and scope

Ship, entirely within `mod-users`, the anonymous-actor mechanism specified by
[the anonymous-actor architecture proposal](./notes/anonymous-actor-architecture-proposal.md):
a genuinely tokenless HTTP request (no `Authorization` header) must resolve to a real,
low-privilege actor identity on the operation context **before** authorization checks run.

This closes the mod-users side of mod-repos followup `Jhkw`. The proposal document is the
**source of truth** for the design; this plan implements it and does not re-derive or
re-litigate any of its decisions.

Three supporting research notes provide background but are subordinate to the proposal:
[current-state audit](./notes/current-state-audit.md) (mod-users' actor-resolution chain at
file:line granularity), [mod-repos consumer context](./notes/mod-repos-consumer-context.md)
(why `Jhkw` blocks on mod-users), and [design-space survey](./notes/design-space-survey.md)
(the option space and its safety risks). The
[mfgen expr/middleware pattern note](./notes/mfgen-expr-middleware-pattern.md) records prior
verification that mod-users' manifest wiring needs no mfgen change.

### What must change

1. **Migration.** A new mod-users migration `model/migrations/sql/0101_system_actors.sql`
   (module range 100–199, `after: [core]`) seeding a `system_actor` entity type, a
   `system_actors` class-table-inheritance child table, exactly one seeded row (slug
   `anonymous`), and an `entities_no_system_actor_owner` `BEFORE INSERT OR UPDATE` trigger on
   `entities` rejecting any row whose `owner_id` resolves to a `system_actors` row. One new
   sqlc query `GetSystemActorBySlug`; `model/db/` regenerated via `sqlc generate`.
2. **Startup-resolved service.** `NewAnonymousActor` resolves the anonymous actor's entity id
   by slug at boot, hard-fails if the row is missing, and performs a boot-time assertion that
   the anonymous actor holds **zero grants**, refusing to start if it finds any.
3. **Middleware.** `ResolveActorOrAnonymous` in `api/internal/auth/`, behaviorally identical to
   `RequireAuth` except that a genuinely **missing** `Authorization` header falls through with
   `opctx.WithActor(anonID)` instead of 401. Invalid/expired tokens still 401 — no silent
   downgrade. The shared success-population and error-mapping logic is first extracted out of
   `RequireAuth` into a helper both middlewares call, with `RequireAuth`'s own behavior kept
   bit-identical. `auth.IsAnonymousActor(ctx)` is exposed as a distinct context accessor.
4. **Wiring.** Facade re-exports in `api/auth/auth.go`; `moduleforge.module.yaml`
   `provides.services` + `provides.middleware` entries per the proposal's exact shape; the
   equivalent hand-wiring in the non-generated dev server `api/cmd/server/main.go`.
5. **Tests.** The full list the proposal enumerates in its "Migration / rollout notes —
   Phase 1" step 5.
6. **Docs.** `docs/architecture.md` and `docs/mod-users-spec.md` — the guest-account vs.
   anonymous-actor distinction, the per-IP rate-limiting precondition, the
   `Vary: Authorization` caching note, the composition rule (never combine with
   `requireAuth`/`requireVerifiedEmail`), and correction of two stale claims.

### What must not change

Hard constraints, all from the proposal:

- No changes to `api/internal/authz/authz.go`, mod-core's `opctx`, mod-core's
  `grant_table.go`, `anon_tokens`, `user_accounts`, `RequireAuth`'s **behavior**, or any
  existing route's `scope:`.
- `system_actor` must **not** be added to `authzSlugs` in `api/cmd/server/main.go` — it stays
  invisible to every list path, and no `accessible_system_actor_ids_for_actor` function is
  ever generated.
- The existing `POST /v1/auth/anonymous` guest-account flow is **not** converged with this
  mechanism. Only their terminology and documentation are disambiguated.
- No mfgen `scope: anonymous` literal. This plan ships entirely on the existing
  `scope: public` + named `middleware:` mechanism, with no cross-project change.
- The anonymous actor must never hold a grant, never own an entity, never join an actor
  group, and never be resolvable from a JWT.

### Success criteria

- A request with no `Authorization` header, routed through `ResolveActorOrAnonymous`, reaches
  the handler with `opctx.ActorEntityID(ctx)` set to the seeded anonymous actor's entity id,
  with neither `opctx.WithSudoActor` nor `WithUserContext` set, and with
  `auth.IsAnonymousActor(ctx) == true`.
- A request with an invalid or expired token through the same middleware still gets 401.
- A request with a valid token through the same middleware gets its own actor, exactly as
  `RequireAuth` would.
- `RequireAuth`'s observable behavior is unchanged, proven by characterization tests written
  against its pre-refactor behavior.
- `Authorize` for the anonymous actor returns `ErrForbidden`, not `ErrUnauthenticated`.
- The database rejects an `entities` row owned by the anonymous actor, and rejects the
  anonymous actor as an actor-group member.
- A deployment whose migration has not been applied fails at boot, not at first request.
- `docs/architecture.md` and `docs/mod-users-spec.md` describe the new mechanism accurately
  and no longer carry the two stale `is_anonymous` claims.

### Out of scope

Explicitly not planned here, and no tasks exist for them:

- mod-repos' own follow-up work (flipping its route scopes to `scope: public` +
  `middleware:`, adding its public-visibility arm to its list-scoping query and decorating
  `Authorizer`, replacing its test-only fake actor with a real end-to-end test). That is
  tracked in the mod-repos repo.
- The optional mfgen `scope: anonymous` ergonomic sugar.
- An in-process per-IP rate limiter. The proposal documents per-IP throttling as a hard
  precondition on any route that opts into the middleware, and states that shipping the
  limiter itself is out of scope for v1 — the right layer is the gateway.

## Current status

Plan created; no tasks executed. Phase 01 (`anonymous-actor`) begins first, starting with
`001-system-actor-migration` and `003-requireauth-helper-extraction`, which are independent of
each other and can be dispatched concurrently.

Pre-conditions for execution:

- Dependencies are **not installed** in the plan worktree. Per `AGENTS.md`, run `bun install`
  at the worktree root and copy `.env` from the main checkout before building. `make preflight`
  (a prerequisite of `build`/`test`) runs `scripts/link-siblings.sh`, which plants the
  compatibility symlinks that make `api/go.mod`'s `../../mod-core/*`, `../../mod-audit/*`, and
  `../../mod-authz/*` replace directives resolve from a nested worktree.
- The DB-level tasks (`001`, `006`) need a running Postgres (`make dev.start`) and the composed
  migrations directory (`make -C model compose`). `sqlc` and `goose` must be on `PATH`.
- Integration tests run with `AUTHZ_DEV_PG_HOST=localhost go test -tags=integration -p 1 ./...`
  from `api/`; on a Docker Desktop for macOS host the shared Postgres container is reachable at
  `localhost`, not the container's docker-network IP. Do not rediscover this.

## Overview

The work is one coherent feature slice and is planned as a **single implementation phase**
followed by the standard documentation phase, because the change introduces a new subsystem
(a zero-authority shared actor identity) and a cross-cutting auth-pipeline addition.

### Phase 01 — Anonymous System Actor

Six tasks. Order below reflects dependency order.

1. **`001-system-actor-migration`** — Author `model/migrations/sql/0101_system_actors.sql`
   (type seed, `system_actors` CTI table + type-check trigger, the one seeded `anonymous` row,
   and the `entities_no_system_actor_owner` ownership guard), add the `GetSystemActorBySlug`
   sqlc query, and regenerate `model/db/`. *Depends on: nothing.*
2. **`002-anonymous-actor-service`** — `AnonymousActor` holder and `NewAnonymousActor`
   constructor in `api/internal/auth/`: slug lookup, missing-row hard failure, and the
   no-grants boot assertion. *Depends on: 001 (needs the generated query).*
3. **`003-requireauth-helper-extraction`** — Add characterization tests pinning every one of
   `RequireAuth`'s existing branches, then extract the shared success-population and
   error-mapping logic into one unexported helper. `RequireAuth`'s behavior stays
   bit-identical. *Depends on: nothing. Parallel-eligible with 001.*
4. **`004-resolve-actor-or-anonymous-middleware`** — The new `ResolveActorOrAnonymous`
   middleware and the `IsAnonymousActor` context accessor, plus the enumerated middleware
   behavior tests. *Depends on: 003. Parallel-eligible with 002.*
5. **`005-facade-and-manifest-wiring`** — `api/auth/auth.go` re-exports,
   `moduleforge.module.yaml` `provides.services` + `provides.middleware` entries, and the
   hand-wiring in the non-generated dev server `api/cmd/server/main.go`.
   *Depends on: 002 and 004.*
6. **`006-db-invariant-integration-tests`** — Integration tests (build tag `integration`) for
   the database-enforced invariants: the ownership trigger on insert and update, actor-group
   rejection, `Authorize` returning `ErrForbidden` rather than `ErrUnauthenticated`, and the
   structural impossibility of a `user_accounts` row for the anonymous actor.
   *Depends on: 001. Parallel-eligible with 002, 004, and 005.*

Parallel-eligible groups: `{001, 003}`, then `{002, 004, 006}`, then `{005, 006}`.

### Phase 02 — Documentation Updates

One task, registered by the architectural-implications check: review and update
`docs/architecture.md` and `docs/mod-users-spec.md` for the new subsystem, the guest-account
vs. anonymous-actor terminology split, the operational preconditions the proposal states
(per-IP rate limiting, `Vary: Authorization`, composition rule, audit-attribution collapse),
and the two stale `is_anonymous` claims the current-state audit found.
