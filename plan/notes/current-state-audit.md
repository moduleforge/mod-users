# mod-users current-state audit: actor identity resolution and enforcement

Scope: read-only research in `/Users/zane/playground/moduleforge/mod-users` (git HEAD as of
2026-08-04). All paths are absolute-repo-relative to `mod-users/` unless stated otherwise. A
`worktrees/plan/users-action-required-migration/` copy of several files exists in-tree but was
NOT used for citations below (main tree only), since it's a stale/parallel worktree.

---

## 1. Full actor-resolution chain

### 1a. opctx — the context-value carrier

`opctx` is NOT part of mod-users; it's defined in the sibling module mod-core, at
`/Users/zane/playground/moduleforge/mod-core/api/opctx/opctx.go` (imported via mod-users'
`api/go.mod` replace directive: `github.com/moduleforge/core-api => ../../mod-core/api`).

- `opctx.WithActor(ctx, entityID int64) context.Context` — opctx.go:37-39
- `opctx.WithSudoActor(ctx, entityID int64) context.Context` — opctx.go:44-46
- `opctx.ActorEntityID(ctx) (int64, bool)` — opctx.go:56-59 (returns `0, false` if unset)
- `opctx.SudoActorEntityID(ctx) (int64, bool)` — opctx.go:63-66
- `opctx.WithRequestID` / `opctx.RequestID` — request correlation, unrelated to actor identity.
- Package doc (opctx.go:1-20) states explicitly: "These values are set by HTTP middleware
  before any service method is called." There is exactly one place in mod-users that does this
  (see 1b).

### 1b. Where ActorEntityID/SudoActorEntityID actually get set on a real request

`mod-users/api/internal/auth/middleware.go`, function `RequireAuth` (middleware.go:69-114):

1. `AuthenticateRequest` (middleware.go:39-64) runs Bearer-extract → `verifier.Verify` →
   `mapper.Map(claims)` → `resolver.Resolve(ctx, principal)`, returning a `*UserContext`.
2. On success, middleware.go:106-110:
   ```go
   ctx := WithUserContext(r.Context(), uc)
   ctx = opctx.WithActor(ctx, uc.EntityID)
   if uc.AssumedUser != nil {
       ctx = opctx.WithSudoActor(ctx, uc.AssumedUser.EntityID)
   }
   next.ServeHTTP(w, r.WithContext(ctx))
   ```
   This is the **only** call site in mod-users that calls `opctx.WithActor` /
   `opctx.WithSudoActor` on a normal request path. (A second, incidental call to
   `opctx.WithActor` exists in `api/cmd/server/main.go:178`, inside the onboarding
   `AdminChecker` closure, used only to run an internal `az.Authorize(adminCtx, "manage", nil)`
   check for OIDC-onboarding admin re-confirmation — not part of the general request pipeline.)
3. `RequireAuth` is a `func(http.Handler) http.Handler` — standard chi middleware. If
   `AuthenticateRequest` errors (`ErrNoAuthHeader`, `ErrInvalidToken`, `ErrUserGone`, or an
   internal fault), the middleware writes an HTTP error response directly (401/500) and never
   calls `next.ServeHTTP` — so a rejected request never reaches app code at all, and no opctx
   actor is ever set. **There is no path through `RequireAuth` that lets a request continue
   without an Authorization header while still getting an actor.**
4. A public re-export facade exists at `mod-users/api/auth/auth.go:89-93` (`auth.RequireAuth`)
   that thinly wraps `inner.RequireAuth` (the `internal/auth` version above) for consumption by
   the manifest compiler / composition roots outside mod-users. Same behavior, same call site
   inside `internal/auth/middleware.go`.

### 1c. Where `authz.go`'s `Authorize` reads the actor back out

`mod-users/api/internal/authz/authz.go`:

- `Authorize(ctx, operation string, target *int64) error` — authz.go:108-186.
- Line 108-113 (confirmed current, matches the cited range in the task background):
  ```go
  func (a *Authorizer) Authorize(ctx context.Context, operation string, target *int64) error {
      // Resolve effective actor. Assumed actor takes priority over real actor.
      actorEntityID, ok := effectiveActor(ctx)
      if !ok {
          return ErrUnauthenticated
      }
  ```
