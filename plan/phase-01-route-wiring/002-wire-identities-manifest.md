# Wire Identities And Credentials Routes Into The Module Manifest

## Purpose and scope

Wire the identity/credential self-service surface into
`moduleforge.module.yaml` so `mfgen`-generated host apps mount it, and re-export
every constructor/register symbol the new manifest entries reference through the
public facades — in this same task, per the mandatory anti-incident rule. Also
reconcile the hand-written reference dev server (`api/cmd/server/main.go`) to the
new split.

**Hard dependency:** task 001 must have landed first — this task references the
internal registrars (`RegisterSelfIdentitiesReadRoute` /
`RegisterSelfIdentitiesWriteRoutes`) and the public cache constructor
(`auth.NewStepUpConsumedCache`) that 001 adds.

No standard skill covers this; follow the Procedure below.

## Requirements

### 1. Public-facade re-exports (`api/handlers/handlers.go`)

Add, following the existing wrapper pattern in that file (type alias + thin
wrapper taking individually-typed params, mirroring `NewProvidersHandler` /
`NewOIDCConfigHandler` — not re-exporting the internal `*Deps` struct):

- Type alias: `type IdentitiesHandler = inner.IdentitiesHandler`.
- Constructor wrapper. Parameter order must match the manifest `args` order in
  §3 exactly:

  ```go
  func NewIdentitiesHandler(
      pool *pgxpool.Pool,
      queries *usersdb.Queries,
      oauth *auth.OAuth,
      obs *observer.ObserverGroup,
      sender authhandlers.Sender,   // exported interface — see gotcha below
      jwtSecret string,
      consumed *sync.Map,
      stepUpRequired bool,
  ) *IdentitiesHandler {
      return inner.NewIdentitiesHandlerWithDeps(inner.IdentitiesHandlerDeps{
          Pool:           pool,
          Queries:        queries,
          OAuth:          oauth,
          Obs:            obs,
          Sender:         sender,
          JWTSecret:      jwtSecret,
          Consumed:       consumed,
          StepUpRequired: stepUpRequired,
      })
  }
  ```

  **Sender-typing gotcha.** `inner.IdentitiesHandlerDeps.Sender` is typed as the
  unexported `emailSender` interface (`api/internal/handlers/identities.go:55`),
  so the facade cannot name it directly. Use the exported, method-set-identical
  `Sender` interface from `api/internal/handlers/auth`
  (`register.go:24`, alias `authhandlers` in this repo) as the parameter type —
  the same interface `infra:smtp` already satisfies for the `authHandler` entry.
  Assigning an `authhandlers.Sender` value into the `emailSender`-typed field is
  valid Go (identical method sets). Add the `sync` and `authhandlers` imports as
  needed.

- Two registrar wrappers delegating to task 001's internal registrars:

  ```go
  func RegisterSelfIdentitiesReadRoute(r chi.Router, h *IdentitiesHandler) {
      inner.RegisterSelfIdentitiesReadRoute(r, h)
  }
  func RegisterSelfIdentitiesWriteRoutes(r chi.Router, h *IdentitiesHandler) {
      inner.RegisterSelfIdentitiesWriteRoutes(r, h)
  }
  ```

The step-up cache constructor needs **no** new facade wrapper — task 001 already
placed `auth.NewStepUpConsumedCache` in the public `api/auth` package, which is
exactly the symbol the manifest references.

### 2. Manifest `provides.services` entries (`moduleforge.module.yaml`)

Add two service entries alongside the existing ones. Match the existing
formatting/comment style.

- **`stepUpConsumed`** — the consumed-JTI cache; its constructor starts the
  janitor as a side effect.

  ```yaml
  - name: stepUpConsumed
    type: "*sync.Map"
    constructor: auth.NewStepUpConsumedCache
    args:
      - context
  ```

- **`identitiesHandler`** — the identity/credential handler. `args` order is
  authoritative (matches the facade constructor in §1 and `main.go:459-468`):

  ```yaml
  - name: identitiesHandler
    type: "*handlers.IdentitiesHandler"
    constructor: handlers.NewIdentitiesHandler
    args:
      - infra:pool
      - queries:usersdb
      - service:oauthOrchestrator
      - service:observerGroup
      - infra:smtp                                        # satisfies the Sender interface
      - field:cfg.LocalAuth.JWTSecret
      - service:stepUpConsumed
      - field:cfg.Auth.RequireStepUpForCredentialChange
  ```

  Config-field name confirmed against `api/internal/config/config.go:118`
  (`AuthConfig.RequireStepUpForCredentialChange`; `Config.Auth` is `AuthConfig`
  at `config.go:155`). Neither constructor returns an error, so omit
  `returnsError`.

