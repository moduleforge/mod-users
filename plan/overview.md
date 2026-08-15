# Require Authenticated — mod-users slice

## Purpose and scope

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

## Current status

Plan created; no tasks executed. Execution begins at **Phase 03 — Effective
Actor Delegation**, task `001-delegate-effective-actor`, which is this
project's only task.

Pre-conditions at plan creation:

- `opctx.EffectiveActorEntityID` **does not exist yet** in mod-core
  (`/Users/zane/playground/moduleforge/mod-core/api/opctx/opctx.go` carries only
  `ActorEntityID` and `SudoActorEntityID`). The task must not be dispatched
  until mod-core's phase 1 task 001 has landed on mod-core's working branch.
- Go dependencies are **not** installed in this checkout, and this is a nested
  plan worktree, so `api/go.mod`'s sibling `replace` paths need the
  compatibility symlinks `scripts/link-siblings.sh` plants. The
  [worktree build constraints note](./notes/worktree-build-constraints.md)
  records the exact commands.
- `docs/mf-standards/` may be an uninitialized submodule (empty directory) in a
  worktree. No task in this phase reads or edits anything under it.
- All existing tests in `api/internal/authz/authz_test.go` pass before the task
  starts.

## Overview

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
