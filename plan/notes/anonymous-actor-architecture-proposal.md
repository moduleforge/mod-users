# Anonymous actor architecture proposal (mod-users)

Author: architecture review, 2026-08-04
Status: proposal — no code written, no repo files changed
Consumer driver: mod-repos followup `Jhkw` (UC5, anonymous read of a public repository)

---

## Summary

Add one seeded, well-known, **zero-authority** entity — the *anonymous system actor* — and
one new opt-in chi middleware, `ResolveActorOrAnonymous`, that behaves exactly like
`RequireAuth` except that a request carrying **no** `Authorization` header falls through
with `opctx.WithActor(ctx, anonymousActorEntityID)` instead of being 401'd. Everything
else about `RequireAuth`'s behavior is preserved, including rejecting invalid/expired
tokens (no silent downgrade to anonymous).

The anonymous actor is a real row in `entities` — so both halves of the platform's authz
duality (`Authorize`'s `checkGrantOrOwn` in Go and the generated
`accessible_<slug>_ids_for_actor(p_actor_entity_id BIGINT, p_op_ids INT[])` SQL) take it
unmodified — but it is deliberately given **no grants and no ownership, ever**, enforced at
the data layer. Its total platform authority is provably the empty set. All anonymous
access is therefore expressed where it belongs: as module-level *visibility* policy in the
consuming module's decorating `Authorizer` and its list-scoping query, not as a grant to a
pseudo-principal.

**No mfgen change is required.** mfgen's generic `provides.middleware` / route
`middleware:` mechanism already composes exactly the wiring needed under the existing
`scope: public`. A future `scope: anonymous` literal is worth proposing to mfgen as
ergonomic sugar, but nothing blocks on it — mod-users can ship 100% of this capability
today.

Sizing: one mod-users migration, one middleware + one startup-resolved service, one facade
re-export, one manifest entry, tests, doc corrections. A single-phase feature, not a
platform change.

---

## Recommended approach

### The decision matrix, resolved

| Question | Recommendation |
|---|---|
| Actor identity backing | **Persisted, well-known singleton entity row** of a new `system_actor` type |
| Scope vocabulary | **Ride on existing `scope: public` + `middleware:`** — no mfgen change |
| Relationship to `POST /v1/auth/anonymous` | **Stay separate**; neither deprecates the other. Converge terminology only |
| Grants to the anonymous actor | **None, ever** — the actor is a bare identity, not a permission holder |

### Why a persisted row and not an ephemeral sentinel id

The ephemeral option (e.g. a constant `AnonymousActorEntityID = -1`, never present in
`entities`) is genuinely attractive on one axis: the design-space survey's flagged
ownership risk becomes *structurally impossible*, because
`entities.owner_id BIGINT REFERENCES entities(id)`
(`mod-core/model/migrations/0013_entity_ownership.sql:7`) would reject `owner_id = -1`
with a foreign-key violation. That is a stronger guarantee than any trigger I can write.

It loses on a decisive counter-fact, found by direct inspection:

```sql
-- mod-audit/model/migrations/0400_audit_log.sql:5
actor_entity_id BIGINT NOT NULL REFERENCES entities(id) ON DELETE RESTRICT,
```

The audit observer (`auditservice.New(...)`, wired into the `ObserverGroup` at
`mod-users/api/cmd/server/main.go:307-314`) writes one `audit_log` row **inside the
operation's transaction**. Under a sentinel actor, the first audited event ever produced
on an anonymous request path aborts the entire transaction with an opaque FK error at
runtime, in production, in a module that never knew the actor was synthetic. The failure
mode is a 500 inside someone else's write path, not a clean denial. And the same latent
break recurs for every future `REFERENCES entities(id)` column keyed on "who did this."

A real row makes every such FK resolve, makes both authz code paths work with literally
zero special-casing, and keeps the ownership risk manageable with a cheap, explicit,
data-layer guard (below). That trade is the right one.

### Why a *shared* fixed actor and not a per-request persisted one