- `effectiveActor` — authz.go:188-196:
  ```go
  func effectiveActor(ctx context.Context) (int64, bool) {
      if id, ok := opctx.SudoActorEntityID(ctx); ok {
          return id, true
      }
      return opctx.ActorEntityID(ctx)
  }
  ```
  Sudo actor (admin "assume identity") takes priority; otherwise falls back to the plain
  actor. If neither is set, `ok` is `false` and `Authorize` returns `ErrUnauthenticated`
  (authz.go:49, an alias of `apiresp.ErrUnauthenticated`) **before opIDs are computed, before
  the wildcard-grant check, before anything policy-related runs.** This is the exact bail-out
  point the architect needs to design around — it runs on every `Authorize` call from every
  service method (coreservice, users-module service layer, authz-module service layer all
  route through this one `Authorizer`).

### 1d. Chain summary (single path, no branching)

```
HTTP request
  → RequireAuth middleware (internal/auth/middleware.go:69)
      → AuthenticateRequest (middleware.go:39): Bearer header required, else ErrNoAuthHeader → 401, chain stops
      → verifier.Verify → mapper.Map → resolver.Resolve → *UserContext
      → opctx.WithActor(ctx, uc.EntityID)                [middleware.go:107]
      → opctx.WithSudoActor(ctx, ...) if assuming         [middleware.go:108-110]
  → next.ServeHTTP (app code, handlers, services)
      → authz.Authorize(ctx, op, target)                  [authz.go:108]
          → effectiveActor(ctx)                            [authz.go:191]
              → opctx.SudoActorEntityID(ctx) else opctx.ActorEntityID(ctx)
          → if !ok: return ErrUnauthenticated               [authz.go:111-113]  ← THE BAIL-OUT
```

There is exactly one producer of the opctx actor values (RequireAuth) and exactly one
consumer that gates all policy on their presence (Authorize/effectiveActor). No other
middleware, decorator, or service-layer code sets `ActorEntityID`/`SudoActorEntityID` on a
normal request path.

---

## 2. `requireAuth`/`requireVerifiedEmail` middleware — location, behavior, wiring

### 2a. Location

- Canonical implementation (unexported-reachable): `api/internal/auth/middleware.go` (RequireAuth,
  see 1b) and `api/internal/auth/require_verified.go` (RequireVerifiedEmail).
- Public facade re-export for cross-module consumption: `api/auth/auth.go`
  - `auth.RequireAuth` — auth.go:89-93 (thin wrapper over `inner.RequireAuth`)
  - `auth.RequireVerifiedEmail` — auth.go:95-99 (thin wrapper over `inner.RequireVerifiedEmail`)
  - `auth.NewRequireVerifiedEmail` — auth.go:101-105, returns `RequireVerifiedEmail` as a
    zero-arg-constructible `chi.Middleware` value; doc comment says "Used by generated wiring
    which calls constructors with zero args" — i.e. this exists specifically so the manifest
    compiler can instantiate the middleware without arguments.
  - There is no equivalent `NewRequireAuth` zero-arg wrapper — `RequireAuth` needs a verifier/
    mapper/resolver, which the compiler presumably threads through as normal constructor args
    (see main.go:514 for the by-hand equivalent).

### 2b. What they actually do

- `RequireAuth` (middleware.go:69-114): reject-or-populate. On any auth failure it writes the
  HTTP error itself and stops the chain (never calls `next`). On success it populates BOTH
  `WithUserContext` (richer struct, for handler code) AND `opctx.WithActor`/`WithSudoActor`
  (for service-layer/Authorizer code) before calling `next`. See 1b for exact lines.
- `RequireVerifiedEmail` (require_verified.go:16-43): pass-through-or-reject, reading state
  that `RequireAuth` already set — it does **no DB I/O** and does **not** touch opctx. It pulls
  `*UserContext` back out of context via `FromContext` (require_verified.go:18) and checks
  `uc.EmailVerifiedAt == nil` (require_verified.go:30). Two outcomes:
  - Missing `UserContext` (i.e. mounted outside/before `RequireAuth` in the chain) → 500,
    logged as "server misconfiguration" (require_verified.go:20-27). This is a hard ordering
    dependency: `RequireVerifiedEmail` is not self-sufficient and cannot run without
    `RequireAuth` having run first in the same request.
  - `EmailVerifiedAt == nil` → 403 with a stable `email_unverified` body
    (require_verified.go:30-38).
  - Otherwise calls `next.ServeHTTP` (require_verified.go:41).
  - **Notable gap**: despite the `is_anonymous` JWT claim existing (see §3), this middleware
    does not special-case anonymous users at all — it only looks at `EmailVerifiedAt`.
    Anonymous accounts have no email and thus no verified-at timestamp, so
    `RequireVerifiedEmail` blocks them from any `scope: verified` route exactly like an
    unverified named account. See §3 "gap" note.
