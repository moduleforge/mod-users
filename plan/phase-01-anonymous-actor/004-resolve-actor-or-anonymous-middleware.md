# Resolveactororanonymous Middleware

## Purpose and scope

Add `ResolveActorOrAnonymous`, the opt-in chi middleware that behaves exactly like
`RequireAuth` except that a request carrying **no** `Authorization` header falls through with
`opctx.WithActor(ctx, anonActorEntityID)` instead of being 401'd. Also add
`IsAnonymousActor(ctx) bool` as a distinct context accessor.

This is the most security-sensitive task in the plan. A presented-but-bad token must **never**
silently downgrade to anonymous — that is the classic confused-deputy pattern, and the proposal
calls it out as the single most likely implementation mistake.

Invoke the standard `implement-task` skill with the Go developer role and the Go design
standards as the technology layer.

Scope: a new `api/internal/auth/anonymous_actor.go` (the filename the proposal names), a new
`api/internal/auth/anonymous_actor_test.go`, and a minimal, additive change to the context-key
declaration in `api/internal/auth/principal.go`. The public facade re-export, the manifest
entry, and the dev-server wiring are task 005's work.

## Requirements

### 1. The middleware

In `api/internal/auth/anonymous_actor.go`:

```go
func ResolveActorOrAnonymous(
    verifier *Verifier, mapper ClaimMapper, resolver *UserResolver,
    anonActorEntityID int64,
) func(http.Handler) http.Handler
```

This is the exact signature the proposal specifies in its §3. It takes a bare `int64`, not the
`*AnonymousActor` holder — the adapter that unwraps the holder for manifest wiring lives in the
public facade and is task 005's responsibility.

Behavior, branching on `AuthenticateRequest`'s already-classified sentinel errors. Reuse the two
unexported helpers task 003 extracted from `RequireAuth` for every row except the diverging one,
so the two middlewares cannot drift:

| `AuthenticateRequest` result | Behavior |
|---|---|
| success | identical to `RequireAuth`: `WithUserContext` + `opctx.WithActor` (+ `opctx.WithSudoActor` when assuming), then `next` |
| `ErrNoAuthHeader`, **and** the request genuinely has no `Authorization` header | `opctx.WithActor(ctx, anonActorEntityID)` + the anonymous marker, then `next` |
| `ErrNoAuthHeader`, but an `Authorization` header **is** present (malformed, non-`Bearer`) | 401, via the shared error helper — unchanged from `RequireAuth` |
| `ErrInvalidToken` | 401, unchanged |
| `ErrUserGone` | 401, unchanged |
| claim-map / resolver fault | 500, unchanged |

The malformed-header row deserves care. `AuthenticateRequest` returns `ErrNoAuthHeader` for
both "no header" and "header present but not `Bearer ` form"
(`api/internal/auth/middleware.go:40-46`). Only the **genuinely missing** header may fall
through. Distinguish them the same way `RequireAuth` already does, with
`r.Header.Get("Authorization") == ""`. A present-but-malformed header is a caller error, not an
absent credential; it must still 401.

Invariants the middleware must hold — each is separately tested below:

- It **never** calls `opctx.WithSudoActor` on the anonymous branch. Identity assumption requires
  an authenticated admin, full stop.
- It **never** calls `WithUserContext` on the anonymous branch. This makes accidental
  composition with `RequireVerifiedEmail` fail closed: that middleware 500s with a "server
  misconfiguration" log when `UserContext` is absent
  (`api/internal/auth/require_verified.go:20-27`), so a route group that wrongly composes both
  breaks visibly in the first integration test rather than silently.
- It sets the anonymous marker only on the anonymous branch, never on the authenticated branch.

Write a thorough doc comment on the exported function. It must state, as hard preconditions
carried from the proposal's Security considerations:

- **Per-IP rate limiting is required.** Every anonymous request shares one actor id, so
  actor-based throttling is meaningless. Any route opting into this middleware must sit behind
  a per-IP/CIDR limiter at the edge or in the composition root. mod-users documents this
  precondition and does not ship the limiter.
- **Never compose with `requireAuth` or `requireVerifiedEmail`** on the same route group. The
  former is contradictory; the latter fails closed with a 500.
