# Identities & Credentials Manifest Wiring

## Purpose and scope

mod-users ships a complete identity/credential self-service HTTP surface — list
OIDC identities, start-link / unlink an OIDC identity, set / remove a local
password, and the step-up (request / verify) challenge — implemented in
`api/internal/handlers/identities.go` and wired only in the hand-written,
non-generated reference dev server (`api/cmd/server/main.go`, via
`handlers.NewIdentitiesHandlerWithDeps` plus a background
`auth.StartStepUpJanitor` goroutine and a process-lifetime `*sync.Map`
consumed-JTI cache). None of it appears in `moduleforge.module.yaml`, so the
`mfgen` codegen path never emits these routes into any generated host app
(`app-mftodo`, `app-mfdemo`, …). The result is a `404` on every generated app
for the entire surface, and step-up-gated credential changes can never be
satisfied because the verify endpoint that mints the step-up token is itself
unreachable.

This plan closes that gap by wiring the surface into `moduleforge.module.yaml`,
strictly mirroring the precedent set by the completed **self-route-manifest**
plan (see `plan/plan-summary-self-route-manifest.md`) for `GET`/`PUT /v1/self`.
Scope is confined to the `mod-users` tree: no changes to `mfgen` itself and no
changes to consuming apps beyond what they pick up by regenerating from the
updated manifest.

**Success criteria.**

- `moduleforge.module.yaml` declares the identity/credential surface so
  `mfgen`-generated apps mount all seven endpoints:
  `GET /v1/self/identities`, `POST /v1/self/identities/oidc/{provider}/start`,
  `DELETE /v1/self/identities/{identity_uuid}`,
  `POST`/`DELETE /v1/self/credential/password`,
  `POST /v1/self/credential/step-up`, and
  `POST /v1/self/credential/step-up/verify`.
- Every `constructor:` / `register:` symbol the new manifest entries reference
  is re-exported through the public facades (`api/handlers/handlers.go` and
  `api/auth/auth.go`) in the same task that adds the manifest entries — the
  mandatory guard against the `self-route-manifest` incident (2026-07-15) where
  a manifest-only addition compiled here but broke every consuming app's `mfgen`
  output.
- The read-only List endpoint is reachable to accounts with an **unverified**
  email (middleware `[requireOIDCConfirmed, requireAuth]`); the six
  credential-mutating endpoints additionally require a verified email
  (`+ requireVerifiedEmail`) — the exact `GET`-unguarded / mutating-gated split
  the self-route-manifest plan established, expressed via each entry's own
  `middleware:` list (never an `expr:`-based single route).
- The consumed-JTI cache and its janitor goroutine are constructed via an
  ordinary `provides.services` entry (a public wrapper constructor that starts
  the janitor as a side effect of construction) — **not** via `startupHooks:`,
  which is an app-manifest-only mechanism mod-users cannot declare on behalf of
  consuming apps.
- The hand-written `api/cmd/server/main.go` reference server is reconciled to
  match the new manifest-driven split (its List endpoint currently sits inside
  the `requireVerifiedEmail` group and must move out, alongside the reconciliation
  comment pattern the self-route plan used).
- Non-tautological tests prove the read-vs-mutating middleware split, the cache
  constructor / janitor start, and the public-facade call shapes.
- `docs/architecture.md` and `docs/mod-users-spec.md` document the surface.

**Hard constraints.**

- No edits to `mfgen/` or to any consuming app; all changes confined to
  `mod-users/`.
- Zero-touch-from-module-manifest wiring model preserved: the fix must not
  require any consuming app to hand-add a hook or route.
- The step-up wire formats stay stable (the `step_up_required` 409 body and the
  `X-Step-Up-Token` header contract are already relied on by consuming GUIs).

## Current status