Reusing `CreateAnonymousUser` (`api/internal/service/user_accounts.go:268`) implicitly per
tokenless request would mean: a DB transaction with three inserts (`natural_person`,
`user_accounts`, `anon_tokens`) on **every** first-touch of an uncredentialed caller.
That is a write amplification DoS handed to any crawler, unbounded row growth in
`entities`, and — because `natural_person` self-owns via `entities_owner_default_self`
(`0013_entity_ownership.sql:16-26`) — one immortal self-owning entity per crawler request.
Rejected.

### Why zero grants

mod-repos does not need the anonymous actor to *hold* any permission. UC5's requirement is
that `Authorize` gets past its `effectiveActor` early-return (`authz.go:110-113`) so that
mod-repos' decorating `Authorizer`'s visibility arm can run. Granting the shared actor
anything would make that grant apply simultaneously to every tokenless request in the
system with no isolation between callers — precisely the second-order risk the
design-space survey flags. Keeping the actor at zero authority means:

- Both authz paths agree trivially and by construction (nothing to disagree about).
- No operational discipline is required around a "don't grant to anonymous" rule beyond a
  boot-time assertion.
- "What can an anonymous caller do?" is answerable by reading each module's visibility
  policy, not by auditing the grants table.

---

## Mechanism detail

### 1. The anonymous system actor entity

A new concrete entity type `system_actor`, seeded by mod-users in its own migration range
(100–199, `after: [core]`), plus a `system_actors` class-table-inheritance child table and
exactly one row: slug `anonymous`.

Type choice rationale — `system_actor` is deliberately **not** `natural_person`,
`corporation`, or `service_account`:

- **It never self-owns.** `entities_owner_default_self`
  (`0013_entity_ownership.sql:18-23`) fires only for types descending from
  `natural_person` or `service_account`. A `system_actor` keeps `owner_id NULL`, so
  `checkGrantOrOwn`'s own-arm (`e.owner_id = $1`, `authz.go:318-322`) can never match it
  as owner, and — because nothing owns *it* — no actor can reach it through the own-arm
  either. If it were a `service_account`, it would self-own, meaning an anonymous caller
  could authorize `update`/`delete` against the shared anonymous actor itself (the own-arm
  is not gated on operation) — a self-DoS. That is the concrete reason not to reuse
  `service_account`.
- **It is invisible to every list path.** `system_actor` is *not* added to `authzSlugs`
  (`api/cmd/server/main.go:285-293`), so no `accessible_system_actor_ids_for_actor`
  function is ever generated. It cannot appear in any list result of any type.
- **It cannot join an actor group.** `authz_actor_group_members`' type-check trigger
  (`mod-authz/model/migrations/0502_authz_actor_group_members.sql:103-120`) admits only
  `legal_entity` descendants and other `authz_actor_group`s. `system_actor` descends from
  `entity`, not `legal_entity` — so the database structurally prevents "someone put the
  anonymous actor in a group that later got a wildcard grant." This is a free, hard
  guarantee that a `corporation`-typed actor would not give us.

### 2. Ownership guard (the flagged risk, closed at the data layer)

The design-space survey's headline risk is that the shared actor ends up as some entity's
`owner_id`, handing every tokenless caller unconditional full control — and `owner_id` is
**immutable after insert** (`entities_immutable_owner`, `0013_entity_ownership.sql:51-63`),
so the mistake is unrecoverable without a manual data fix.

Close it with a `BEFORE INSERT OR UPDATE ON entities` trigger in the same mod-users
migration that rejects any row whose `owner_id` resolves to a `system_actors` row:

```
IF NEW.owner_id IS NOT NULL
   AND EXISTS (SELECT 1 FROM system_actors WHERE entity_id = NEW.owner_id)
THEN RAISE EXCEPTION 'entities: a system actor may not own an entity (owner_id=%)', NEW.owner_id;
```

A primary-key lookup on a one-row table; negligible cost, and it generalizes to any future
system actor. This converts the survey's "operational discipline" concern into a
database-enforced invariant.