- `RequireOIDCConfirmed` (require_confirmed.go:22-42) is a third, orthogonal middleware — gates
  ALL of `/v1/*` (except `/v1/oidc-config/*`) behind whether OIDC onboarding is confirmed, with
  a 503 if not. It runs BEFORE `RequireAuth` in the chain (outermost) and has nothing to do with
  actor identity — flagged here only because it is composed in the same router group and any
  new anonymous-actor middleware must be aware of it (it can 503 before actor injection ever
  gets a chance to run).

### 2c. How `scope:` wires to these middleware — manifest spec

`docs/mf-standards/manifest-spec.md:161` (route field table):

> `scope` — Authentication scope gate applied before this route group. Values: `public` (no
> auth required), `authenticated` (valid bearer token required), `verified` (authenticated +
> email verified). Default: `authenticated`.

`docs/mf-standards/manifest-spec.md:174` (for `mountFromModule:` routes):

> `mountFromModule:` routes' `scope:` and `middleware:` fields are honored the same way
> `register:` routes' are: both are wrapped in the appropriate `requireAuth`/
> `requireVerifiedEmail` middleware before the mount is emitted.

Worked examples in the spec (manifest-spec.md:793-841, the `/healthz`, `/v1/auth`,
`/v1/user-accounts`, `/v1/oidc-config`, `/v1/self` route entries) show `scope: public` /
`scope: verified` / `scope: authenticated` as plain YAML keys on each `routes[]` entry; the
compiler is the thing that reads `scope:` and decides whether to wrap the mount in
`requireAuth`, `requireAuth` + `requireVerifiedEmail`, or neither. **`scope: public` literally
means "wrap in neither" — confirmed both in the spec text and in mod-users' own manifest (next
paragraph). There is no third code path today that injects any actor for a public route; it is
simply unwrapped.**

Section 6 of the spec ("Routing scope matching", manifest-spec.md:434-451) describes only path/
method precedence and where `r.Use(...)` gets emitted for `middleware:` — it says nothing about
scope-driven actor injection either.

### 2d. mod-users' own manifest as a real (non-illustrative) example