Declaration order: `stepUpConsumed` must be declared such that it is available to
`identitiesHandler` (place `stepUpConsumed` before `identitiesHandler`, or wherever
the resolver requires producers-before-consumers — follow the ordering the
existing entries use, e.g. `observerGroup` before its consumers).

### 3. Manifest `provides.routes` entries (`moduleforge.module.yaml`)

Add two route entries mirroring the two `/v1/self` entries exactly — same
`prefix`/`handler`, differentiated purely by `register:` and `middleware:`:

- **Read-only (List), reachable to unverified email:**

  ```yaml
  - prefix: /v1
    handler: identitiesHandler
    register: handlers.RegisterSelfIdentitiesReadRoute
    middleware:
      - requireOIDCConfirmed
      - requireAuth
  ```

- **Mutating (six endpoints), verified email required:**

  ```yaml
  - prefix: /v1
    handler: identitiesHandler
    register: handlers.RegisterSelfIdentitiesWriteRoutes
    middleware:
      - requireOIDCConfirmed
      - requireAuth
      - requireVerifiedEmail
  ```

Add a brief comment on each entry describing the endpoints and the
unverified-vs-verified rationale, matching the `/v1/self` entries' comment style.
Do **not** use an `expr:`-based single route — the two-entry, per-`middleware:`
approach is the sanctioned convention (self-route-manifest key decision).

### 4. Reconcile the reference dev server (`api/cmd/server/main.go`)

The reference server currently mounts `GET /self/identities` **inside** the
`requireVerifiedEmail` group (`main.go:539`), which diverges from the target
split. Reconcile it to match the manifest:

- Move `r.Get("/self/identities", identitiesHandler.List)` out of the
  `requireVerifiedEmail` group and into the `requireAuth`-but-not-verified group
  (alongside `r.Get("/self", selfHandler.Get)` at `main.go:529`).
- Leave the six mutating identity/credential routes (`main.go:540-549`) in the
  `requireVerifiedEmail` group.
- Update the surrounding comment to explain that this hand-written block mirrors
  the now-manifest-driven wiring (two `/v1` route entries registering
  `handlers.RegisterSelfIdentitiesReadRoute` under
  `requireOIDCConfirmed + requireAuth` and
  `handlers.RegisterSelfIdentitiesWriteRoutes` under those plus
  `requireVerifiedEmail`), and that this standalone dev server is not regenerated
  by `mfgen` — the same reconciliation-comment pattern the self-route plan used
  for `/self`. Behavior of the dev server must remain equivalent (the cache and
  janitor are still constructed at `main.go:457-458`; leave that direct wiring as
  is, or optionally route it through `auth.NewStepUpConsumedCache` for
  consistency — either is acceptable so long as the server still builds and the
  janitor still starts).

### 5. Public-facade call-shape coverage

Add a test that exercises the new public facade the way generated code will call
it — the direct guard against the `self-route-manifest` incident (a signature
mismatch that compiles here but breaks consuming apps). Prefer an external test
package (`package handlers_test` importing
`github.com/moduleforge/mod-users/api/handlers`, and `package auth_test` for the
cache constructor) so the exported surface is exercised from outside:

- Call `handlers.NewIdentitiesHandler(...)` with representative typed args
  (nil pool/queries/oauth/obs are safe — the constructor only stores fields; a
  nil or trivial `authhandlers.Sender`, an empty `jwtSecret`, a fresh
  `&sync.Map{}`, and `false`) and assert the result is non-nil.
- Register `handlers.RegisterSelfIdentitiesReadRoute` and
  `handlers.RegisterSelfIdentitiesWriteRoutes` on a `chi.NewRouter()` and assert
  the expected routes are mounted (e.g. via `chi.Walk` or by issuing requests and
  asserting non-404 routing), proving the wrappers register the surface.
- Reference `auth.NewStepUpConsumedCache` from the `api/auth` external test if
  not already covered by task 001's test, asserting a non-nil `*sync.Map`.

## Validation

