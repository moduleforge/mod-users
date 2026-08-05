# Facade Manifest And Dev Server Wiring

## Purpose and scope

Make the anonymous actor reachable from outside `api/internal/`: re-export it through the public
facade `api/auth/auth.go`, declare the `provides.services` and `provides.middleware` entries in
`moduleforge.module.yaml` exactly as the proposal specifies, and hand-wire the equivalent in the
non-generated dev server `api/cmd/server/main.go`.

Invoke the standard `implement-task` skill with the Go developer role.

Scope: `api/auth/auth.go`, `moduleforge.module.yaml`, `api/cmd/server/main.go`, and a facade
unit test.

## Requirements

### 1. `api/auth/auth.go` re-exports

`AGENTS.md`'s Conventions section is emphatic about this and cites a real production break:
**any** handler/constructor/register function referenced by `moduleforge.module.yaml`'s
`constructor:` or `register:` keys must be re-exported through this facade in the same change.
External composition roots import `github.com/moduleforge/mod-users/api/auth` and cannot see
`internal/` symbols; a missing re-export compiles fine here and breaks `mfgen generate` output
in every consuming app.

Add, following the file's existing type-alias + thin-wrapper style:

- `type AnonymousActor = inner.AnonymousActor` — required because the manifest declares
  `type: "*auth.AnonymousActor"`.
- `func NewAnonymousActor(ctx context.Context, pool *pgxpool.Pool) (*AnonymousActor, error)` —
  delegating to `inner.NewAnonymousActor`.
- `func NewResolveActorOrAnonymous(verifier *Verifier, mapper ClaimMapper, resolver *UserResolver,
  anon *AnonymousActor) func(http.Handler) http.Handler` — the **adapter** the manifest names as
  its middleware constructor. It unwraps the holder (`anon.EntityID()`) and delegates to
  `inner.ResolveActorOrAnonymous(verifier, mapper, resolver, anon.EntityID())`.

  This adapter is the reconciliation between the proposal's §3 internal signature (which takes a
  bare `int64`) and its §5 manifest wiring (which passes `service:anonymousActor`). Keep the
  internal function's `int64` signature as-is; do the unwrapping here. Document that in the
  adapter's doc comment.
- `func ResolveActorOrAnonymous(verifier *Verifier, mapper ClaimMapper, resolver *UserResolver,
  anonActorEntityID int64) func(http.Handler) http.Handler` — a plain re-export of the internal
  form, matching how `RequireAuth` is re-exported at `auth.go:89-93`, for callers (including the
  dev server) that already hold the id.
- `func IsAnonymousActor(ctx context.Context) bool` — the context accessor, re-exported so
  handlers and observers in other modules can call it.

Do not re-export `WithAnonymousActor`: only the middleware may set the marker.

### 2. `moduleforge.module.yaml`

Add one `provides.services` entry, placed alongside the other auth services (near `verifier` /
`claimMapper` / `userResolver`), using the proposal's exact shape:

```yaml
    - name: anonymousActor
      type: "*auth.AnonymousActor"
      constructor: auth.NewAnonymousActor
      returnsError: true
      args:
        - context
        - infra:pool
```

And one `provides.middleware` entry, appended to the existing three (`requireAuth`,
`requireVerifiedEmail`, `requireOIDCConfirmed`) at the end of the `middleware:` block, again
exactly as the proposal specifies:

```yaml
    - name: resolveActorOrAnonymous
      constructor: auth.NewResolveActorOrAnonymous
      args:
        - service:verifier
        - service:claimMapper
        - service:userResolver
        - service:anonymousActor
```

Both entries get an explanatory YAML comment in the style of their neighbours. The middleware
comment must state that it is opt-in, that consuming modules reference it by name from a
`scope: public` route group's `middleware:` list (with `requireOIDCConfirmed` first), and that it
must never be combined with `requireAuth` or `requireVerifiedEmail`.

`infra:pool` is already declared under `requires.infra`, so no `requires:` change is needed.

**Consequence to state in the comment and in the task report:** `provides.middleware` nodes are
unconditional reachability roots in mfgen (`internal/resolver/reachability.go`), so
`resolveActorOrAnonymous` — and therefore `anonymousActor` — is constructed in *every* consuming
app's composition root, whether or not that app uses the middleware. That means every consuming
app now hard-fails at boot if mod-users' `0101_system_actors.sql` has not been applied. This is
the proposal's intended fail-fast design, and it is safe because generated composition roots run
all module migrations before constructing services (verified in
`app-mftodo/cmd/server/main.go`, where the `Migrate` calls precede service wiring). Do not try
to make the service lazy or optional.