Note this trigger lives on `entities`, which exists from core migration `0008` — well
before mod-users' 100–199 range. mod-users *cannot* add an equivalent guard to `grants`
(mod-authz, range 500s, runs after mod-users), which is why the no-grants invariant is
enforced by boot-time assertion instead (§4).

### 3. The middleware: `ResolveActorOrAnonymous`

Lives in `api/internal/auth/anonymous_actor.go`, re-exported through the existing public
facade `api/auth/auth.go` next to `RequireAuth`/`NewRequireVerifiedEmail`.

```go
func ResolveActorOrAnonymous(
    verifier *Verifier, mapper ClaimMapper, resolver *UserResolver,
    anonActorEntityID int64,
) func(http.Handler) http.Handler
```

Behavior, branch by branch on `AuthenticateRequest`'s already-classified sentinel errors:

| `AuthenticateRequest` result | `RequireAuth` today | `ResolveActorOrAnonymous` |
|---|---|---|
| success | `WithUserContext` + `WithActor` (+ `WithSudoActor`) | **identical** |
| `ErrNoAuthHeader` | 401 | **`WithActor(anonActorEntityID)` + anonymous marker, continue** |
| `ErrInvalidToken` | 401 | 401 (unchanged) |
| `ErrUserGone` | 401 | 401 (unchanged) |
| claim-map / resolver fault | 500 | 500 (unchanged) |

Only the missing-header case diverges. **A presented-but-bad token must never silently
downgrade to anonymous** — that is the classic confused-deputy pattern where an expired
session quietly starts returning public-only data (or, worse, a caller believes they are
authenticated while being served anonymous results). Fail loudly instead.

This is not a bolt-on to `RequireAuth` — as the current-state audit notes, every
`RequireAuth` error branch is reject-and-stop by construction. But the extension point was
anticipated: `AuthenticateRequest` was already extracted precisely for this, and its own
doc comment says so (`api/internal/auth/middleware.go:14-16`): *"Callers use errors.Is to
distinguish 'no session' … from genuine internal faults … possibly fall through to an
alternate auth path."* The implementation should extract the shared success-population and
error-mapping blocks (`middleware.go:73-111`) into one unexported helper that both
middlewares call, so the two cannot drift.

Invariants the middleware must hold, and test:

- It never sets `opctx.WithSudoActor` on the anonymous branch. Identity assumption requires
  an authenticated admin, full stop.
- It never sets `WithUserContext` on the anonymous branch. This makes composition with
  `RequireVerifiedEmail` fail **closed**: that middleware already 500s with a
  "server misconfiguration" log when `UserContext` is absent
  (`api/internal/auth/require_verified.go:20-27`). A route group that wrongly composes
  both breaks visibly in the first integration test rather than silently.
- It exposes `auth.IsAnonymousActor(ctx) bool` (a distinct context key, not a reuse of the
  existing derived-from-`email IS NULL` `IsAnonymous` or the inert `is_anonymous` JWT
  claim) so handlers and observers can distinguish "shared anonymous actor" from
  "authenticated guest account."

### 4. Resolving the entity id at startup

A new `provides.services` entry constructs the id holder once at boot:

```yaml
- name: anonymousActor
  type: "*auth.AnonymousActor"
  constructor: auth.NewAnonymousActor
  returnsError: true
  args:
    - context
    - infra:pool
```

`NewAnonymousActor` runs `SELECT entity_id FROM system_actors WHERE slug = 'anonymous'`
(one new sqlc query, `GetSystemActorBySlug`) and **returns an error if the row is
missing** — so a deployment whose migrations have not been applied fails at boot rather
than at first anonymous request.

