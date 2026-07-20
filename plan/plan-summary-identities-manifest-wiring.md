# Plan Closeout: identities-manifest-wiring

## What was planned and why

mod-users already shipped a complete identity/credential self-service HTTP
surface — list OIDC identities, start-link / unlink an OIDC identity, set /
remove a local password, and the step-up (request / verify) challenge —
implemented in `api/internal/handlers/identities.go`. But it was wired only
into the hand-written, non-generated reference dev server
(`api/cmd/server/main.go`), via `handlers.NewIdentitiesHandlerWithDeps`, a
background `auth.StartStepUpJanitor` goroutine, and a process-lifetime
`*sync.Map` consumed-JTI cache. None of this appeared in
`moduleforge.module.yaml`, so the `mfgen` codegen path never emitted these
routes into any generated host app (`app-mftodo`, `app-mfdemo`, …) — every
generated app 404'd on the entire surface, and step-up-gated credential
changes could never be satisfied because the verify endpoint that mints the
step-up token was itself unreachable.

This plan closed that gap by wiring the surface into
`moduleforge.module.yaml`, strictly mirroring the precedent set by the
completed **self-route-manifest** plan (`plan/plan-summary-self-route-manifest.md`)
for `GET`/`PUT /v1/self`. Scope was confined to the `mod-users` tree: no
changes to `mfgen` itself and no changes to consuming apps beyond what they
pick up by regenerating from the updated manifest. Success required: all
seven endpoints reachable in generated apps; every new `constructor:`/
`register:` symbol re-exported through the public facades in the same task
that added the manifest entries (the mandatory guard against the
`self-route-manifest` incident of 2026-07-15); the `GET`-unguarded /
mutating-gated middleware split preserved exactly; the consumed-JTI cache and
its janitor wired via an ordinary `provides.services` entry rather than
`startupHooks:` (an app-manifest-only mechanism mod-users cannot declare on
behalf of consuming apps); the hand-written `main.go` reconciled to match;
non-tautological tests proving the split and the facade call shapes; and
`docs/architecture.md` / `docs/mod-users-spec.md` updated to document the
surface.

## What shipped

### Phase 1 — Identities And Credentials Route Wiring (2 tasks)

- **Task 1 — Add Self-Identities Route Registrars And Step-Up Cache
  Constructor** (`phase-01-route-wiring/001-registrars-and-stepup-cache.md`,
  merge `519bf1e2c72dcbc579ebc8cbfca9105fe97405b4`): Added
  `RegisterSelfIdentitiesReadRoute` (`GET /self/identities`) and
  `RegisterSelfIdentitiesWriteRoutes` (the six credential-mutating
  endpoints) mirroring `self_routes.go`'s per-verb-registrar convention, plus
  `auth.NewStepUpConsumedCache(ctx)` wrapping `StartStepUpJanitor` as a
  construction side effect. Shipped the non-tautological read/write split
  test plus three cache/janitor tests. Pure groundwork — no manifest,
  facade, or `main.go` changes in this task.

- **Task 2 — Wire Identities And Credentials Routes Into The Module
  Manifest** (`phase-01-route-wiring/002-wire-identities-manifest.md`, merge
  `8881f4b07415fd27f08b48f6ad0511b6b27e2f7c`): Wired `identitiesHandler` and
  `stepUpConsumed` into `moduleforge.module.yaml` as `provides.services`
  entries plus two `provides.routes` entries (read:
  `requireOIDCConfirmed` + `requireAuth`; write: the same two plus
  `requireVerifiedEmail`), following the `/v1/self` two-entry convention.
  Added the public-facade re-exports (`IdentitiesHandler` alias,
  `NewIdentitiesHandler` constructor with manifest-matching argument order
  taking the exported `authhandlers.Sender` interface, and the two
  registrar wrappers). Reconciled `main.go` to move `GET /self/identities`
  out of the verified-email group. Added a facade call-shape test guarding
  against a recurrence of the self-route-manifest-style incident.

Two ad-hoc gate-review fix commits were merged onto this plan branch after
the phase-1 tasks landed (dispatched as gate-review fixes, not plan tasks,
so they carry no task-record `git` block of their own):

- `54c548f` — "Merge branch '2026-07-19-fix-stale-line-citation'": corrected
  a stale `main.go` line-number citation in a doc comment, flagged by the
  phase-1 correctness lens.
- `c87f9fb` — "Merge branch '2026-07-19-fix-last-identity-409-wording'":
  corrected an imprecise 409-scoping claim in `docs/mod-users-spec.md`,
  flagged by the phase-2 correctness lens.

### Phase 2 — Documentation Updates (1 task)

- **Task 1 — Update Architecture Docs**
  (`phase-02-doc-updates/001-update-architecture-docs.md`, merge
  `c27d650bfdcf4808ab9b31ce3e2e4a75facaaf30`): Documented the identity/
  credential self-service surface Phase 1 wired into
  `moduleforge.module.yaml`: `docs/architecture.md` gained an
  Identities/Credentials API-layer table row plus a paragraph on the
  two-entry route split and `stepUpConsumed`'s constructor-starts-janitor
  model; `docs/mod-users-spec.md` gained a new use case, an API-definition
  block, and a security-requirements bullet. All claims were verified
  against the actual manifest/source rather than taken from the task doc's
  own excerpts.

