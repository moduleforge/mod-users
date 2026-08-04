# Design-space survey: tokenless-request actor resolution for mod-users

Scope: read-only research across mod-users (priority), with brief cross-checks into
mfgen, mod-authz, mod-core, and mod-repos' own architecture docs (which already
carry mod-repos' side of this dependency in detail). This does NOT duplicate the
sibling task auditing mod-users' current actor-resolution/middleware mechanics at
file:line granularity — that ground is only touched here where needed for context.

---

## 1. Platform-level ownership of the `scope:` vocabulary

**Finding: cross-project. mod-users cannot unilaterally add a new `scope:` value
(e.g. `anonymous`) — the vocabulary is a hardcoded enum inside `mfgen`, a separate
Go compiler project.**

- Canonical spec: `docs/mf-standards/manifest-spec.md` (mirrored identically into
  every module/app repo, including mod-users, mod-tasks, mod-contacts,
  app-mftodo — confirmed by grepping the same section text across repos). The spec
  itself states it documents `mfgen`'s behavior and is "canonical" but is a
  generated/authored contract, not the implementation.
- The real, canonical source lives at `/Users/zane/playground/moduleforge/mfgen`
  — a standalone Go project (`go.mod`, `internal/codegen`, `internal/schema`).
  mod-users' `docs/mf-standards/manifest-spec.md` is a vendored copy for reference,
  not the compiler.
- The `scope:` enum is implemented in exactly two places inside mfgen, both of
  which special-case the three literal strings:
  - `mfgen/internal/schema/validate.go` (~line 311-321): a `validScopes` map
    literal `{"public": true, "authenticated": true, "verified": true}` used for
    manifest validation — any other value is a validation error at generate time.
  - `mfgen/internal/codegen/main_gen.go` (~line 822-850): a `switch` statement
    that emits different router-wrapping code per scope (`case "public", "":`
    → no wrap; `case "authenticated":` → wrap in `r.Use(requireAuth)`;
    `case "verified":` → wrap in `requireAuth` then `requireVerifiedEmail`).
- **Consequence:** introducing a fourth scope value (e.g. `scope: anonymous` that
  emits a wrap like `r.Use(injectAnonymousActor)` before/instead of `requireAuth`)
  requires a code change in mfgen (both files above), a mfgen release, and every
  consuming repo (mod-users included) picking up the new mfgen version before the
  new scope value validates. This is a **cross-project change**, not something
  mod-users' repo alone can ship.