The same constructor performs the no-grants boot assertion:
`SELECT EXISTS(SELECT 1 FROM grants WHERE actor_id = $1)` → if true, log an error and
refuse to start. This runs after *all* migrations (including mod-authz's 500s), which is
why it belongs here rather than in a mod-users migration trigger.

Hardcoding the entity id as a Go constant is explicitly rejected: `entities.id` is
`BIGSERIAL` and its value is not deterministic across environments. Resolve by slug.

### 5. Manifest wiring — the piece that avoids an mfgen change

mod-users declares the middleware alongside the two it already provides
(`moduleforge.module.yaml:331+`):

```yaml
  middleware:
    - name: resolveActorOrAnonymous
      constructor: auth.NewResolveActorOrAnonymous
      args:
        - service:verifier
        - service:claimMapper
        - service:userResolver
        - service:anonymousActor
```

A consuming module then writes, on the route group that wants tokenless reads:

```yaml
    - prefix: /v1/repositories
      register: reposhttpapi.RegisterRepositoryRoutes
      register_args:
        - service:reposDeps
      scope: public
      middleware:
        - requireOIDCConfirmed
        - resolveActorOrAnonymous
```

This produces exactly the desired Go, verified against mfgen's emitter
(`mfgen/internal/codegen/main_gen.go:825-860`, `buildScopeNestingInner`): named
`middleware:` entries emit `r.Use(...)` lines first, then `case "public", "":` writes the
leaf statement directly with no auth wrap.

```go
r.Route("/v1/repositories", func(r chi.Router) {
    r.Use(requireOIDCConfirmed)
    r.Use(resolveActorOrAnonymous)
    reposhttpapi.RegisterRepositoryRoutes(r, reposDeps)
})
```

Three facts make this work cross-module with no compiler change:

1. `provides.middleware` is a generic, name-keyed mechanism — `middlewareVarFromNodes`
   (`main_gen.go:487-490`) searches **all** modules' middleware nodes by name, and the
   resolver builds those nodes from every module manifest
   (`internal/resolver/graph.go:163,315`).
2. Middleware nodes are unconditional reachability roots
   (`internal/resolver/reachability.go:49-63`) — mod-users' new middleware is always
   constructed in the composition root, whether or not mod-users itself references it.
3. `scope: public` already validates (`internal/schema/validate.go:310-321`,
   `recognizedScopes`) and already means "emit the leaf unwrapped."

The equivalent hand-wiring should also be added to the non-generated dev server
(`api/cmd/server/main.go`, alongside the three-tier nesting at 511-596) so the capability
is exercisable locally — mfgen does not regenerate that file (see its own note at
main.go:522-533).

### 6. Scope vocabulary: ship on `public` now, propose `anonymous` later

**Recommendation: `scope: public` + `middleware:`. Nothing waits on mfgen.**

The cross-project change the design-space survey describes (a `scope: anonymous` literal
requiring edits to `mfgen/internal/schema/validate.go` and
`mfgen/internal/codegen/main_gen.go`, an mfgen release, and every consumer bumping) buys
one thing over the above: self-documentation. Under the recommendation, a module that
writes `scope: public` and *forgets* the `middleware:` line gets a route that reaches the
handler and then fails at `Authorize` with `ErrUnauthenticated` — i.e. exactly today's
behavior, fail-closed, no security regression, just a confusing 401.

That is a real ergonomic wart and a reasonable follow-up to propose to mfgen once the
mechanism has a consumer in production:

```go
case "anonymous":
    body.WriteString("\t\tr.Group(func(r chi.Router) {\n")
    body.WriteString("\t\t\tr.Use(resolveActorOrAnonymous)\n")
    ...
```
plus `"anonymous": true` in `recognizedScopes`. It is purely additive sugar over the same
middleware — no behavioral difference, no migration path, and it can land any time.

**What mod-users can ship now:** all of it. The migration, the actor, the middleware, the
facade, the manifest entry, the tests, the docs.
**What waits on mfgen:** only the optional `scope: anonymous` spelling. It is not on the
critical path for `Jhkw`.

---

## Composition with `POST /v1/auth/anonymous`

**They stay separate. Neither deprecates the other.** They solve different problems and
converging them would damage both:

| | `POST /v1/auth/anonymous` (existing) | Anonymous system actor (new) |
|---|---|---|
| Shape | Session bootstrap: one round trip, returns a JWT | Tokenless: zero round trips |
| Identity | One persisted `natural_person` + `user_accounts` row **per device** | One shared row for **all** callers |
| State | Owns things; has `anon_tokens` continuity across sessions | Owns nothing, ever |
| Upgradeable | Yes — becomes a named account, keeps its entity and its data | No — has no account and never will |
| Authority | Normal actor; can hold grants | Provably zero; holds no grants by construction |
| Best name | *guest account* | *anonymous actor* |

Converging them would require either making the shared actor stateful (catastrophic — one
shared `owner_id` across all tokenless callers, which is the exact risk we are guarding
against) or making the guest flow tokenless (loses per-device continuity, the whole point
of the `anon_tokens` table). Keep both.

A caller who *does* hold a guest token and hits an anonymous-enabled route takes the normal
authenticated branch and gets their own guest actor, not the shared one. That is correct
and should be an explicit test case.

Two things should converge, both cheap:

1. **Terminology.** "Anonymous" currently means the guest account. Rename in docs to
   *guest account* vs *anonymous actor*, and make the new context accessor
   `IsAnonymousActor` unambiguous against the existing derived
   `UserAccount.IsAnonymous` (`user_accounts.go:670`, `!ua.Email.Valid`).
2. **The stale claims the current-state audit found (§3e).** Two documented behaviors do
   not exist in code and will mislead anyone reading this area next:
   - The `is_anonymous` JWT claim (`api/internal/auth/local_jwt.go:44-63`) is **written
     but never read** — nothing in `jwt.go`, the claim mappers, or `resolver.go`
     extracts it, and neither `Principal` nor `UserContext`
     (`api/internal/auth/principal.go:10-29`) has a field for it.
   - `docs/architecture.md` and `anonymous.go:46-48` both claim `RequireVerifiedEmail`
     distinguishes anonymous sessions via that claim. It does not — it checks only
     `EmailVerifiedAt == nil` (`require_verified.go:30`).

   Fix the docs (or wire the claim through) as part of this work. Do **not** repurpose the
   inert claim for the new mechanism — the new actor has no JWT at all, so a JWT claim is
   the wrong carrier by definition.

---

## The authz duality — both paths, no special-casing

The consumer-context file asks that the actor identity work against both the single-row Go
`Authorize` and the list-scoping SQL access functions. Verified directly against both
implementations:

**Path A — single-row, Go** (`api/internal/authz/authz.go:108-186`):
`effectiveActor` returns `(anonEntityID, true)` → the `ErrUnauthenticated` early-return at
:110-113 is passed, which is the entire ask. Then:
`checkWildcardGrant` (:226-249) — `ActorChain` seeds the anon id, no group memberships
exist (blocked by the `0502` type trigger), no grants exist → `false`.
`checkGrantOrOwn` (:294-330) — grant arm `false`; own arm `e.owner_id = $1` `false` because
nothing may be owned by a system actor. Result: `ErrForbidden`. Which is correct: the
platform has no opinion, and the module's decorating `Authorizer` visibility arm now gets
to run.

**Path B — list scoping, SQL** (`mod-core/api/authz/setup/grant_table.go:28-123`):
signature is `accessible_<slug>_ids_for_actor(p_actor_entity_id BIGINT, p_op_ids INT[])` —
a bare entity id, same as Path A. All three UNION arms evaluate empty for the anon actor:
`WildcardAdmin` empty (no grants), the own-arm `e.owner_id = p_actor_entity_id` matches
nothing, `TargetChain` empty. Result: the empty set.

**The two paths agree exactly**, and they agree *by construction* rather than by
coincidence: the anonymous actor's platform-level authority is ∅ on both. Neither path
special-cases the id, and no code in `authz.go`, `opctx`, or `grant_table.go` changes.
`opctx.WithActor` takes an `int64` with no opinion about its provenance
(`mod-core/api/opctx/opctx.go:37-39`).

The corollary the consuming module must absorb: because Path B returns the empty set,
**mod-repos must add its public-visibility arm to its list query as well as to its
decorating `Authorizer`** — it cannot rely on the generated access function to surface
public repositories to an anonymous caller. That symmetry obligation is mod-repos' side of
the contract, and it is the same obligation it already has for any visibility rule that is
not grant-backed.

---

## Security considerations

Beyond the ownership risk closed in §2:

1. **No credential means no per-caller rate-limit key.** Every anonymous request shares one
   actor id, so actor-based throttling is meaningless. Any route opting into
   `resolveActorOrAnonymous` **must** sit behind a per-IP/CIDR limiter at the edge or in
   the composition root. mod-users should state this as a hard precondition in the
   middleware's doc comment and in `docs/architecture.md`. Shipping an in-process per-IP
   token bucket as a separate composable middleware is a reasonable follow-up but is out
   of scope for v1 — the right layer is usually the gateway, and pretending otherwise
   invites a false sense of protection.
2. **Audit attribution collapses.** `audit_log.actor_entity_id` would be the same value for
   every anonymous caller, so the per-actor activity index
   (`0400_audit_log.sql:16`) becomes useless for that id. Mitigations: (a) the invariant
   that **no module may authorize a mutating operation for the anonymous actor** — audited
   events are only emitted for `create/update/delete/assume/login/grant/revoke`, none of
   which an anonymous caller can reach without a module deliberately writing a visibility
   arm that permits a write, which is a module-level policy bug; (b) attribution for
   anonymous traffic is `opctx.RequestID` plus HTTP access logs, not `audit_log`. Document
   both. Consider a dev-mode assertion in the audit observer that flags any audit row
   written under a system actor.
3. **Never downgrade a bad token.** Covered in §3; restating because it is the single most
   likely implementation mistake. `ErrInvalidToken` and `ErrUserGone` must keep returning
   401.
4. **HTTP caching.** Anonymous and authenticated responses for the same URL differ. Any
   cache or CDN in front of an anonymous-enabled route must key on the Authorization
   header — emit `Vary: Authorization` on responses from these route groups, or the first
   anonymous response gets served to an authenticated caller (and vice versa).
5. **Composition hazards.** `resolveActorOrAnonymous` must never appear on the same route
   group as `requireAuth` or `requireVerifiedEmail`. The former is contradictory; the
   latter fails closed with a 500 (§3) which is acceptable but ugly. If a `scope: anonymous`
   literal is later added to mfgen, add a validation rule there that rejects the
   combination at generate time.
6. **The anonymous actor must never be loginable.** It has no `user_accounts` row, no
   credential, and no JWT can name it — `resolver.Resolve`'s local-issuer fast path
   (`api/internal/auth/resolver.go:154-172`) maps a JWT subject UUID to a `user_accounts`
   row, and there is none. Assert this with a test rather than leaving it implicit, and
   never add a `user_accounts` row for the system actor.
7. **`RequireOIDCConfirmed` still runs outermost** and can 503 before actor injection
   (`require_confirmed.go:22-42`). Acceptable — an unconfirmed instance should serve no
   traffic — but keep `requireOIDCConfirmed` first in the `middleware:` list, matching
   every existing route entry in mod-users' manifest.
8. **Enumeration and scraping** of public resources is the feature, not a bug, but
   anonymous-enabled list endpoints should enforce a hard maximum page size.

---

## Data model changes

One new mod-users migration, `model/migrations/sql/0101_system_actors.sql` (currently the
only file in that directory is `0100_schema.sql`; the assembled copy lands in
`model/schema/migrations/`). Four statements, all following patterns already in the tree:

1. **Seed the `system_actor` type** — exact shape of
   `mod-core/model/migrations/0007_type_service_account.sql`: a guard `DO $$` block
   asserting parent slug `entity` exists, then
   `INSERT INTO types (slug, parent_id, concrete, name, description)` with
   `slug='system_actor'`, `concrete=true`, parent `entity`.
2. **`system_actors` CTI table** — `entity_id BIGINT PRIMARY KEY REFERENCES entities(id)`,
   `slug TEXT UNIQUE NOT NULL`, plus the standard type-check trigger copied from
   `0012_service_accounts.sql` / `0010_natural_persons.sql`
   (`type_is_or_descends_from(v_type_id, 'system_actor')`).
3. **Seed the one row** — insert into `entities` with the `system_actor`
   `fundamental_type_id` (owner_id left NULL; the self-own trigger does not fire for this
   type), then the matching `system_actors` row with `slug='anonymous'`.
4. **`entities_no_system_actor_owner` trigger** — `BEFORE INSERT OR UPDATE ON entities`,
   body per §2.

Also:

- One sqlc query, `GetSystemActorBySlug`, in `model/queries/`. Regenerate `model/db/` with
  `cd model && sqlc generate` (never hand-edit `model/db/*.go`).
- **No change** to `authzSlugs` in `api/cmd/server/main.go:285-293` — deliberately, so no
  access function is generated for `system_actor` and it is invisible to all list paths.
- **No change** to `authz.go`, `opctx.go`, `grant_table.go`, `anon_tokens`, `user_accounts`,
  `RequireAuth`, or any existing route's scope.

Migration-ordering facts that constrain the design and were verified: mod-users' range is
100–199 `after: [core]`; `entities` and the ownership triggers land at core `0008`/`0013`,
so both are available. mod-authz's `grants` and `authz_actor_group_members` land at 500s —
**after** mod-users — which is why the no-grants invariant is a boot-time assertion (§4)
rather than a trigger, and why the anonymous actor cannot be modeled as an
`authz_actor_group` (mod-users could not insert into a table that does not exist yet at
its migration time).

---

## Alternatives considered and rejected

| Alternative | Why rejected |
|---|---|
| **Ephemeral non-persisted sentinel actor id** (e.g. `-1`, never in `entities`) | Strongest ownership guarantee (FK makes the bug impossible), but `audit_log.actor_entity_id NOT NULL REFERENCES entities(id)` aborts the enclosing transaction the first time anything audited runs under it — a runtime FK error deep in another module's write path, not a clean denial. Every future `REFERENCES entities(id)` actor column re-breaks. |
| **Per-request persisted anonymous user** (implicitly invoke `CreateAnonymousUser`) | Three inserts and a transaction per tokenless request; unbounded `entities` growth from crawlers; each row self-owns forever. A write-amplification DoS on a public endpoint. |
| **Model the shared actor as `service_account`** | Self-owns via `entities_owner_default_self`, so an anonymous caller could authorize any operation (the own-arm is not operation-gated) against the shared actor itself — self-DoS. Also appears in `accessible_service_account_ids_for_actor` results. |
| **Model it as `corporation`** | Correct on ownership (NULL owner_id) but semantically wrong, pollutes corporation listings and the generated `accessible_corporation_ids_for_actor`, and — being a `legal_entity` — remains eligible for actor-group membership, forfeiting a free structural guarantee. |
| **Model it as an `authz_actor_group`** | Semantically appealing ("the anonymous callers group") and NULL-owner by design, but `authz_actor_groups` is a mod-authz table in migration range 500s, which runs *after* mod-users' 100–199. mod-users cannot seed it. |
| **Grant the anonymous actor explicit read permissions** | Any grant applies to every tokenless caller simultaneously with zero isolation, and creates a permanent operational hazard around the grants table. Visibility is a property of the resource; express it in the module's policy, not as a grant to a pseudo-principal. |
| **Patch `RequireAuth` to fall through on `ErrNoAuthHeader`** | Turns the codebase's one hard security invariant into a conditional. A route mis-scoped to `authenticated` would silently downgrade to anonymous rather than 401. A separate, opt-in middleware keeps the default reject-and-stop behavior untouched. |
| **New `scope: anonymous` literal as the shipping mechanism** | Requires cross-project mfgen changes (`validate.go` `recognizedScopes`, `main_gen.go` `buildScopeNestingInner`), an mfgen release, and every consumer bumping — for zero behavioral gain over `scope: public` + `middleware:`. Worth proposing later as ergonomic sugar; not worth blocking `Jhkw` on. |
| **Reuse the existing `is_anonymous` JWT claim as the signal** | The claim is written but never read (`local_jwt.go:44-63`; nothing in `jwt.go`/mappers/`resolver.go` extracts it). More fundamentally, the new mechanism has no JWT at all, so a JWT claim cannot be its carrier. |

---

## Migration / rollout notes

Purely additive. No existing route changes scope, no existing middleware changes behavior,
no existing table changes shape. A deployment that applies the migration and does not use
the new middleware is byte-for-byte equivalent in behavior to today.

**Phase 1 — mod-users (this work, one plan phase):**
1. `0101_system_actors.sql` + regenerated `model/db/` (`sqlc generate`).
2. `NewAnonymousActor` service: slug lookup, missing-row error, no-grants boot assertion.
3. `ResolveActorOrAnonymous` middleware + the extracted shared helper refactor of
   `RequireAuth`, keeping `RequireAuth`'s behavior bit-identical.
4. `api/auth/auth.go` facade re-exports; `moduleforge.module.yaml` service + middleware
   entries; hand-wiring in `api/cmd/server/main.go`.
5. Tests: no-header → anonymous actor on opctx; invalid token → 401; expired token → 401;
   valid token → normal actor (not the shared one); sudo never set on the anonymous
   branch; `Authorize` reaches past `effectiveActor` and returns `ErrForbidden` (not
   `ErrUnauthenticated`) for an anonymous actor with no visibility policy; DB test that
   `INSERT INTO entities (..., owner_id = <anon id>)` raises; DB test that the anonymous
   actor cannot be added to an actor group; test that no JWT can resolve to it.
6. Doc updates: `docs/architecture.md` and `docs/mod-users-spec.md` — the guest-account vs
   anonymous-actor distinction, the rate-limit precondition, the `Vary: Authorization`
   note, the composition rule, and correction of the two stale `is_anonymous` claims.

**Phase 2 — mod-users release**, then mod-repos consumes: flips `/v1/namespaces` and
`/v1/repositories` to `scope: public` + `middleware: [requireOIDCConfirmed,
resolveActorOrAnonymous]`, adds its public-visibility arm to **both** its decorating
`Authorizer` and its list-scoping query, replaces the test-only fake actor with a genuinely
tokenless end-to-end HTTP test, and closes `Jhkw`.

**Phase 3 (optional, unblocked, any time) — mfgen** `scope: anonymous` sugar plus a
validation rule rejecting `anonymous` combined with `requireAuth`/`requireVerifiedEmail`.

Rollback: drop the two triggers, the `system_actors` table, the seeded rows, and the type,
in reverse order — standard goose `-- +goose Down`. Nothing depends on them unless a
consuming module has already opted in.

---

## Open questions for the mod-repos side

1. **List-path symmetry.** Does mod-repos' repository/namespace list query rely solely on a
   generated `accessible_*_ids_for_actor`, or does it already UNION its own visibility
   arm? It must do the latter — the generated function returns the empty set for the
   anonymous actor by design. Please confirm the list path and the single-row path are
   both covered before flipping the route scopes.
2. **Route surface.** Are the git smart-HTTP object endpoints in the same manifest route
   groups as the JSON metadata endpoints, or separate? They may need different middleware
   and very different rate-limit treatment.
3. **Denial semantics.** An anonymous caller hitting a *private* repository will now get
   `ErrForbidden` (403) rather than `ErrUnauthenticated` (401). Is 403 acceptable, or does
   mod-repos want to mask non-visible resources as 404 to avoid confirming existence? This
   is mod-repos' call and should be decided before UC5 ships.
4. **Any anonymous writes at all?** Clone counters, view records, ephemeral fetch state —
   anything that inserts a row on an anonymous request is an audited write under a shared
   actor and needs an explicit design exception (see Security §2). If the answer is "no
   writes ever," say so and we can assert it.
5. **Caching posture.** Does mod-repos intend to put a cache or CDN in front of anonymous
   reads? If so, `Vary: Authorization` needs to be set by mod-repos' handlers, not by
   mod-users' middleware.
6. **Rate limiting owner.** Confirm where the per-IP limiter for anonymous traffic will
   live — gateway, app composition root, or mod-repos. mod-users will document it as a
   precondition but will not ship it.
