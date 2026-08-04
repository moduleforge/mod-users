# Consumer-side context: mod-repos' anonymous-actor blocker on mod-users

This file consolidates everything already known from `moduleforge/mod-repos` about why
mod-repos followup **Jhkw** blocks on `mod-users`. Gathered directly by the Flow manager
by reading mod-repos' spec/architecture docs and recovering a deleted planning note from
git history, so downstream research agents don't need to re-derive this — only to
investigate the mod-users side and propose a fix.

## The blocking followup (mod-repos, id Jhkw, phase namespace-repository-api)

> The real tokenless-HTTP entry point (middleware injecting a low-privilege actor for a
> request with no Authorization header) does not exist in mod-users today. The
> anonymous-actor test proves the visibility logic is correct once some actor identity
> has reached it, via a test-only fake actor — not that UC5's genuinely tokenless HTTP
> path works end-to-end.
>
> Second, independent co-blocker, on mod-repos' own side (not in scope for this
> mod-users work, noted for completeness): `moduleforge.module.yaml` registers
> `/v1/namespaces` and `/v1/repositories` with `scope: authenticated`, which wraps the
> route group in `requireAuth` middleware — rejecting any request lacking a valid
> bearer token before it ever reaches a handler. Even once mod-users ships an
> anonymous-actor capability, mod-repos will separately need to change its own route
> scope to actually use it. Flagged here for the architect's awareness only.

## mod-repos-spec.md — UC5 and the external-dependencies table

> **UC5: Anonymous read of a public repository over HTTP**
> Actor: an unauthenticated (or low-privilege anonymous) HTTP caller.
> Action: requests repository metadata or git objects for a repository that is public
> and whose owning namespace is also public.
> Outcome: the read succeeds without an explicit per-actor grant. ... Depends on the
> unresolved anonymous-identity precondition tracked in External dependencies (blocking).

> **Precondition, not a guarantee mod-repos provides:** an actor-less HTTP request must
> still resolve to *some* low-privilege actor identity before it reaches mod-repos'
> authorization checks. mod-repos depends on this identity being available; it does not
> implement it.

External dependencies (blocking) table, relevant row:

| Dependency | Owner | Status | Blocks |
|---|---|---|---|
| An anonymous/low-privilege actor identity that survives the platform's actor check | `mod-users` | Partially exists but not yet in a usable shape. | Anonymous HTTP read of public repositories, per the visibility model. |

## docs/architecture/authorization-and-visibility.md — "the genuine blocker"

> **The genuine blocker mod-repos cannot solve itself.** The platform's `Authorize`
> returns `ErrUnauthenticated` *before any policy runs at all* whenever the operation
> context carries no actor. An actor-less request never reaches the visibility arm
> above, no matter how it is written — the check that would grant public read never
> executes, because the function returns before it. mod-users' existing anonymous-token
> mechanism is adjacent to what is needed here but not yet in a usable shape for this
> purpose. **mod-repos must treat "an anonymous HTTP caller arrives with some
> low-privilege actor identity already on the operation context" as a precondition it
> depends on, not a capability it implements or can work around locally.**

Also documents the platform's two-independent-code-paths duality (single-row `Authorize`
in Go vs. list-scoping via generated SQL `accessible_<slug>_ids_for_actor` functions) —
relevant because whatever anonymous-actor mechanism mod-users builds has to produce an
actor identity that both paths agree on, not just satisfy the single-row check.

## Recovered planning note: `plan/notes/anonymous-actor-precondition.md`

This file existed during mod-repos' implementation planning (phase 3) and was deleted
as a planning-artifact cleanup once the phase merged. Recovered from git history
(commit `a8c84c4` added it; commit `02c1921` removed it during artifact cleanup).
Full original text, verified directly against mod-users source at the time:

---

# Anonymous HTTP read precondition — verified against mod-users source

## Purpose and scope

Confirms, by reading `mod-users` directly, that UC5 (anonymous HTTP read of a public
repository) is genuinely blocked in the way the spec/topic docs describe, and
characterizes precisely what exists today so Phase 3's task documents can stub or defer
this accurately rather than guessing at the shape of the gap.

## What exists today

`mod-users/api/internal/handlers/auth/anonymous.go` implements `POST /v1/auth/anonymous`:
given a `device_id`, it calls `CreateAnonymousUser`, which creates a real `UserAccount`
entity, then issues a JWT (`IssueAnonymousJWT`) carrying `is_anonymous=true`. This is a
**full anonymous session-creation flow** — the caller makes one authenticated-after-the-
fact request to obtain a token, then uses that token like any other bearer token on
subsequent requests.