- **Escape hatch that does NOT require touching mfgen:** `scope: public` already
  compiles to "no auth middleware wrap, leaf statement directly." If the actor
  injection is implemented as its own middleware (not gated by mfgen's
  scope-driven `requireAuth`/`requireVerifiedEmail` switch), it could be applied
  globally at the composition root (e.g. wrapped around every router mount
  regardless of scope, or specifically around `public`-scoped routes via the
  existing `middleware:` field, which mfgen already honors generically — see
  manifest-spec.md line ~161, "Authentication scope gate applied before this
  route group" and the separate `middleware:` field). That is: **a global/opt-in
  actor-injection middleware sitting *underneath* `scope: public`, independent of
  mfgen's scope switch, is achievable without a compiler change.** Only a
  first-class new `scope:` literal (with its own validation and codegen branch)
  requires mfgen.

## 2. Prior design intent for anonymous/guest/tokenless actors

**Finding: mod-users has an existing, related-but-different "anonymous account"
feature (session bootstrap), but nothing addressing a genuinely tokenless
request. mod-repos already flagged this exact gap as a blocking external
dependency, in detail, in its own docs — but no design/implementation exists yet
anywhere in the ecosystem for a token-free actor.**

- Grepped mod-users' `next-steps.yaml`, `plan/followups.yaml`, `AGENTS.md` for
  "anonymous", "guest", "tokenless", "public actor" — **zero hits**. No
  in-progress or planned work in mod-users targets this.
- mod-users **does** have a real, shipped feature that is adjacent but distinct:
  - `POST /v1/auth/anonymous` (`docs/architecture.md` lines 57, 70;
    `docs/mod-users-spec.md` lines 168-231): creates a real, persisted
    `user_accounts` row with `email IS NULL` (`is_anonymous` is a derived
    boolean), plus an `anon_tokens` row (device_id + SHA-256 session token) for
    cross-session continuity. The response carries a **signed JWT** with
    `is_anonymous: true`. Upgrading to a named account clears `is_anonymous` and
    deletes the `anon_tokens` rows.
  - This is a *session-bootstrap* flow: the client calls this endpoint once,
    gets back a real bearer token, and then behaves like any authenticated
    caller from then on. It does **not** help a request that arrives with **no**
    `Authorization` header at all and expects the server to resolve *some*
    identity implicitly, mid-request, with no prior handshake.
- **mod-repos already did the legwork of identifying this exact gap** (not in
  mod-users, but in the requesting repo's own docs — worth reading directly if
  not already seen):
  - `mod-repos/docs/architecture.md` line 148: *"An anonymous HTTP caller is a
    precondition mod-repos depends on, not one it provides... mod-repos assumes
    some low-privilege anonymous actor identity is already on the request
    context by the time it runs — this is a blocking external dependency on
    mod-users, not something this module implements."*
  - `mod-repos/mod-repos-requirements.md` lines 215, 332-333: states plainly that
    the platform's `Authorize` returns `ErrUnauthenticated` *before any policy
    runs* when `opctx` carries no actor, that mod-users' existing `anon_tokens`
    mechanism is "adjacent but not sufficient as it stands," and references a
    doc called **`mod-users-ssh-extension.md`** which is supposed to carry this
    as an explicit requirement.
  - **That referenced doc does not exist in the mod-repos repo** (`find` for
    `*ssh-extension*` under mod-repos/docs returned nothing; the only matches
    were unrelated worktree directory names). It is a forward-reference to a doc
    that was apparently never written, or was written and later removed/never
    committed. So: the requirement is *named and cited* but the actual design
    content is not present anywhere I could find. This is a real gap the
    architect should know about — the citation trail dead-ends.
  - `mod-repos/mod-repos-requirements.md` line 333 lists, in a gap-tracking
    table: *"An anonymous/anon-token read identity that survives Authorize's
    actor check | mod-users | Partially exists (anon_tokens,
    POST /v1/auth/anonymous) but not in a usable shape."*
- Grepped mod-core and mod-authz (both repos, `.go` and `.md`, excluding the
  vendored `mf-standards` copies) for "anonymous", "tokenless", "public actor",
  "guest actor" — **zero hits in both.** Neither sibling repo has touched this
  problem at all.
- **Conclusion:** this is a known, named, externally-tracked gap (mod-repos is
  actively blocked on it and has written it down), but there is **no existing
  design** for the tokenless-actor-injection mechanism itself anywhere in the
  ecosystem. The architect is not working from a vacuum (the requirement and its
  shape — "some low-privilege actor identity must land on opctx before Authorize
  runs" — are already well-specified by mod-repos) but is working with a genuine
  blank page on the *mechanism*.

## 3. Actor-group primitives as a building block for a shared anonymous actor

**Finding: workable, but with one structural wrinkle — actor groups don't admit
`user_account` rows directly; they admit `legal_entity` subtypes (or other actor
groups). A single well-known anonymous actor would need to be a real
self-owning entity, and the existing anonymous-account flow already produces
one of exactly that shape.**

- `mod-authz/model/migrations/0501_authz_actor_groups.sql`: `authz_actor_groups`
  is itself a full `Entity` via class-table inheritance (CTI) — a concrete type
  under the root `entity` type, not under `legal_entity`. Grants made to a group
  are transitively inherited by all members, resolved at query time via a
  recursive CTE in the Authorizer.
- `mod-authz/model/migrations/0502_authz_actor_group_members.sql`: membership
  rows (`authz_actor_group_members`) are guarded by two triggers:
  - Cycle prevention (walks the membership graph, rejects cycles).
  - **Member-type restriction**: `member_id` must be either another
    `authz_actor_group` OR an entity whose type "is-or-descends-from"
    `legal_entity` (i.e. `natural_person`, `corporation`, or future
    legal-entity subtypes). The migration comment is explicit: *"user_accounts
    is not an entity type in the entities/types hierarchy — user_accounts is a
    separate login-identity table whose account_holder FK points to a
    legal_entity. The effective actor entity_id is the legal_entity entity_id."*
- **Implication for a shared anonymous actor:** you cannot put a `UserAccount`
  row directly into an actor group. What you'd actually add to an actor group
  (or use directly as the effective actor) is the `legal_entity` (concretely, a
  `natural_person`) that some `UserAccount.account_holder` points to. This is
  exactly the shape the *existing* `POST /v1/auth/anonymous` flow already
  produces — a real persisted account backed by a real self-owning
  `natural_person` entity (see §5 below re: `entities_owner_default_self`).
- Two viable variants, both mechanically sound given the group model:
  - **(a) One single, static, well-known anonymous entity** shared by every
    tokenless request — a single `natural_person`/anonymous-account row seeded
    once, its entity ID hardcoded or config-referenced, optionally placed in a
    dedicated `authz_actor_group` (e.g. "anonymous-callers") so grants can be
    scoped to it without hardcoding grants to a bare entity ID. This is cheap
    and requires no new table.
  - **(b) Per-request/per-session synthesis**, i.e. reusing the existing
    `POST /v1/auth/anonymous` machinery invisibly (server calls it internally,
    or a lighter-weight equivalent, when a request arrives with no
    `Authorization` header) — creates a real row per caller/session rather than
    sharing one. More consistent with how `anon_tokens` already models
    "one row per anonymous caller," but is heavier (a DB write on every
    first-touch of a tokenless caller) and raises its own cost/DoS questions
    (unbounded row growth from anonymous crawlers hitting a public endpoint).
  - The actor-group model itself does not prefer one over the other — both are
    representable. (a) is simpler and matches "low-privilege shared identity";
    (b) matches the existing account-per-session precedent already shipped.
    This is a real fork the architect needs to make a call on; nothing in the
    schema forces either choice.

## 4. Existing patterns for implicit identity without a bearer token

**Finding: none. No IP-based, fixed-system-credential, or reserved-signature
mechanism exists anywhere in mod-users or the sibling repos checked.**

- mod-users' JWT/session infra (`LocalAuth`, `JWTSecret`) is used exclusively for
  explicit, issued, bearer-token flows — register, login, anonymous
  session-bootstrap, OIDC callback, email-code. Every one of these produces a
  token the client then presents; none of them describe a mechanism for
  resolving identity from the request's *absence* of a token (IP address,
  static system credential, always-valid signature, mTLS, trusted-proxy header,
  etc.).
- mod-core has a `service_account` entity type (`api/httpapi/service_accounts.go`,
  `docs/mf-standards/architecture/entity-typing.md`) — a machine identity,
  distinct from `natural_person`/`corporation`, that **self-owns by default**
  (`entities_owner_default_self` trigger fires for `natural_person` and
  `service_account` types alike). This is a precedent for "a first-class,
  non-human, self-owning entity type" as a modeling pattern — structurally
  similar to what a shared anonymous actor would need — but it is not itself a
  tokenless-auth mechanism: service-account HTTP handlers still return
  `ErrUnauthenticated` when the caller isn't authenticated (checked directly in
  `mod-core/api/httpapi/service_accounts.go`), i.e. something still has to
  authenticate on their behalf before they act. Useful as a modeling precedent,
  not as a bypass precedent.
