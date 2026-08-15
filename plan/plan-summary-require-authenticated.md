# Plan Summary: require-authenticated

## What was planned and why

Refactor mod-users' unexported `effectiveActor()` helper
(`api/internal/authz/authz.go`) so that it delegates to the new
`opctx.EffectiveActorEntityID(ctx)` accessor in mod-core instead of re-deriving
the sudo-first-then-actor policy locally. After this change the policy "the sudo
actor wins over the real actor when both are present" has exactly one
implementation workspace-wide rather than two that can silently diverge.

This is **mod-users' slice of a federated, two-project plan** (mod-core,
mod-users). mod-core hosts the new exported primitives — landing in its own
sibling phases of this same plan (phase 1 "Authenticated Check Primitives",
phase 2 "Documentation Updates", in mod-core's plan worktree at
`/Users/zane/playground/moduleforge/mod-core/worktrees/plan/require-authenticated`).
This project consumes exactly one of them.

The capability was originally requested by mod-notifications and was initially
mis-filed against mod-authz; that followup has been closed and the request
re-routed here after a user-resolved escalation. Neither mod-notifications nor
mod-authz is part of this plan.

### What must change

- `api/internal/authz/authz.go` — the body of `effectiveActor` becomes a
  one-line delegation to `opctx.EffectiveActorEntityID(ctx)`, plus the two
  in-file doc comments that currently describe the local derivation.

That is the entire code footprint: one file, no new file, no deleted file.

### What must not change

- `effectiveActor`'s own signature, `func effectiveActor(ctx context.Context)
  (int64, bool)`, and its name. It stays unexported and stays in this package.
- Its single caller — the effective-actor resolution at the top of
  `Authorizer.Authorize`, including the `ErrUnauthenticated` (401) return on
  `!ok`.
- The `Authorizer` interface, `Authorize`'s method signature, `New`'s signature,
  the two test-seam dispatch helpers, and the two SQL check functions.
- Any observable behavior. Identical inputs must produce identical
  401 / 403 / success outcomes.
- Any test file. No existing test may be weakened, skipped, deleted, or
  rewritten to accommodate the refactor.
- `docs/architecture.md`, `docs/mod-users-spec.md`, and `AGENTS.md` — none of
  them documents `effectiveActor` or the local derivation, so none goes stale.
  The canonical cross-module doc
  (`docs/mf-standards/architecture/authorization-design.md`, a git submodule
  shared across the workspace) is reconciled by mod-core's own phase 2, not
  here.

### Success criteria

- `effectiveActor`'s body is a single `return opctx.EffectiveActorEntityID(ctx)`
  statement; no `SudoActorEntityID` / `ActorEntityID` call remains anywhere in
  `api/internal/authz/authz.go`.
- mod-users' existing `Authorize` unit suite
  (`api/internal/authz/authz_test.go`) passes **unchanged** — same test file
  bytes before and after.
- `cd api && go build ./...`, `go vet ./internal/authz/...`, and
  `gofmt -l internal/authz` are clean.
- `git diff --stat` shows exactly one changed file.

### Hard constraints

- **Gated on mod-core.** This work cannot start until
  `opctx.EffectiveActorEntityID` exists in the mod-core checkout that
  mod-users' `api/go.mod` resolves `github.com/moduleforge/core-api` against —
  that is mod-core's **main checkout**, reached via the `../../mod-core/api`
  replace directive, not mod-core's plan or task worktree. mod-core's phase 1
  task `001-add-effective-actor-accessor` must therefore be **merged to
  mod-core's working branch** before this task is dispatched. The details and
  the verification command are in the
  [mod-core dependency note](./notes/mod-core-dependency.md); the dependency is
  also already recorded in this project's `plan/manifest.yaml`.
- Zero behavior change. This is an implementation swap, not a policy change.
- Out of scope: mod-core's own two new exported symbols (added in mod-core's
  sibling phase — do not duplicate them here), mod-notifications' consumption of
  the capability, and any change to the `Authorizer` interface or to
  `Authorize`'s signature.
- No deadline constraints.

One phase, one task. The change touches a single function in a single file, all
information needed to implement it is already in hand, and its only complexity
is a cross-project ordering constraint the manager enforces at dispatch time —
none of which warrants further decomposition.

### Phase 03 — Effective Actor Delegation

Replaces the locally-derived sudo-first-then-actor policy in mod-users' authz
package with a delegation to mod-core's canonical accessor.

- **001 — Delegate Effective Actor.** Rewrite `effectiveActor`'s body in
  `api/internal/authz/authz.go` as a one-line call to
  `opctx.EffectiveActorEntityID(ctx)`, refresh the function's doc comment and
  the one sentence of the package doc comment that names the two underlying
  accessors, and verify behavior is unchanged by running the existing test suite
  untouched. Blocked on mod-core phase 1 task 001 being merged to mod-core's
  working branch.

No parallelism applies — the phase holds a single task.

## What shipped

### Phase 03 — Effective Actor Delegation

1. **Delegate Effective Actor** (`001-delegate-effective-actor.md`, tier `sonnet-med`) — Delegated mod-users' effectiveActor helper (api/internal/authz/authz.go) to mod-core's newly-landed opctx.EffectiveActorEntityID accessor, replacing the local sudo-first-then-actor derivation with a single delegating call. Confirmed the upstream symbol via go doc before editing. Refreshed doc comments per task wording constraints; changed nothing else. All in-scope validation passed; two module-wide test failures are pre-existing, unrelated, and confirmed byte-identical against the pre-edit baseline via git stash. git diff --stat confirms exactly one file touched.
   Commit `52d2211`, merged at `3d5992bd3750ec2aa9cb8c451376c0b3d59ca530`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Follow-up items

- **`E8OK`** — **Two pre-existing, unrelated test failures exi** — Two pre-existing, unrelated test failures exist on this worktree baseline and persist post-change: api/auth TestNewStepUpConsumedCache_JanitorStopsOnCancel (goroutine-count timing flake) and api/internal/auth TestResolveActorOrAnonymous_ComposedWithRequireVerifiedEmailFailsClosed (response-body decode assertion mismatch). Neither in this task's scope (api/internal/authz) nor introduced/worsened by this change — confirmed via git stash comparison — but worth tracking separately as failing tests independent of this plan.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 03 — Effective Actor Delegation

- [x] [001-delegate-effective-actor.md](./phase-03-effective-actor-delegation/001-delegate-effective-actor.md) — tier `sonnet-med` · branch `plan/require-authenticated-03-001` · commit `52d2211` · merge `3d5992bd3750ec2aa9cb8c451376c0b3d59ca530`
