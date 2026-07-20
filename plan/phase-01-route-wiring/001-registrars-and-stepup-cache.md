# Add Self-Identities Route Registrars And Step-Up Cache Constructor

## Purpose and scope

Add the internal symbols that the module-manifest wiring in task 002 will
reference, with no manifest or public-facade coupling yet. Two independent,
self-contained deliverables:

1. Two internal route registrars in `api/internal/handlers/` that split the
   identity/credential surface by middleware group — one read-only registrar
   (`GET /self/identities`) and one mutating registrar (the six
   credential-mutating endpoints) — mirroring the `RegisterSelfGetRoute` /
   `RegisterSelfPutRoute` per-verb-registrar convention in
   `api/internal/handlers/self_routes.go`.
2. A public step-up consumed-cache constructor `auth.NewStepUpConsumedCache` in
   `api/auth/auth.go` that builds a `*sync.Map` and starts the existing
   `StartStepUpJanitor` goroutine as a side effect of construction.

This task is pure groundwork: it adds symbols and tests but touches neither
`moduleforge.module.yaml`, `api/handlers/handlers.go`, nor
`api/cmd/server/main.go`. The build stays green and the reference dev server is
unchanged. No standard skill covers this; follow the Procedure below.

## Requirements

### 1. Internal route registrars

In a new file under `api/internal/handlers/` (suggested
`self_identities_routes.go`), add two functions with the same shape as
`self_routes.go`'s registrars (caller supplies the `/v1` prefix and the
middleware group; the registrar mounts paths only, adds no middleware or prefix):

- **Read-only registrar** — mounts exactly `GET /self/identities`
  (`h.List`). Suggested name `RegisterSelfIdentitiesReadRoute(r chi.Router, h *IdentitiesHandler)`.
  Its doc comment must state that the caller's middleware group is deliberately
  `requireOIDCConfirmed` + `requireAuth` only (no `requireVerifiedEmail`), so the
  List endpoint stays reachable to accounts with an unverified email — matching
  the `GET /self` rationale.
- **Mutating registrar** — mounts the remaining six endpoints. Suggested name
  `RegisterSelfIdentitiesWriteRoutes(r chi.Router, h *IdentitiesHandler)`:
  - `POST   /self/identities/oidc/{provider}/start` → `h.StartLink`
  - `DELETE /self/identities/{identity_uuid}`       → `h.Unlink`
  - `POST   /self/credential/password`              → `h.SetPassword`
  - `DELETE /self/credential/password`              → `h.RemovePassword`
  - `POST   /self/credential/step-up`               → `h.StepUpRequest`
  - `POST   /self/credential/step-up/verify`        → `h.StepUpVerify`

  Its doc comment must state that the caller's middleware group adds
  `requireVerifiedEmail` on top of the read group's middleware. The step-up
  request/verify endpoints belong in this (verified-email-gated) group, exactly
  as `api/cmd/server/main.go` mounts them today (`main.go:539-549`).

  The exact endpoint→handler-method mapping must match `main.go:539-549`
  verbatim (paths, verbs, and handler methods) so the manifest-driven wiring is
  behavior-identical to the reference server.

`IdentitiesHandler` and its `List`/`StartLink`/`Unlink`/`SetPassword`/
`RemovePassword`/`StepUpRequest`/`StepUpVerify` methods already exist in
`api/internal/handlers/identities.go`; this task only adds the registrar
functions, not handler logic.

### 2. Public step-up consumed-cache constructor

In `api/auth/auth.go` (the existing public facade over
`api/internal/auth`), add an exported constructor that constructs the consumed-
JTI cache and starts its janitor as a side effect of construction, so it can be
declared as an ordinary `provides.services` entry in task 002:

```go
// NewStepUpConsumedCache constructs the process-lifetime consumed-JTI cache for
// step-up tokens and starts the background janitor that prunes expired entries.
// The janitor runs until ctx is cancelled. Declared as a provides.services entry
// so the generated composition root constructs the cache and starts the janitor
// without needing an app-level startup hook.
func NewStepUpConsumedCache(ctx context.Context) *sync.Map {
    m := new(sync.Map)
    inner.StartStepUpJanitor(m, ctx.Done())
    return m
}
```