This is not the same shape as what UC5 needs. UC5's requirement is a **truly tokenless**
HTTP request — no prior round-trip, no `device_id`, no JWT — that still resolves to
*some* low-privilege actor identity by the time mod-repos' service method calls
`Authorize`. The existing `anon_tokens` mechanism requires an explicit client-side
bootstrap step; it does not inject an actor onto `opctx` for a request that never
authenticated at all.

`mod-users/api/internal/authz/authz.go`'s `Authorize` returns `ErrUnauthenticated`
(aliased from `apiresp.ErrUnauthenticated`) before any policy runs whenever
`opctx.ActorEntityID`/`SudoActorEntityID` resolves to nothing — confirmed directly in
the `effectiveActor` early-return path (`authz.go:108-113`). A request that never went
through `POST /v1/auth/anonymous` (or any other auth flow) carries no actor at all, so
it never reaches the visibility-check arm mod-repos' decorating `Authorizer` would add,
no matter how that arm is written.

## Conclusion for planning

This confirms the spec's characterization is accurate, not stale: the `anon_tokens`
mechanism is "adjacent but not in a usable shape" exactly as described, because it's a
session-bootstrap endpoint, not a request-context-level anonymous-actor default. There
is no middleware anywhere in `mod-users` today that would inject an actor onto a
genuinely tokenless request.

**Recommendation carried into the plan:** ... the literal "no token at all" HTTP entry
point for UC5 should be built against a test-only fake actor ... and the task document
should flag the true tokenless-request wiring (whatever middleware layer would need to
inject a low-privilege actor onto `opctx` for a request carrying no `Authorization`
header at all) as an explicit open follow-up pending a `mod-users` release, consistent
with the blocking-dependency table in the spec.

---

## Platform manifest scope vocabulary (docs/mf-standards/manifest-spec.md, §2 provides.routes[])

> `scope` (string, optional): Authentication scope gate applied before this route group.
> Values: `public` (no auth required), `authenticated` (valid bearer token required),
> `verified` (authenticated + email verified). Default: `authenticated`.

Important nuance for the architect: `scope: public` in the platform's generated router
only means the compiler **skips wrapping the route group in `requireAuth` /
`requireVerifiedEmail` middleware**. Nothing in the manifest spec or the compiler's
routing-scope-matching rules (§6) currently causes a low-privilege actor to be *injected*
onto the operation context for a `public`-scoped route — this matches the planning
note's finding above ("no middleware anywhere in mod-users today that would inject an
actor"). So today, a `public`-scoped mod-repos route would reach the handler without an
`ErrUnauthenticated` 401 at the middleware layer, but the handler's own call into
`Authorize` would *still* fail with `ErrUnauthenticated`, because `opctx` carries no
actor at all. Closing this gap is squarely mod-users' (and/or the platform manifest
compiler's) responsibility, not mod-repos'.

Note also: the `scope:`/`middleware:` vocabulary itself lives in the platform manifest
spec (`docs/mf-standards/manifest-spec.md`), which is vendored into every module from a
canonical source (project `docs-mf-standards`). If the eventual fix requires a new scope
value (e.g. `anonymous`) rather than reusing `public` + new middleware, that may be a
cross-cutting manifest-spec change beyond mod-users' own repo — the architect should
flag this explicitly rather than silently assume mod-users alone can ship it.

## mod-repos' own current wiring (for context only — NOT in scope for the mod-users fix)

`moduleforge.module.yaml` (mod-repos root):
```yaml
    - prefix: /v1/namespaces
      register: reposhttpapi.RegisterNamespaceRoutes
      register_args:
        - service:reposDeps
      scope: authenticated

    - prefix: /v1/repositories
      register: reposhttpapi.RegisterRepositoryRoutes
      register_args:
        - service:reposDeps
      scope: authenticated
```
Both are `scope: authenticated` today. Making UC5 actually work end-to-end will
eventually require mod-repos to change these to `public` (or whatever scope the
mod-users fix introduces) — but that change, and re-verifying the visibility-arm SQL/Go
policy pairing, is separate follow-up work in mod-repos itself, not part of this task.

## What this task needs from the architecture proposal

A concrete, implementable design — inside `mod-users` — for: given an HTTP request that
carries **no** `Authorization` header at all, hitting a route mounted with a
public/anonymous scope, produce a real low-privilege actor identity on the operation
context by the time the handler's `Authorize` call runs, such that:
- both the single-row `Authorize` path and the list-scoping SQL access-function path
  agree on the same actor semantics (per the platform's authz duality),
- it composes cleanly with the existing `anon_tokens`/`POST /v1/auth/anonymous`
  session-bootstrap mechanism rather than duplicating or conflicting with it,
- it fits the existing manifest `scope:` vocabulary and multi-module composition model
  (or explicitly proposes and justifies extending that vocabulary),
- it does not weaken `authenticated`/`verified` routes elsewhere in the ecosystem.