- `make build.api` succeeds. (Apply the local, uncommitted `go.work` workaround
  if the worktree hits follow-up `oyo6`'s nesting issue.)
- `make test.unit.api` passes, including the facade call-shape test.
- `go vet ./...` / `make lint.api` clean for touched packages.
- `moduleforge.module.yaml` parses and contains: a `stepUpConsumed` service, an
  `identitiesHandler` service with the eight `args` in the specified order, and
  two `provides.routes` entries registering
  `handlers.RegisterSelfIdentitiesReadRoute` (no `requireVerifiedEmail`) and
  `handlers.RegisterSelfIdentitiesWriteRoutes` (with `requireVerifiedEmail`).
  Spot-check with:
  `grep -n "identitiesHandler\|stepUpConsumed\|RegisterSelfIdentities" moduleforge.module.yaml`.
- Every manifest `constructor:`/`register:` symbol resolves to a public-facade
  export: `handlers.NewIdentitiesHandler`, `handlers.RegisterSelfIdentitiesReadRoute`,
  `handlers.RegisterSelfIdentitiesWriteRoutes` exist in `api/handlers/handlers.go`,
  and `auth.NewStepUpConsumedCache` exists in `api/auth/auth.go`. Verify:
  `grep -n "func NewIdentitiesHandler\|func RegisterSelfIdentities" api/handlers/handlers.go`
  and `grep -n "func NewStepUpConsumedCache" api/auth/auth.go`.
- `api/cmd/server/main.go`: `GET /self/identities` is no longer inside the
  `requireVerifiedEmail` group; the six mutating routes remain inside it; the
  reconciliation comment is present. `make build.api` still builds the dev server.
- The facade constructor's parameter order matches the manifest `args` order
  one-for-one.
- `git diff --name-only` shows changes confined to the `mod-users/` tree
  (`moduleforge.module.yaml`, `api/handlers/handlers.go`, `api/cmd/server/main.go`,
  and the facade test file) — no edits to `mfgen/` or any consuming app.

## Metadata

architectural_impact: true

## Assumptions

- Task 001 has landed, so `RegisterSelfIdentitiesReadRoute`,
  `RegisterSelfIdentitiesWriteRoutes`, and `auth.NewStepUpConsumedCache` exist.
- `mfgen`'s current `constructor:`/`args:` shape supports a `context` arg and a
  `*sync.Map` return type with zero `mfgen` changes (matches the existing
  `auth.NewOAuth` `context`-arg entry). No manifest-render-and-compile harness
  exists in this repo to prove the generated output compiles; the facade call-
  shape test in §5 is the in-repo proxy for that guard.
- The task worktree may require the local, gitignored `go.work` workaround
  (follow-up `oyo6`); it must not be committed.

## References

- `plan/phase-01-route-wiring/001-registrars-and-stepup-cache.md` — the task that
  provides the symbols wired here.
- `moduleforge.module.yaml` — the two `/v1/self` service+route entries
  (`selfHandler`, `RegisterSelfGetRoute`/`RegisterSelfPutRoute`) are the exact
  template; the header comment on `provides.routes` states the facade re-export
  rule.
- `api/handlers/handlers.go` — existing wrapper pattern
  (`NewProvidersHandler`/`NewOIDCConfigHandler`, `RegisterSelfGetRoute`/`PutRoute`).
- `api/internal/handlers/identities.go:55,63-102` — `emailSender` (unexported)
  and `IdentitiesHandlerDeps` / `NewIdentitiesHandlerWithDeps`.
- `api/internal/handlers/auth/register.go:24` — the exported `Sender` interface
  the facade constructor param uses.
- `api/cmd/server/main.go:457-468,529-549` — cache/janitor construction, the
  handler deps, and the current route nesting to reconcile.
- `api/internal/config/config.go:118,155` — the `RequireStepUpForCredentialChange`
  field path.
- `AGENTS.md` Conventions §156 — the mandatory facade-re-export rule and the
  self-route-manifest incident (2026-07-15) it guards against.
- `plan/plan-summary-self-route-manifest.md` — precedent, including the
  `expr:`-rejected / two-entry decision and follow-up `oyo6`.

## Checkpoint hints

- After adding the public-facade re-exports in `api/handlers/handlers.go`.
- After adding the two `provides.services` and two `provides.routes` manifest
  entries.
- After reconciling `api/cmd/server/main.go`.
- After adding the facade call-shape test.