- `inner` is the existing `api/internal/auth` import alias in `auth.go`; add the
  `sync` import.
- `StartStepUpJanitor(consumed *sync.Map, done <-chan struct{})` already exists
  and is exported (`api/internal/auth/stepup.go:194`) — do not modify it or add
  any new internal symbol; the wrapper only calls it.
- The returned `*sync.Map` is the same instance the janitor prunes and that
  `IdentitiesHandler` will use for single-use step-up verification, so it must be
  returned (not discarded).

### 3. Tests

- **Registrar-split test** (`api/internal/handlers/`, new
  `self_identities_routes_test.go`): a non-tautological test modeled on
  `self_routes_test.go`'s `TestSelfRoutes_GetPutSplit`. Register the read
  registrar in a chi group with no verified-email marker and the write registrar
  in a group carrying a `verifiedEmailMarker`-style middleware, then assert:
  - `GET /self/identities` reaches the handler without the verified-email marker
    ever firing.
  - At least one representative mutating endpoint (e.g.
    `POST /self/credential/password`) is blocked when the marker denies and
    reaches the handler only when the marker allows.

  Reuse the `recoverToSentinel` technique from `self_routes_test.go`: the
  identities handler methods call `localauth.MustFromContext` first, which panics
  when no `*UserContext` is on the request context, giving an observable
  "reached the handler" signal distinct from "middleware short-circuited." The
  test must fail if a future edit merges the two registrars or applies the
  verified-email gate to the read route.

- **Cache-constructor test** (`api/auth/`, new or existing `_test.go`): assert
  that `NewStepUpConsumedCache`:
  - returns a non-nil `*sync.Map`, and two calls return two distinct instances;
  - returns a live consumed cache — drive a `VerifyStepUpToken` round-trip
    against the returned map (issue a token via `IssueStepUpToken`, verify once
    → nil, verify the same token again → `ErrStepUpRequired`), proving the map is
    the single-use store the janitor and handler share;
  - accepts a cancellable context and does not block or panic when that context
    is cancelled (janitor goroutine terminates cleanly).

  Do not attempt to assert janitor *pruning* timing: `StartStepUpJanitor` uses a
  hardcoded 1-minute ticker with no injection seam, so pruning-on-tick is out of
  scope for this test.

## Validation

- `make build.api` succeeds. (If the task worktree hits the known `go.work`
  nesting issue from the self-route plan's follow-up `oyo6`, apply the same
  local, uncommitted `go.work` workaround; do not commit it.)
- `make test.unit.api` passes, including the two new tests.
- `go vet ./...` (or `make lint.api`) is clean for the touched packages.
- New file(s): `api/internal/handlers/self_identities_routes.go` (registrars),
  `api/internal/handlers/self_identities_routes_test.go`, and the cache-
  constructor test under `api/auth/`. Confirm each exists.
- Grep confirms the endpoint set in the mutating registrar matches
  `main.go:539-549` exactly:
  `grep -n "self/identities\|self/credential" api/internal/handlers/self_identities_routes.go`
  lists the same seven paths (one read + six write) as `main.go`.
- `moduleforge.module.yaml`, `api/handlers/handlers.go`, and
  `api/cmd/server/main.go` are **unchanged** by this task (verify via
  `git diff --name-only`).
- The registrar-split test is non-tautological: temporarily removing the
  verified-email marker from the write group (or the split) makes the test fail.

## Metadata

architectural_impact: true

## Assumptions

- The task worktree may require a local, gitignored `go.work` file for
  `make build.api` to run at all (pre-existing structural issue documented as
  follow-up `oyo6` in the self-route-manifest plan). The workaround is local and
  must not be committed.
- No `sqlc`/`model` regeneration is needed; this task adds no queries.

## References