- No grep hit for "api key", "service credential", "trusted proxy", "mTLS", or
  similar in mod-users' auth code or docs.

## 5. Blast-radius / safety concerns

**Biggest, concrete risk found: the platform's ownership check
(`entities.owner_id = actor`) is unconditional on operation — owning any entity
grants full control (read/update/delete/grant/revoke/assume) over it, with no
per-operation gating. A shared, well-known anonymous actor that is *ever*
accidentally set as an entity's owner (e.g. by a create-flow that defaults
`owner_id` to "the current actor" without special-casing anonymity) would hand
every tokenless caller in the system full control over that entity — and unlike
a per-session anonymous account (where the blast radius is one throwaway row),
a *shared* anonymous actor makes that mistake immediately global across all
tokenless traffic.**

Detail, read directly from `mod-users/api/internal/authz/authz.go` (the real
implementation of the platform-wide `Authorizer.Authorize`, which lives in
mod-users despite the interface being declared in `mod-core/api/authz/authz.go`
— confirmed directly, not inferred; mod-authz itself contains no concrete
`Authorize` implementation, only the `authz-api` support types like
`OperationRegistry`):

- `Authorize(ctx, operation, target)` order of checks (lines ~108-186):
  1. Resolve effective actor from ctx (`SudoActorEntityID` takes priority over
     the real actor if set). **No actor on ctx → `ErrUnauthenticated` before any
     policy runs at all.** This is exactly the check mod-repos' docs describe as
     the current blocker for tokenless requests.
  2. `SatisfiedBy(operation)` computes the op closure; unregistered slugs fall
     back to a wildcard-`manage` check only.
  3. **Wildcard grant check** — if the actor (or any actor-group they
     transitively belong to) holds a grant with `target_id IS NULL` for an op in
     the closure, allow unconditionally, full stop, no target check at all.
  4. If `target == nil`, deny (nothing to resolve a grant against beyond the
     wildcard check already done).
  5. If `target != nil`: `checkGrantOrOwn` — one recursive-CTE query that ORs
     (a) an explicit grant via actor-chain × target-chain, with (b) a raw
     `entities.owner_id = actorEntityID` predicate. **The comment in the source
     is explicit and load-bearing:** *"Owning the target satisfies every
     operation on that entity (read, update, delete, assume, grant, revoke,
     ...); the own arm is not gated on the operation or on opIDs."*