`mod-users/moduleforge.module.yaml:224-266`: the `/v1/auth` route entry declares
`scope: public` (comment: "unauthenticated — bearer token not yet issued") and is wired with
`middleware: [requireOIDCConfirmed]` only — no `requireAuth`, no `requireVerifiedEmail`. Same
pattern for `/v1/oidc-config` (`scope: public`, comment "unauthenticated — OIDC onboarding
precedes auth"). This is the concrete, in-repo confirmation that `scope: public` today = "skip
the auth middleware entirely," full stop; nothing else happens.

### 2e. mod-users' own `main.go` / router bootstrap (concrete Go wiring pattern)

`api/cmd/server/main.go` is a **hand-written standalone dev server**, explicitly called out as
such and NOT regenerated by the manifest compiler (comment at main.go:522-533: "This file is a
non-generated standalone dev server that mfgen does not regenerate, so the hand-written block
below stays" — it exists to mirror what the generated composition root in a real host app would
produce). Key wiring, in order:

- `srv, r := server.New(cfg)` — main.go:365, builds the chi router.
- Health + `/v1/auth` mounted directly, unauthenticated — main.go:368-380, 504-509 (the actual
  `authhandlers.RegisterRoutes` call is commented out at main.go:508 with a
  `TODO(generated): auth routes wired here by mfgen` marker; only `r.Route("/v1/auth", func(r
  chi.Router) { r.Use(requireConfirmed) ... })` is live, confirming `/v1/auth` gets
  `requireOIDCConfirmed` only, nothing else — matches 2d).
- `requireConfirmed := auth.RequireOIDCConfirmed(onboarding.CurrentState)` — main.go:502, built
  once, reused across route groups.
- The `/v1` group (main.go:511-596) nests three concentric `r.Group`s:
  1. Outer: `r.Use(requireConfirmed)` — main.go:512.
  2. Middle: `r.Use(auth.RequireAuth(verifier, localMapper, resolver))` — main.go:513-514. This
     is the sole point where `RequireAuth` (and therefore `opctx.WithActor`) is attached, for
     ALL of `/v1/*` except `/v1/auth` and `/v1/oidc-config`. `GET /self` and `GET
     /self/identities` are mounted directly inside this middle group (main.go:534, 539) — i.e.
     "authenticated" scope, no verified-email gate.
  3. Inner: `r.Use(auth.RequireVerifiedEmail)` — main.go:542-543, wraps everything else
     (`PUT /self`, identity-management, core entity CRUD via `r.Mount("/", coreRouter)`,
     `/assume`, `/audit`, `/authz`) — i.e. "verified" scope.
- This three-tier nesting (`requireOIDCConfirmed` → `requireAuth` → `requireVerifiedEmail`) is
  the literal Go realization of the `public`/`authenticated`/`verified` scope vocabulary. **A
  new "inject anonymous actor for public/no-auth-header requests" step would need to slot in
  as a fourth possible middleware, conditionally applied instead of (or before) `RequireAuth`
  for whichever scope value ends up meaning "tokenless-but-not-fully-public."** There is no
  existing scope value between `public` (no wrapping at all, no actor) and `authenticated`
  (hard 401 without a valid bearer token) — this middle ground does not exist yet in the scope
  vocabulary or in the Go wiring.

---

## 3. The existing `POST /v1/auth/anonymous` flow in full

### 3a. Route registration

`api/internal/handlers/auth/routes.go:16`: `r.Post("/anonymous", h.Anonymous)`, inside
`RegisterRoutes`, mounted under the `/v1/auth` prefix which carries `scope: public` (§2d) —
i.e. this endpoint itself requires no bearer token, consistent with it being how a client
*gets* its first token.

### 3b. Handler

`api/internal/handlers/auth/anonymous.go`, method `Handler.Anonymous` (anonymous.go:24-74):

1. Decodes `{"device_id": "..."}` from the body; 400 if missing (anonymous.go:31-35).
2. Calls `h.userSvc.CreateAnonymousUser(ctx, svc.CreateAnonymousUserInput{DeviceID: req.DeviceID})`
   (anonymous.go:37-39).
3. Builds a minimal `db.UserAccount{Uuid: ...}` from the result UUID (anonymous.go:52, comment
   notes `IssueAnonymousJWT` only reads `.Uuid` so no extra DB round-trip is needed) and calls
   `localauth.IssueAnonymousJWT(dbUA, h.jwtSecret, h.issuer)` (anonymous.go:54).
4. Responds 201 with `{token, session_token, user: {uuid, is_anonymous: true}}`
   (anonymous.go:66-73).

### 3c. `CreateAnonymousUser` (service layer)

`api/internal/service/user_accounts.go`, method `UserAccountService.CreateAnonymousUser`
(user_accounts.go:268-...), runs inside one DB transaction (`txhelper.Run`):

1. Validates `device_id` non-empty (user_accounts.go:269-273).
2. Defaults `GivenName`/`FamilyName` to `"Anonymous"`/`"User"` if empty — needed because
   `NaturalPersonService.CreateInTx` requires non-empty names (user_accounts.go:275-284).
3. **Creates a real `natural_person` entity** via `s.npService.CreateInTx` (user_accounts.go:289-295)
   — this is a genuine mod-core entity row, exactly the same kind created for a normal
   registered user. There is no separate "anonymous entity kind."
4. **Creates a real `user_accounts` row** via `db.New(tx).CreateUserAccount` with
   `Email: pgtype.Text{Valid: false}` (NULL email) (user_accounts.go:297-304) — again the same
   table, same row shape as a normal account; the only distinguishing signal is a NULL email.
5. Generates a random 32-byte session token, SHA-256-hashes it for storage
   (user_accounts.go:306-315).
6. Inserts an `anon_tokens` row mapping `device_id` + hashed `session_token` → the new
   `user_account_id`, with a 30-day expiry (user_accounts.go:317-327,
   query `db.CreateAnonToken`, `model/queries/anon_tokens.sql` /
   `model/db/anon_tokens.sql.go`).

Answer to "is an anonymous UserAccount a real row indistinguishable from normal, or a distinct
kind of entity": **it is a completely ordinary `user_accounts` row and a completely ordinary
`natural_person` entity.** The `IsAnonymous` field (service struct `UserAccount.IsAnonymous`,
user_accounts.go:66) is *derived*, not stored — `user_accounts.go:670`:
`IsAnonymous: !ua.Email.Valid` — computed from "email column is NULL" at read time. Confirmed
in `docs/architecture.md:91`: "The derived boolean `is_anonymous` (email IS NULL) is exposed on
Go service structs and in API responses. Anonymous accounts are a valid `user_account` subtype:
they participate in authorization and auditing the same as named accounts." There is **no
`is_anonymous` column** anywhere in the schema — it's 100% derived from `email IS NULL`.

### 3d. Data model — `anon_tokens` table

`model/migrations/sql/0100_schema.sql:195-207` (also mirrored at
`model/schema/migrations/0100_schema.sql`):

```sql
CREATE TABLE anon_tokens (
  id              BIGSERIAL PRIMARY KEY,
  uuid            UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  device_id       TEXT NOT NULL,
  session_token   TEXT NOT NULL UNIQUE,      -- SHA-256 hex hash, not the raw token
  user_account_id BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX anon_tokens_device_id_idx       ON anon_tokens(device_id);
CREATE INDEX anon_tokens_session_token_idx   ON anon_tokens(session_token);
CREATE INDEX anon_tokens_user_account_id_idx ON anon_tokens(user_account_id);
```

Purpose per the migration's own comment (0100_schema.sql:189-194) and
`docs/architecture.md:37`: cross-session continuity — lets a device recover the *same*
anonymous identity across visits by presenting `device_id` + the raw session token (hashed and
compared server-side), rather than minting a brand-new anonymous user every visit. (Note: no
handler for *redeeming* a session_token was found in this pass — `POST /v1/auth/anonymous`
only ever creates; a "recover by session_token" endpoint may not be implemented yet or may live
elsewhere. Not confirmed either way; flagging as unverified rather than asserting.)

### 3e. `IssueAnonymousJWT` and the `is_anonymous` claim — important gap

`api/internal/auth/local_jwt.go:44-63`:

```go
func IssueAnonymousJWT(ua db.UserAccount, secret, issuer string) (string, error) {
    ...
    claims := localClaims{
        RegisteredClaims: jwt.RegisteredClaims{Subject: ua.Uuid.String(), Issuer: issuer, ...},
        Roles:       []string{},
        IsAnonymous: true,
    }
    ...
}
```

`localClaims` (local_jwt.go:12-17) has an `IsAnonymous bool \`json:"is_anonymous,omitempty"\``
field alongside the normal registered claims + `Roles` + `SudoUserUUID`.

**Gap found**: this `is_anonymous` claim is written into the JWT payload but **never read back
out** anywhere in the verify/claim-map/resolve pipeline. Confirmed by grep across
`api/internal/auth/claims*.go`, `jwt.go`, and `resolver.go` — no reference to `IsAnonymous` or
`is_anonymous` exists outside `local_jwt.go` itself and the two places that *write* it
(`anonymous.go` handler, and the `user_accounts.go` service struct, which is a *different*,
DB-derived `IsAnonymous` field, not the JWT claim). Neither `Principal`
(`api/internal/auth/principal.go:10-17`) nor `UserContext`
(`api/internal/auth/principal.go:21-29`) has an `IsAnonymous` field. So today:

- The anonymous JWT's `is_anonymous` claim is inert — a forward-looking / write-only field.
- `RequireAuth`/`resolver.Resolve` treat an anonymous-account JWT exactly like any other
  locally-issued JWT: `resolver.go:154-172` — the "local-issuer fast path" parses `Subject` as
  a UUID and does a straight `uuidLookup` (`GetUserAccountByUUID`-equivalent) with no
  anonymous-specific branch. This works because the anonymous account is a real row (§3c), so
  no special-casing is needed for the happy path.
  - **But** `docs/architecture.md`'s "All non-public endpoints require ... JWT" line and the
    `anonymous.go:46-48` comment both claim `RequireVerifiedEmail` "can distinguish anonymous
    sessions ... without a database round-trip" via `is_anonymous` — **this is not true of the
    current code**; `RequireVerifiedEmail` (§2b) only checks `EmailVerifiedAt == nil` and never
    looks at the claim or at `IsAnonymous`. The practical effect is identical (both null-email
    anonymous accounts and unverified named accounts get 403'd the same way), so the bug is
    latent/cosmetic today, but the aspirational comment is stale relative to the actual
    implementation — worth flagging to the architect since it signals someone already thought
    about "does this account need different treatment" and didn't finish wiring it through.

### 3f. Reusability assessment for the architect's tokenless-request problem

- **The current design assumes every anonymous actor is a persisted row.** There is no
  request-scoped/non-persisted anonymous actor concept anywhere in mod-users. Getting an actor
  at all requires: (a) a DB transaction that creates a `natural_person` + `user_accounts` row,
  (b) a JWT round-trip, (c) then normal `RequireAuth` processing on the *next* request. This is
  the "session-bootstrap" flow the task background already describes — confirmed accurate.
- Could the `is_anonymous` concept be reused for a non-persisted actor? Only loosely — the
  concept ("this actor has reduced/no email-bound trust") is reusable in spirit, but the
  concrete mechanism (derived from `email IS NULL` on a real DB row, or an inert JWT claim) has
  nothing that supports "actor exists only for the duration of this request, never persisted."
  A tokenless design would need either (a) a well-known persisted low-privilege actor entity
  shared across all anonymous requests (see §4 — no precedent exists for this in mod-users), or
  (b) a genuinely request-scoped, non-persisted actor id that `checkGrantOrOwn`/
  `checkWildcardGrant` in authz.go can still resolve grants against — but those SQL queries
  (authz.go:226-249, 294-330) take `actorEntityID int64` and JOIN against `entities`/
  `authz_actor_group_members`, i.e. **they assume the actor entity ID refers to a real row in
  `entities`.** A non-persisted actor id would need either a real placeholder entity row to
  join against, or a code path that bypasses the grant/ownership SQL entirely for that
  well-known id (e.g. a hardcoded low-privilege allow-list check ahead of
  `checkWildcardGrant`). Neither exists today.

---

## 4. Well-known/system/service-account actor pattern — searched, not found in mod-users

Grepped mod-users and (per instructions, briefly) mod-core for `SystemActor`, `PublicActor`,
`AnonymousActor`, `well-known actor`, `system actor` (case-insensitive) — **no matches**. There
is no shared, non-per-request actor entity constructed anywhere in mod-users for any purpose.

The closest analog is mod-core's `service_account` entity kind (referenced in
`api/cmd/server/main.go:285-293`'s `authzSlugs` list — `"service_account"` is one of the entity
types with a row-level-scoping access function; also seen in
`mod-core/api/authz/setup/grant_table_integration_test.go:386-400`,
`seedServiceAccount`/`CreateServiceAccount`). This is a **per-instance, persisted, individually
provisioned** entity type for machine/API clients (like `natural_person` and `corporation` are
for humans/companies) — it participates in the same grants-table authorization model as any
other actor. It is NOT a shared/well-known singleton actor injected automatically for a class
of requests; every `service_account` is its own row with its own entity id and must be
individually granted permissions, same as a human user. It doesn't provide a ready-made pattern
for "one shared low-privilege actor for all anonymous requests," but it does confirm the
authorization model already tolerates non-human actor entities as first-class citizens — i.e.
if the architect's design creates one persisted "anonymous"/"public" service-account-like row
and points every tokenless request's `opctx.ActorEntityID` at that same row, the grants/
ownership machinery in authz.go would very likely work against it unmodified (it only cares
that the id resolves to a row in `entities`, not what kind of entity it is).

---

## 5. Existing design intent — grepped, largely negative result

Searched `next-steps.md`, `plan/` (excluding `worktrees/`), `docs/architecture.md`, and
`docs/mod-users-spec.md` for "anonymous", "tokenless", "guest", "public actor" (case-insensitive).

- `next-steps.md`: no matches.
- `plan/`: only two files matched, and both are false positives from the word "public" in an
  unrelated context (`plan/plan-summary-users-apiresp-migration.md`,
  `plan/plan-summary-auth-apiresp-migration.md`, `plan/notes/server-error-site-inventory.md` —
  these are about the `apiresp` error-response package migration, not actor identity). No
  "tokenless"/"guest"/"public actor" design discussion found anywhere in `plan/`.
- `docs/architecture.md` and `docs/mod-users-spec.md`: multiple matches, but all describe the
  *existing* `POST /v1/auth/anonymous` flow already covered in §3 above (lines 36-37, 50, 57,
  70, 91, 97 of architecture.md; lines 168-231, 284 of mod-users-spec.md). Nothing describes a
  tokenless/no-round-trip design or a shared public actor.

**Conclusion: there is no existing design thinking in mod-users' own docs/plan about
tokenless requests, guest actors, or a shared public actor.** This appears to be a genuinely
new design problem from mod-users' side, consistent with the task background describing it as
a "blocking dependency" mod-repos discovered rather than something mod-users had already
scoped.

---

## 6. What makes this hard or easy — extension points

### Easy / favorable facts

- **Actor-context construction is centralized, not scattered.** Exactly one middleware
  (`RequireAuth`, §1b) sets `opctx.ActorEntityID`/`SudoActorEntityID` on the request path, and
  exactly one function (`effectiveActor` in authz.go, §1c) reads them back for policy
  decisions. There is no fan-out across multiple middlewares or service constructors that would
  need to be found and patched individually.
- **`opctx` itself has no opinion about where the actor id came from.** `WithActor` just takes
  an `int64`; nothing about it is coupled to "came from a verified JWT." Any code running before
  `Authorize` is called — including a brand-new middleware — can call `opctx.WithActor(ctx,
  someID)` and `Authorize` will accept it identically to a JWT-derived actor.
- **chi middleware composition already has a slot for this.** main.go's three-tier
  `r.Group`/`r.Use` nesting (§2e) is exactly the shape a fourth "inject anonymous actor when no
  Authorization header is present" middleware would plug into — e.g. as an alternative branch
  ahead of (or instead of) `RequireAuth` for a new scope value, without needing to touch
  `authz.go` or `opctx` at all.
- **The authorization SQL doesn't care what kind of entity the actor is** (§4) — as long as a
  real `entities` row exists at the target id, `checkWildcardGrant`/`checkGrantOrOwn` will work
  against it with zero changes to authz.go.
- **A public re-export facade already exists** (`api/auth/auth.go`) for exposing new
  internal-package middleware to the manifest compiler / other modules with a zero-arg
  constructor pattern (`NewRequireVerifiedEmail`, auth.go:101-105) — a new anonymous-actor
  middleware could follow the identical pattern for compiler consumption.

### Hard / unfavorable facts

- **No middle ground exists in the scope vocabulary today.** `public` = no wrapping, no actor,
  ever. `authenticated`/`verified` = hard 401 without a valid bearer token, via `RequireAuth`
  which never calls `next` on failure (§1b, §2b). There is no scope value or middleware
  variant today that means "try to authenticate, but if there's no token, inject a fallback
  actor instead of rejecting." That third mode needs new plumbing: either a new scope value in
  the manifest spec (`docs/mf-standards/manifest-spec.md:161`) plus compiler support, and/or a
  new middleware function analogous to `RequireAuth` but with permissive fallback behavior.
- **Every existing "actor" is backed by a real `entities`/`user_accounts` row and a JWT.** There
  is no precedent anywhere in mod-users for a non-persisted, purely-in-memory actor id. Even the
  "session bootstrap" anonymous flow (§3) fully persists before issuing a token. If the
  architect wants a truly tokenless, zero-round-trip anonymous actor, they will either need (a)
  one well-known persisted row (bootstrapped once, id embedded in config/constant, reused by
  every tokenless request — cheap, but is a single shared actor across all anonymous callers,
  weakening any future per-request rate limiting/ownership semantics since `owner_id` ownership
  checks in authz.go would treat every tokenless caller as the same owner), or (b) new code in
  `checkWildcardGrant`/`checkGrantOrOwn` (or a bypass ahead of them) that tolerates an actor id
  that doesn't resolve to a real `entities` row.
- **`RequireAuth`'s error branches never call `next`.** Any new "fall through to anonymous
  instead of rejecting" behavior cannot be bolted onto `RequireAuth` via a small patch — the
  function's structure (middleware.go:71-113) is fundamentally reject-and-stop on every error
  path, so a fallback would need to be inserted as an explicit new branch (e.g. treating
  `ErrNoAuthHeader` specially, distinct from `ErrInvalidToken`/`ErrUserGone`/internal faults,
  which should probably keep rejecting) rather than an outside wrapper.
- **The `is_anonymous` JWT claim is dead code today** (§3e) — anyone assuming it's already wired
  up as a signal for downstream middleware will be surprised; it would need to be connected
  (claim mapper → `Principal` → `UserContext`) before it's useful, or a new, similarly-named
  concept introduced deliberately for the tokenless case to avoid confusing it with the existing
  (currently inert) one.
- **`RequireOIDCConfirmed` sits outermost and can 503 before any actor-injection logic runs**
  (§2b) — any new middleware needs to be aware it may not even get invoked until onboarding is
  confirmed, on real deployments (though this is arguably orthogonal/acceptable, since an
  unconfirmed instance shouldn't serve any traffic anyway).

---

## File index (all citations above, deduplicated)

- `api/internal/authz/authz.go` — `Authorize` (108-186), `effectiveActor` (188-196),
  `ErrUnauthenticated`/`ErrForbidden` aliases (42-56), `checkWildcardGrant` (226-249),
  `checkGrantOrOwn` (294-330).
- `/Users/zane/playground/moduleforge/mod-core/api/opctx/opctx.go` — full file (73 lines).
- `api/internal/auth/middleware.go` — `AuthenticateRequest` (39-64), `RequireAuth` (69-114),
  opctx population (106-110).
- `api/internal/auth/require_verified.go` — `RequireVerifiedEmail` (16-43).
- `api/internal/auth/require_confirmed.go` — `RequireOIDCConfirmed` (22-42).
- `api/auth/auth.go` — public facade re-exports, `RequireAuth` (89-93), `RequireVerifiedEmail`
  (95-99), `NewRequireVerifiedEmail` (101-105).
- `api/cmd/server/main.go` — hand-written standalone dev server; `/v1/auth` mount (504-509),
  three-tier scope nesting (511-596), `requireConfirmed` construction (502).
- `api/internal/handlers/auth/routes.go` — `RegisterRoutes` (13-26), anonymous route (16).
- `api/internal/handlers/auth/anonymous.go` — `Handler.Anonymous` (24-74).
- `api/internal/service/user_accounts.go` — `UserAccount` struct incl. `IsAnonymous` (60-71),
  `CreateAnonymousUserInput` (73-78), `CreateAnonymousUser` (268-...), derived
  `IsAnonymous: !ua.Email.Valid` (670).
- `api/internal/auth/local_jwt.go` — `localClaims` (12-17), `IssueAnonymousJWT` (44-63).
- `api/internal/auth/principal.go` — `Principal` (10-17), `UserContext` (21-29) — neither has
  an `IsAnonymous` field.
- `api/internal/auth/resolver.go` — `Resolve`, local-issuer fast path (154-172).
- `api/internal/auth/jwt.go` — `Verify`/`verifyLocal` (58-98ish), claims returned as untyped
  `map[string]any`, no `is_anonymous` extraction.
- `model/migrations/sql/0100_schema.sql:195-207` (mirrored at
  `model/schema/migrations/0100_schema.sql`) — `anon_tokens` table DDL.
- `model/queries/anon_tokens.sql`, `model/db/anon_tokens.sql.go` — sqlc query/generated code
  for `anon_tokens`.
- `docs/mf-standards/manifest-spec.md` — scope field definition (161), mountFromModule scope
  honoring (174), routing scope matching (434-451), worked route examples incl. `scope: public`
  (793-841).
- `moduleforge.module.yaml` — mod-users' own manifest, `/v1/auth` (`scope: public`,
  224-252) and `/v1/oidc-config` (`scope: public`, 254-266) route entries.
- `docs/architecture.md` — anonymous-flow narrative (36-37, 50, 57, 70, 91, 97).
- `docs/mod-users-spec.md` — anonymous-flow spec (168-231, 284).
- `mod-core/api/authz/setup/grant_table_integration_test.go` — `seedServiceAccount` (386-400),
  `service_account` entity-kind precedent (not a shared/well-known actor pattern).
- `api/cmd/server/main.go:285-293` — `authzSlugs` list including `service_account`.