- `api/internal/handlers/self_routes.go` — the per-registrar convention to
  mirror (read vs mutating differentiated by the caller's middleware group).
- `api/internal/handlers/self_routes_test.go` — the `recoverToSentinel` /
  `verifiedEmailMarker` non-tautological split-test pattern to reuse.
- `api/cmd/server/main.go:529-549` — the authoritative endpoint→method→verb
  mapping and the current (to-be-reconciled) middleware nesting.
- `api/internal/handlers/identities.go` — the `IdentitiesHandler` methods being
  registered.
- `api/internal/auth/stepup.go:194` — `StartStepUpJanitor` signature the public
  wrapper calls.
- `api/auth/auth.go` — the facade package the new constructor is added to
  (`inner` = `api/internal/auth`).
- `plan/plan-summary-self-route-manifest.md` — the precedent this plan mirrors,
  including the non-tautological-test bar and follow-up `oyo6` (`go.work`).

## Checkpoint hints

- After adding the two internal registrars and their split test.
- After adding `NewStepUpConsumedCache` and its cache-constructor test.

## Status

- **Outcome:** succeeded (2026-07-19).
- **Implementation worktree:** `/Users/zane/playground/moduleforge/mod-users/worktrees/2026-07-20-wire-auth/worktrees/phase-01-task-01-registrars-and-stepup-cache`, branch `phase-01-task-01-registrars-and-stepup-cache`, commit `a54e67c`.
- **Files added/modified (repo-relative, inside the implementation worktree):**
  - `api/internal/handlers/self_identities_routes.go` (new — `RegisterSelfIdentitiesReadRoute`, `RegisterSelfIdentitiesWriteRoutes`)
  - `api/internal/handlers/self_identities_routes_test.go` (new — `TestSelfIdentitiesRoutes_ReadWriteSplit`)
  - `api/auth/auth.go` (modified — added `NewStepUpConsumedCache`, `sync` import)
  - `api/auth/stepup_cache_test.go` (new — three tests covering distinct instances, live single-use round-trip, and janitor-goroutine-exits-on-cancel)
- **Validation summary:** `make build.api` succeeded (after a local, uncommitted `go.work` workaround — see note below); `make test.unit.api` passed, including all four new tests; `go vet ./...` / `make lint.api` clean; both new registrar/cache-constructor files and both new test files confirmed present; grep of `self/identities|self/credential` in the new registrar file matches `main.go:539-549`'s seven paths (verbs and handler methods) exactly; `git diff --name-only` confirms `moduleforge.module.yaml`, `api/handlers/handlers.go`, and `api/cmd/server/main.go` are unchanged; the registrar-split test was manually confirmed non-tautological (temporarily dropping the verified-email marker from the write group made 2 of 3 subtests fail, then the test file was restored to its committed state).
- **`go.work` workaround note (follow-up `oyo6`):** this task worktree is nested *two* levels deeper than the single-nesting case `oyo6`/`building-common.md` documents (`mod-users/worktrees/2026-07-20-wire-auth/worktrees/<task>/`, not `mod-users/worktrees/<task>/`), so the aggregate root is 5 `../` up from the worktree root, not 3. The documented recipe's plain `go work use` also was not sufficient here: `go build` reported "conflicting replacements" for `core-model`, `core-api`, `audit-model`, `authz-model`, `audit-api`, and `authz-api` (each module's own `go.mod` `replace` directive, still resolved at the original 2-level-nesting depth, disagreed with the workspace's `use` entry for the same module). Resolved by also adding an explicit `go work edit -replace <module>@v0.0.0=<correct 5-../-path>` for each of those six modules. The resulting `go.work` (gitignored, not committed) sits at the implementation worktree root. Flagging this because the task doc's own Assumptions/Validation only anticipated the single-nesting-depth `oyo6` fix, not this doubly-nested case or the extra `-replace` overrides it required — future task worktrees under this same doubly-nested `worktrees/<plan-branch>/worktrees/<task>/` layout will likely need the same two-part fix (deeper `../` count + explicit `-replace` overrides, not just `use`).
- **Assumptions applied:** both `## Assumptions` bullets held — no `sqlc`/`model` regeneration was needed, and the `go.work` workaround was required (see note above for how it needed to be adapted for this worktree's extra nesting level).