- This ownership-check behavior is independently corroborated from the
  consuming side: `mod-repos/docs/architecture.md` (line 150) documents that it
  directly inspected this same `checkGrantOrOwn` function and confirmed the
  own-arm is unconditional — and notes this *corrected an earlier, wrong
  assumption* in mod-repos' own design history that the ownership check was
  "aspirational, not implemented." That earlier mistake is itself a signal:
  this exact behavior is easy to get wrong/underestimate, and has already
  burned one project in this ecosystem once.
- Separately, `mod-core/docs/mf-standards/architecture/entity-typing.md` (line
  36) documents `entities_owner_default_self`: entities whose fundamental type
  descends from `natural_person` or `service_account` get `owner_id := NEW.id`
  automatically at creation *when the caller supplies no explicit owner_id*.
  This means: whatever entity type a shared anonymous actor is modeled as, if
  it is a `natural_person` (the type the existing `POST /v1/auth/anonymous` flow
  already uses), it will self-own by default — which is fine and expected for
  the anonymous actor's own record, but is a reminder that owner_id defaulting
  is type-driven and automatic, not opt-in, throughout this codebase. Any code
  path that creates entities *on behalf of* the resolved anonymous actor (e.g.
  "anonymous users can create X") needs to make sure `owner_id` is never
  silently defaulted to the shared anonymous actor's ID for anything other than
  throwaway/anonymous-scoped resources — because that owner_id, once set, is
  **immutable** (platform-enforced trigger, noted in
  `mod-repos/docs/architecture.md` line 150) and grants unconditional full
  control forever.
- Second-order concern specific to the "single shared well-known actor" variant
  (§3 option (a)): because actor groups and grants are transitive and
  resolved per-request with no per-caller distinction beyond the entity ID
  itself, **any** grant or group membership ever added to the anonymous actor's
  entity ID (directly, or via an actor group it's placed in) applies
  simultaneously to *every* tokenless request in the system, with no isolation
  between callers. A per-session/per-account model (§3 option (b)) does not
  have this property — a mistake there is scoped to one throwaway account. This
  is a direct trade-off against option (a)'s simplicity and should be weighed
  explicitly, not assumed away.
- No wildcard grant should ever be created for the anonymous actor (or any
  group it belongs to) — `CreateWildcardGrant` bypasses `Authorize` entirely for
  the grant-creation call itself (see `mod-authz/api/service/grant_service.go`
  line ~372, "Authorize is NOT [checked]" for that internal helper's callers to
  enforce), so this is purely an operational/process discipline concern, not
  something the schema prevents.

---

## Sources consulted (file paths, for follow-up)

- `/Users/zane/playground/moduleforge/mod-users/docs/mf-standards/manifest-spec.md`
- `/Users/zane/playground/moduleforge/mfgen/internal/schema/validate.go`
- `/Users/zane/playground/moduleforge/mfgen/internal/codegen/main_gen.go`
- `/Users/zane/playground/moduleforge/mod-users/docs/architecture.md`
- `/Users/zane/playground/moduleforge/mod-users/docs/mod-users-spec.md`
- `/Users/zane/playground/moduleforge/mod-users/next-steps.yaml`,
  `plan/followups.yaml`, `AGENTS.md` (all grepped, no hits)
- `/Users/zane/playground/moduleforge/mod-repos/docs/architecture.md`
  (lines ~144-174, ownership + visibility section)
- `/Users/zane/playground/moduleforge/mod-repos/mod-repos-requirements.md`
  (lines ~103-215, ~330-354)
- `/Users/zane/playground/moduleforge/mod-authz/model/migrations/0501_authz_actor_groups.sql`
- `/Users/zane/playground/moduleforge/mod-authz/model/migrations/0502_authz_actor_group_members.sql`
- `/Users/zane/playground/moduleforge/mod-authz/api/service/grant_service.go`
- `/Users/zane/playground/moduleforge/mod-core/api/authz/authz.go` (interface only)
- `/Users/zane/playground/moduleforge/mod-users/api/internal/authz/authz.go`
  (concrete `Authorize` implementation — lines ~100-330)
- `/Users/zane/playground/moduleforge/mod-core/docs/mf-standards/architecture/entity-typing.md`
- `/Users/zane/playground/moduleforge/mod-users/docs/mf-standards/architecture/authorization-design.md`
- `/Users/zane/playground/moduleforge/mod-core/api/httpapi/service_accounts.go`