- **Keep `requireOIDCConfirmed` outermost**, matching every existing route entry in mod-users'
  manifest. It can 503 before actor injection ever runs; that is acceptable, since an
  unconfirmed instance should serve no traffic.
- **HTTP caching**: responses from an anonymous-enabled route group differ between anonymous
  and authenticated callers. Any cache or CDN in front of such a route must key on the
  `Authorization` header — consuming modules emit `Vary: Authorization` from their handlers;
  this middleware does not set it.
- **Audit attribution collapses** for the shared actor: `audit_log.actor_entity_id` would be
  identical for every anonymous caller. Attribution for anonymous traffic is `opctx.RequestID`
  plus HTTP access logs, not `audit_log`. No module may authorize a mutating operation for the
  anonymous actor.

### 2. `IsAnonymousActor` context accessor

A distinct context key and accessor:

```go
// WithAnonymousActor marks ctx as carrying the shared anonymous system actor.
func WithAnonymousActor(ctx context.Context) context.Context

// IsAnonymousActor reports whether ctx was populated by ResolveActorOrAnonymous'
// anonymous branch — i.e. the actor on ctx is the shared, zero-authority
// anonymous system actor rather than an authenticated principal.
func IsAnonymousActor(ctx context.Context) bool
```

**Context-key hazard.** `api/internal/auth/principal.go:40-42` currently declares
`type contextKey int` with `const userContextKey contextKey = iota`, which evaluates to `0`. A
new key declared carelessly in a separate `const` block will also be `0` and will **collide**
with `userContextKey`. Convert that declaration into a single grouped `const (...)` block with
`iota` so each key gets a distinct value, or give the new key its own distinct unexported type.
Whichever you choose, add a test that asserts the two keys do not alias (e.g. a context carrying
only the anonymous marker yields `FromContext(ctx) == (nil, false)`).

This accessor is deliberately distinct from both:
- `UserAccount.IsAnonymous` (`api/internal/service/user_accounts.go:670`), which is derived from
  `!ua.Email.Valid` and describes a *guest account*; and
- the `is_anonymous` JWT claim (`api/internal/auth/local_jwt.go`), which is written but never
  read anywhere.

Do not reuse or repurpose either. The doc comment must say so explicitly — the new mechanism has
no JWT at all, so a JWT claim is the wrong carrier by definition.

### 3. Tests

`api/internal/auth/anonymous_actor_test.go`, built the same way as task 003's
characterization tests (local-only `Verifier`, generic `ClaimMapper`, nil-pool `UserResolver`
with an injectable `uuidLookup` stub, `captureHandler`, `httptest`). Table-driven where it fits.

This task owns the middleware half of the test list the proposal enumerates in its
"Migration / rollout notes — Phase 1" step 5:

1. **No `Authorization` header → anonymous actor on opctx.** `next` is called;
   `opctx.ActorEntityID(ctx)` equals the configured `anonActorEntityID`;
   `IsAnonymousActor(ctx)` is `true`.
2. **Invalid token → 401.** A Bearer token with a bad signature yields 401 with
   `invalid or expired token`; `next` is not called; no actor is set.
3. **Expired token → 401.** A well-signed token whose `exp` is in the past yields the same 401.
   Assert this separately from case 2 — expiry is the realistic silent-downgrade scenario.
4. **Valid token → normal actor, not the shared one.** `opctx.ActorEntityID(ctx)` equals the
   resolved `uc.EntityID` and is **not** `anonActorEntityID`; `FromContext(ctx)` yields the
   `*UserContext`; `IsAnonymousActor(ctx)` is `false`. Include a case whose token is a
   *guest-account* JWT (carrying `is_anonymous: true`) to pin the proposal's point that a caller
   holding a guest token takes the normal authenticated branch and gets their own guest actor,
   not the shared one.
5. **Sudo never set on the anonymous branch.** `opctx.SudoActorEntityID(ctx)` is unset when the
   header is absent. Also assert the authenticated-with-`AssumedUser` case still sets it, so the
   invariant is "never on the anonymous branch", not "never".
6. **`UserContext` never set on the anonymous branch.** `FromContext(ctx)` returns
   `(nil, false)`. Add a composition test: `ResolveActorOrAnonymous` followed by
   `RequireVerifiedEmail`, with no `Authorization` header, produces the 500 "server
   misconfiguration" response — the documented fail-closed behavior.