## Key decisions

- **Step-up consumed-JTI cache wired as a `provides.services` entry, not
  `startupHooks:`.** The consumed-JTI cache/janitor is constructed via a new
  public `auth.NewStepUpConsumedCache` wrapper (task 1's digest), declared
  in `moduleforge.module.yaml` as an ordinary `provides.services` entry
  (task 2's digest) rather than through `moduleforge.app.yaml`'s
  `startupHooks:` mechanism. `startupHooks:` is app-manifest-only and
  cannot be declared from a module manifest, so it was structurally
  unavailable for mod-users to use on behalf of consuming apps — the
  constructor-starts-janitor-as-a-side-effect pattern was the only route
  that preserves the plan's zero-touch-from-module-manifest constraint.

- **Two manifest entries differentiated by middleware, not `expr:`.** The
  identities/credential routes are split into a `GET`-unguarded read entry
  (`requireOIDCConfirmed` + `requireAuth`) and a verified-email-gated write
  entry (`+ requireVerifiedEmail`) — mirroring the established `/v1/self`
  two-entry precedent from the prior `self-route-manifest` plan, rather
  than an `expr:`-based single route. `self-route-manifest` had itself
  redesigned away from an initial `expr:` approach after a
  architecture-conformance review found it an unnecessary deviation from
  the codebase's per-route-middleware convention (as exemplified by
  `RegisterAccountRoutes`); this plan applied that already-settled
  precedent from the start rather than re-litigating it.

- **Facade re-exports landed in the same task as each manifest addition.**
  Every new constructor/register symbol (`IdentitiesHandler` alias,
  `NewIdentitiesHandler`, the two registrar wrappers, and
  `auth.NewStepUpConsumedCache`) was re-exported through the public facades
  (`api/handlers/handlers.go`, `api/auth/auth.go`) in task 2, the same task
  that added the manifest entries referencing them — per the mandatory
  anti-incident rule established after the `self-route-manifest` incident
  of 2026-07-15, where a manifest-only addition compiled inside mod-users
  but broke every consuming app's `mfgen` output because the referenced
  symbol wasn't reachable through the public facade. Task 2's digest
  explicitly frames its facade test as "guarding the self-route-manifest-
  style incident."

## Follow-up items

Four open follow-ups in `plan/followups.yaml` are tagged to this plan's own
phases (`plan/phase:route-wiring`; the phase-2 doc-updates task logged none):

- **`UaNK`** (2026-07-20): The documented go.work workaround (follow-up
  `oyo6/building-common.md`) assumes single-level worktree nesting; this
  plan's task worktrees are double-nested (task worktree carved from a plan
  worktree) and needed additional `go work edit -replace` overrides for six
  cross-module deps beyond plain `go work use`. Flagged as worth a doc
  update / new follow-up so future doubly-nested task worktrees in this
  repo don't rediscover it from scratch.

- **`QKY6`** (2026-07-20): A companion note confirming the go.work
  workaround for this doubly-nested layout matched task 1's documented
  recipe exactly (five `../` plus explicit `-replace` overrides with
  `@v0.0.0` qualifiers) — reinforcing that the `oyo6` guidance should
  account for this nesting depth going forward.

- **`5RbD`** (2026-07-20): A pre-existing flaky test,
  `TestNewStepUpConsumedCache_JanitorStopsOnCancel`
  (`api/auth/stepup_cache_test.go`, added by phase-1 task 1), failed once
  under full-suite load (a goroutine-count timing assertion) but passed
  reliably in isolation and on repeat full-suite runs. Not touched by the
  task that introduced it; flagged as worth investigating if it recurs.

- **`biJk`** (2026-07-20): `api/openapi.yaml` — AGENTS.md's designated
  "authoritative REST API specification" — has zero hits for identities/
  credential/step-up. The identity/credential self-service surface now
  wired into `moduleforge.module.yaml` is undocumented there. This is a
  pre-existing gap (the endpoints already existed hand-wired in `main.go`
  before this plan), not introduced or worsened by phase 1, and was
  explicitly out of this plan's Phase 2 doc-update scope
  (`docs/architecture.md` + `docs/mod-users-spec.md` only). Recommended as
  a follow-up task to add these endpoints to `openapi.yaml`.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — Identities And Credentials Route Wiring

- [x] [001-registrars-and-stepup-cache.md](./phase-01-route-wiring/001-registrars-and-stepup-cache.md) — tier `sonnet-high` · branch `phase-01-task-01-registrars-and-stepup-cache` · commit `a54e67c` · merge `519bf1e2c72dcbc579ebc8cbfca9105fe97405b4`
- [x] [002-wire-identities-manifest.md](./phase-01-route-wiring/002-wire-identities-manifest.md) — tier `sonnet-high` · branch `phase-01-task-02-wire-identities-manifest` · commit `8756de1` · merge `8881f4b07415fd27f08b48f6ad0511b6b27e2f7c`

### Phase 02 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-02-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `phase-02-task-01-update-architecture-docs` · commit `1a2ca37` · merge `c27d650bfdcf4808ab9b31ce3e2e4a75facaaf30`