### 3. `api/cmd/server/main.go` hand-wiring

mfgen does not regenerate this file — it is a hand-written standalone dev server, as its own
comment at `main.go:522-533` states — so the equivalent wiring must be added by hand for the
capability to be exercisable locally.

Construct the actor and the middleware alongside the existing auth components (the `verifier` /
`localMapper` / `resolver` block, roughly `main.go:81-100`, and `requireConfirmed` at
`main.go:502`):

```go
anonActor, err := auth.NewAnonymousActor(ctx, pool)   // exit(1) with a clear log on error
resolveActorOrAnonymous := auth.NewResolveActorOrAnonymous(verifier, localMapper, resolver, anonActor)
```

Follow the file's existing error convention (`slog.ErrorContext(...)` then `os.Exit(1)`) — a
missing seeded row must stop the dev server, matching the production contract.

Add a demonstration mount that exercises the middleware without changing any existing route's
behavior: a new sibling `r.Route`/`r.Group` under `/v1` that uses `requireConfirmed` then
`resolveActorOrAnonymous`, mirroring the shape mfgen emits for `scope: public` +
`middleware: [requireOIDCConfirmed, resolveActorOrAnonymous]`:

```go
r.Route("/v1/<prefix>", func(r chi.Router) {
    r.Use(requireConfirmed)
    r.Use(resolveActorOrAnonymous)
    // ...
})
```

Keep the demonstration minimal and clearly marked as dev-server-only. Do **not** move, re-scope,
or re-nest any existing route: the three-tier `requireOIDCConfirmed` → `requireAuth` →
`requireVerifiedEmail` nesting at `main.go:511-596` stays byte-for-byte as it is, and
`resolveActorOrAnonymous` never appears inside it. Add a comment recording that this block
mirrors the manifest entry, in the style of the file's existing `TODO(generated):` markers.

### 4. Facade test

Add or extend a test under `api/auth/` asserting the facade compiles against and delegates to
the internal symbols — at minimum that `NewResolveActorOrAnonymous` produces middleware that
resolves the same entity id the passed `*AnonymousActor` holds. Keep it dependency-free (no
pool, no database): construct the `*AnonymousActor` through whatever seam task 002's injectable
design provides, or assert the delegation at the level the facade permits without a DB. If no
such seam exists, a compile-time assertion test (referencing every new exported symbol) plus a
manual note is acceptable — say so explicitly in the report rather than inventing a seam in
`internal/`.

### Must not change

- `authzSlugs` in `api/cmd/server/main.go` — `system_actor` must **not** be added.
- Any existing route entry's `scope:` or `middleware:` list, in the manifest or in `main.go`.
- `RequireAuth`, `RequireVerifiedEmail`, `RequireOIDCConfirmed`, and their existing facade
  wrappers.
- `api/internal/authz/authz.go`, mod-core's `opctx` or `grant_table.go`, `anon_tokens`,
  `user_accounts`.
- Nothing under `mfgen/` — no `scope: anonymous` literal, no compiler change. This plan ships
  entirely on the existing `scope: public` + named `middleware:` mechanism.

## Validation

- `cd api && go build ./...` and `go vet ./...` clean.
- `cd api && go test ./...` passes, including tasks 003 and 004's tests unmodified.
- `grep -n "NewAnonymousActor\|NewResolveActorOrAnonymous\|IsAnonymousActor\|AnonymousActor =" api/auth/auth.go`
  shows all five new exports present.
- Every symbol named by a `constructor:` key in `moduleforge.module.yaml` resolves in
  `api/auth/auth.go` — check `auth.NewAnonymousActor` and `auth.NewResolveActorOrAnonymous`
  specifically, per the `AGENTS.md` facade convention.