7. **Malformed header still 401.** `Authorization: Basic abc` and `Authorization: Bearer` (no
   token) each yield 401, not the anonymous fall-through.
8. **`ErrUserGone` still 401**, and a claim-map / resolver fault still 500 — the shared helper
   is genuinely shared.
9. **No JWT resolves to the anonymous actor.** A locally-issued JWT whose subject UUID has no
   `user_accounts` row resolves to `ErrUserGone` → 401; it never lands on
   `anonActorEntityID`. (The structural, database-level half of this assertion — that a
   `user_accounts` row for the system actor is impossible — is task 006's.)

Also assert `RequireAuth` is untouched: task 003's `middleware_test.go` must still pass
unmodified.

### Must not change

- `RequireAuth`'s behavior, `AuthenticateRequest`, `RequireVerifiedEmail`,
  `RequireOIDCConfirmed`.
- `api/internal/authz/authz.go`, mod-core's `opctx` or `grant_table.go`, `anon_tokens`,
  `user_accounts`.
- The `POST /v1/auth/anonymous` guest-account flow — no convergence, no reuse, no shared code
  path. Only terminology in comments is disambiguated.
- Any existing route's `scope:`; no manifest or `main.go` edits in this task.
- The inert `is_anonymous` JWT claim: do not wire it through, do not read it, do not repurpose
  it.

## Validation

- `api/internal/auth/anonymous_actor.go` and `anonymous_actor_test.go` exist. The only other
  production file touched is `principal.go`, and only its context-key declaration.
- `cd api && go build ./...`, `go vet ./...` clean.
- `cd api && go test ./internal/auth/...` passes, including task 003's `middleware_test.go`
  unmodified.
- Every one of the nine numbered test cases above exists as a named test (or a named row in a
  table-driven test) — enumerate them in the task report by test name.
- `grep -n "WithSudoActor\|WithUserContext" api/internal/auth/anonymous_actor.go` shows both
  appear only inside the shared authenticated-success helper call path, never on the anonymous
  branch.
- `grep -n "is_anonymous\|IsAnonymous\b" api/internal/auth/anonymous_actor.go` shows no use of
  the JWT claim or the derived guest-account boolean.
- The context-key non-aliasing test passes.
- `make lint` and `make test.unit` pass.

## Metadata

architectural_impact: true

## Assumptions

- Task 003 has landed: the shared error-mapping and success-population helpers exist in
  `api/internal/auth/middleware.go`, and `middleware_test.go` pins `RequireAuth`'s behavior.
- This task is independent of tasks 001 and 002 (it takes a bare `int64`) and may run
  concurrently with 002.
- `server.Error` keeps its current response shape.

## Checkpoint hints

- After the context accessor and its non-aliasing test land.
- After the middleware's happy path plus the no-header anonymous fall-through are green.
- After the full 401/500 matrix (cases 2, 3, 7, 8) is green.

## References

- [Anonymous-actor architecture proposal](../notes/anonymous-actor-architecture-proposal.md) —
  §3 "The middleware: ResolveActorOrAnonymous" (the branch table and the three invariants),
  §"Security considerations" (all eight items; 1, 3, 4, 5, 6, 7 are directly relevant here),
  §"Composition with POST /v1/auth/anonymous" (guest-account vs. anonymous-actor terminology and
  the guest-token test case), §"Migration / rollout notes — Phase 1" step 5 (the test list).
  **Authoritative; do not re-derive.**
- [Current-state audit](../notes/current-state-audit.md) — §1b (the single opctx producer), §2b
  (`RequireVerifiedEmail`'s missing-`UserContext` 500), §3e (the inert `is_anonymous` claim).
- `api/internal/auth/middleware.go` — `AuthenticateRequest`'s sentinel-error contract and its doc
  comment, which anticipated exactly this extension point.
- `api/internal/auth/principal.go` — the `contextKey` declaration to extend carefully.
- `api/internal/auth/require_verified.go` — the fail-closed 500 the composition test asserts.

## Status

- **Outcome:** succeeded. 2026-08-04.
- **Files:** created `api/internal/auth/anonymous_actor.go` (`ResolveActorOrAnonymous`,
  `WithAnonymousActor`, `IsAnonymousActor`) and `api/internal/auth/anonymous_actor_test.go`;
  modified `api/internal/auth/principal.go` — context-key declaration only, converted to one
  grouped `const (...)` block so `userContextKey` (0) and the new `anonymousActorKey` (1) cannot
  alias. No other production file touched; `middleware_test.go` is unmodified and still passes.
- **Reuse of task 003's helpers:** every non-diverging row goes through `writeAuthError` and
  `contextWithAuthenticatedActor`, so the middleware body contains exactly one branch that
  `RequireAuth` does not have — the genuinely-absent-header fall-through, gated on
  `errors.Is(err, ErrNoAuthHeader) && r.Header.Get("Authorization") == ""`.
- **Test-name → required case mapping** (all in `anonymous_actor_test.go`):
  1. `TestResolveActorOrAnonymous_NoAuthHeader_SetsAnonymousActor`
  2. `TestResolveActorOrAnonymous_PresentedCredentialFailuresAreRejected/invalid token: bad signature`
  3. `TestResolveActorOrAnonymous_PresentedCredentialFailuresAreRejected/expired token`
  4. `TestResolveActorOrAnonymous_ValidToken_UsesResolvedActor/ordinary local JWT` and
     `.../guest-account JWT carrying the inert anonymity claim` (the guest token is minted by the
     real `IssueAnonymousJWT`, so it genuinely carries the inert claim)
  5. `TestResolveActorOrAnonymous_SudoActorNeverSetOnAnonymousBranch` plus
     `TestResolveActorOrAnonymous_SudoActorStillSetForAssumedUser` (invariant is "never on the
     anonymous branch", not "never")
  6. `TestResolveActorOrAnonymous_NoUserContextOnAnonymousBranch` plus
     `TestResolveActorOrAnonymous_ComposedWithRequireVerifiedEmailFailsClosed` (500 "server
     misconfiguration")
  7. `TestResolveActorOrAnonymous_PresentedCredentialFailuresAreRejected/malformed header: Basic scheme`
     and `.../malformed header: bare Bearer with no token`
  8. `TestResolveActorOrAnonymous_PresentedCredentialFailuresAreRejected/resolver reports ErrUserGone`,
     `.../claim mapper fault: missing sub claim`, `.../resolver internal fault`
  9. `TestResolveActorOrAnonymous_NoJWTResolvesToAnonymousActor`
  Context-key hazard: `TestAnonymousActorKeyDoesNotAliasUserContextKey` (plus
  `TestIsAnonymousActor_FalseOnUntouchedContext`).
- **Mutation-checked, then reverted:** deleting the `r.Header.Get("Authorization") == ""` guard
  fails exactly the two malformed-header rows; collapsing the two context keys back onto separate
  `iota` blocks fails the non-aliasing test. Both hazard tests are therefore load-bearing rather
  than tautological.
- **Validation:** `cd api && go build ./...`, `go vet ./...`, `go test ./internal/auth/...`, and
  `go test -race -count=1 ./internal/auth/...` all pass; `make lint.api`, `make lint.gui`, and the
  full `make test.unit` (model + api + gui) pass. `make lint` as a whole fails only in
  `lint.model`'s `shadow-db-lint`, which cannot apply `0100_schema.sql` because mod-core's
  `legal_entities` is absent from the ephemeral shadow DB — reproduced identically at this
  branch's base commit (`a2432dc`), and no `model/` file is touched by this task.
  `grep -n "WithSudoActor\|WithUserContext" api/internal/auth/anonymous_actor.go` matches only the
  two doc-comment lines describing the *success* row (the shared helper's behavior); neither
  identifier appears in executable code in this file, so neither can appear on the anonymous
  branch. `grep -n "is_anonymous\|IsAnonymous\b" api/internal/auth/anonymous_actor.go` returns no
  matches: the doc comment disambiguates against the guest-account boolean and the inert JWT claim
  by naming `toUserAccount` and `IssueAnonymousJWT` rather than the literal identifiers, which
  keeps the grep gate meaningful.
- **Note (unchanged behavior, worth knowing):** a request sending `Authorization:` with an *empty*
  value is indistinguishable from an absent header via `r.Header.Get`, so it takes the anonymous
  branch. That matches `RequireAuth`'s own classification (it reports "missing Authorization
  header" for the same input), so the two middlewares stay consistent; no code changed for it.