Starting fresh; no tasks executed yet. Execution begins with **Phase 1 —
Identities & Credentials Route Wiring**, which is prerequisite to **Phase 2 —
Documentation Updates**. Within Phase 1, task 002 has a hard dependency on task
001 (it wires the symbols task 001 lands), so the two run sequentially; Phase 2
depends on both Phase 1 tasks completing.

**Verified pre-conditions (checked against code/docs this session).**

- `auth.StartStepUpJanitor(consumed *sync.Map, done <-chan struct{})` already
  exists and is exported (`api/internal/auth/stepup.go:194`); the public wrapper
  only has to call it.
- The step-up-gate config field is `cfg.Auth.RequireStepUpForCredentialChange`
  (`api/internal/config/config.go:118`; `Config.Auth` is `AuthConfig` at
  `config.go:155`), matching `main.go:467`.
- `IdentitiesHandlerDeps.Sender` is typed as the unexported `emailSender`
  interface (`api/internal/handlers/identities.go:55`); the public facade
  constructor must take an exported interface param — the existing exported
  `Sender` interface in `api/internal/handlers/auth` (`register.go:24`,
  method-set-identical) is the mirror to use.
- The `infra:smtp` singleton already satisfies the same email-sender interface
  for the `authHandler` entry, so the identities entry reuses `infra:smtp`.
- `api/cmd/server/main.go` currently nests `GET /self/identities` **inside** the
  `requireVerifiedEmail` group (`main.go:539`) — a real divergence from the
  target split that must be reconciled, not assumed already correct.
- **No manifest-render-and-compile harness exists in this repo** (searched:
  nothing renders `moduleforge.module.yaml` into a generated `main.go` and
  compiles it as a test). This is the strongest regression guard against exactly
  this class of gap, but building one is out of scope here; it is recorded as a
  known gap and flagged for the manager rather than constructed in this plan.

## Overview

### Phase 1 — Identities & Credentials Route Wiring

Wires the identity/credential surface into the module manifest end-to-end,
mirroring the self-route-manifest precedent. Two tasks, run sequentially (002
depends on 001's symbols):

- **Task 001 — Add Self-Identities Route Registrars And Step-Up Cache
  Constructor.** Pure groundwork with no manifest / facade coupling. Adds two
  internal per-middleware-group route registrars in `api/internal/handlers/`
  (one read-only registrar mounting `GET /self/identities`; one mutating
  registrar mounting the six credential-mutating endpoints), and the public
  step-up consumed-cache constructor `auth.NewStepUpConsumedCache(ctx)` in
  `api/auth/auth.go` (constructs a `*sync.Map` and starts the janitor as a side
  effect). Ships the non-tautological registrar-split test (read endpoint never
  behind the verified-email gate; mutating endpoints only reachable past it,
  modeled on `self_routes_test.go`) and a cache-constructor test. Leaves the
  system building and the dev server unchanged.

- **Task 002 — Wire Identities & Credentials Routes Into The Module Manifest.**
  Depends on 001. Adds the public-facade re-exports in `api/handlers/handlers.go`
  (an `IdentitiesHandler` type alias, the identities-handler constructor wrapper
  taking the exported `Sender` interface param, and the two registrar wrappers);
  the two `provides.services` entries (`identitiesHandler` and `stepUpConsumed`)
  and two `provides.routes` entries (read-only + mutating) in
  `moduleforge.module.yaml`; the `api/cmd/server/main.go` reconciliation (move
  List out of the verified-email group + reconciliation comment); and public-
  facade call-shape coverage. Facade re-exports and manifest entries land in the
  **same** task, per the mandatory anti-incident rule.

### Phase 2 — Documentation Updates

- **Task 001 — Update Architecture Docs.** Adds an Identities / Credentials row
  to `docs/architecture.md`'s "API layer" table and a use-case + API-definition
  section to `docs/mod-users-spec.md` for the surface (both currently absent) —
  mirroring what the self-route-manifest plan's doc phase did for `/self`.
  Depends on Phase 1.