- The manifest parses: run whatever manifest validation the repo exposes (`make lint`, and
  `mfgen`'s validator if reachable from this checkout). If mfgen is not runnable here, verify by
  inspection that the two new entries match the field names and arg-kind prefixes used by the
  existing `verifier` service and `requireAuth` middleware entries, and say so in the report.
- `grep -n "system_actor" api/cmd/server/main.go` returns no match.
- `git diff api/cmd/server/main.go` shows only additions — the construction lines and the new
  demonstration route block — with no change to the existing `/v1` nesting or `/v1/auth` mount.
- The dev server starts against a migrated database (`make dev.start`) and fails to start with a
  clear log line against a database missing the seeded row.
- `make lint`, `make test.unit`, and `make build.api` pass.

## Metadata

architectural_impact: true

## Assumptions

- Tasks 002 and 004 have landed: `inner.NewAnonymousActor`, `inner.ResolveActorOrAnonymous`, and
  `inner.IsAnonymousActor` all exist.
- `mfgen` itself is not modified and is not required to run for this task to be validated; the
  manifest entries are verified against the existing entries' shapes and against the proposal's
  verified emitter analysis.
- The `pool` variable is already in scope at the point in `main.go` where `NewAnonymousActor` is
  called; if it is not, move the construction to after the pool is created rather than
  restructuring the file.

## Checkpoint hints

- After the `api/auth/auth.go` re-exports compile.
- After the two `moduleforge.module.yaml` entries are added.
- After the dev-server wiring builds and the server starts.

## References

- [Anonymous-actor architecture proposal](../notes/anonymous-actor-architecture-proposal.md) —
  §5 "Manifest wiring — the piece that avoids an mfgen change" (the exact YAML, the three
  cross-module facts, and the note that the dev server needs the equivalent hand-wiring), §4
  (the service entry), §6 (why `scope: public` and not a new literal). **Authoritative.**
- [mfgen expr/middleware pattern note](../notes/mfgen-expr-middleware-pattern.md) — prior
  verification that middleware var names are a camelCase pass-through and that multiple entries
  at one prefix do not bleed middleware.
- `AGENTS.md` — Conventions, the public-facade rule and the `self-route-manifest` regression it
  cites.
- `moduleforge.module.yaml` — the existing `verifier`/`claimMapper`/`userResolver` services and
  the three existing `provides.middleware` entries, for shape and comment style.
- `api/cmd/server/main.go` — `main.go:502` (`requireConfirmed`), `main.go:511-596` (the
  three-tier nesting that must not change), `main.go:522-533` (the not-regenerated-by-mfgen
  note), `main.go:285-293` (`authzSlugs`, which must not gain `system_actor`).

## Status

- **Outcome:** succeeded
- **Date:** 2026-08-04
- **Files:**
  - `api/auth/auth.go` — added the five new facade exports (`AnonymousActor` type alias,
    `NewAnonymousActor`, `NewResolveActorOrAnonymous`, `ResolveActorOrAnonymous`,
    `IsAnonymousActor`).
  - `api/auth/anonymous_actor_test.go` (new) — compile-time signature assertions for all five
    new exports, plus a dependency-free behavioral test of `ResolveActorOrAnonymous`'s anonymous
    fallback branch and `IsAnonymousActor`'s negative case.
  - `moduleforge.module.yaml` — added the `anonymousActor` `provides.services` entry and the
    `resolveActorOrAnonymous` `provides.middleware` entry, each with an explanatory comment
    matching the neighbouring entries' style, including the unconditional-reachability-root
    consequence.
  - `api/cmd/server/main.go` — added a `pubauth "github.com/moduleforge/mod-users/api/auth"`
    import, the `anonActor`/`resolveActorOrAnonymous` construction, and a dev-server-only
    `/v1/anonymous-demo` demonstration route. No existing line was modified (see decision on the
    facade import alias below).
- **Decision — facade import alias in `main.go`:** `main.go` already imports
  `api/internal/auth` under the default alias `auth` (both packages are named `auth`, so they
  cannot share that alias). `NewResolveActorOrAnonymous` exists only on the facade (by design —
  it is the holder-unwrapping adapter; `inner.ResolveActorOrAnonymous` keeps its plain `int64`
  signature), so the dev server needs the facade specifically for that constructor. Resolved by
  importing the facade under a second alias, `pubauth`, and using `pubauth.NewAnonymousActor` /
  `pubauth.NewResolveActorOrAnonymous` / `pubauth.IsAnonymousActor` for the three symbols this
  task's requirement 3 code sample designated `auth.*`, while leaving every pre-existing `auth.*`
  reference (internal package) untouched. This keeps `git diff main.go` purely additive, per the
  task's own validation check, and both packages' `Verifier`/`ClaimMapper`/`UserResolver`/
  `AnonymousActor` types are identical under Go's type-alias equivalence, so no conversion is
  needed at the call sites.
- **Decision — construction placement in `main.go`:** `anonActor` is constructed right after
  `localMapper` (needs only `ctx`+`pool`, both already in scope); `resolveActorOrAnonymous` is
  constructed right after `resolver` (the last of its four dependencies to become available). The
  demonstration route is placed as a sibling of the existing `/v1/auth` `r.Route` block, after
  `requireConfirmed` is built and before the existing `/v1` block, so it doesn't touch either.
- **Decision — facade test scope:** per Requirement 4's explicit escape hatch, no dependency-free
  seam exists for constructing a `*AnonymousActor` with a chosen entity id (its only field is
  unexported; its only constructor needs a live `*pgxpool.Pool`). The test file instead pins all
  five exports' exact signatures at compile time and behaviorally exercises the plain
  `ResolveActorOrAnonymous` re-export end-to-end (verifier/mapper/resolver deliberately `nil`,
  since `AuthenticateRequest` short-circuits on an empty header before touching any of them).
  `NewResolveActorOrAnonymous`'s own body is a one-line pass-through
  (`inner.ResolveActorOrAnonymous(verifier, mapper, resolver, anon.EntityID())`), so this test
  plus task 004's exhaustive suite in `api/internal/auth/anonymous_actor_test.go` covers the
  delegation it adds; no seam was added to `internal/`.
- **Validation summary:**
  - PASS — `cd api && go build ./...` clean.
  - PASS — `cd api && go vet ./...` clean.
  - PASS — `cd api && go test ./...` — all packages pass, including `internal/auth` (task 004)
    and `internal/handlers/auth` (task 003) unmodified.
  - PASS — `grep -n "NewAnonymousActor\|NewResolveActorOrAnonymous\|IsAnonymousActor\|AnonymousActor =" api/auth/auth.go`
    shows all five new exports.
  - PASS — the manifest's `anonymousActor`/`resolveActorOrAnonymous` entries resolve correctly:
    `mfgen` turned out to be reachable in this environment (a sibling checkout at
    `../mfgen/bin/mfgen`, built). Ran `mfgen generate` against a scratch copy of
    `app-mftodo/moduleforge.app.yaml` with the `mod-users` module's `localPath` pointed at this
    worktree; manifest loading, validation, and dependency-graph resolution all succeeded and the
    generated `main.go` contained exactly
    `anonymousActor, err := auth.NewAnonymousActor(ctx, pool)` and
    `resolveActorOrAnonymous := auth.NewResolveActorOrAnonymous(verifier, claimMapper, userResolver, anonymousActor)`
    — matching this task's Requirement 3 sample verbatim. (The subsequent `go mod tidy` step
    failed on an unrelated missing app-local package, `mftodo/cmd/server/appsetup`, an artifact
    of running the generator outside its own app checkout — not a manifest defect.) The scratch
    manifest copy and generated output were both discarded; no file outside this worktree was
    left modified.
  - PASS — `grep -n "system_actor" api/cmd/server/main.go` — no match.
  - PASS — `git diff api/cmd/server/main.go` — 40 insertions, 0 deletions; the existing `/v1`
    nesting and `/v1/auth` mount are byte-for-byte unchanged.
  - PASS (substitute method) — dev-server boot behavior. `make dev.start` itself cannot run from
    inside this task worktree: its Docker build `context: ../../..` (relative to
    `deploy/local/docker-compose.yml`) assumes the checkout sits directly at
    `.../moduleforge/mod-users/`, but this worktree sits three levels deeper
    (`mod-users/worktrees/plan/<task>/`), so the context path resolves to a nonexistent
    directory — a pre-existing worktree/docker-compose layout mismatch, unrelated to this task's
    changes, and out of this task's scope to fix. Instead validated the two required boot
    behaviors directly against the built `api/bin/server` binary and a throwaway Postgres
    container (migrated with mod-core, mod-authz, mod-audit, and mod-users' own goose
    migrations): (1) with mod-users migrated only through `0100_schema.sql` (0101 pending), the
    server logged `"anonymous actor init failed"` with the underlying `system_actors` error and
    exited 1, immediately, before touching OAuth/onboarding; (2) with `0101_system_actors.sql`
    also applied, the server booted cleanly (`"users-api starting"`), `/healthz` returned 200, and
    `/v1/anonymous-demo/` returned the same `503 oidc_not_confirmed` response every other `/v1/*`
    route returns pre-confirmation — confirming `resolveActorOrAnonymous` is correctly gated
    behind `requireConfirmed`, matching the manifest's ordering requirement. The throwaway
    container, binary process, and generated TLS cert were all torn down afterward; the worktree
    is clean (`git status` — nothing to commit).
  - PASS — `make lint` (with `SHADOW_DB_PREREQ_DIRS=<mod-core>/model/migrations`, per task 002's
    documented precedent): `lint.model`, `lint.api` (`go vet` + `check-server-error-literals`),
    and `lint.gui` all passed clean.
  - PASS — `make test.unit`: `api` (`go test ./...`, all packages ok) and `gui` (9/9) passed; no
    flake reproduced this run (the pre-existing, already-tracked
    `TestNewStepUpConsumedCache_JanitorStopsOnCancel` flake — followup 5RbD — did not trigger).
  - PASS — `make build.api` clean.
