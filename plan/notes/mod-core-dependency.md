# mod-core dependency

## Purpose and scope

Records the exact upstream symbol this project's single task consumes, why the
dependency is a *merge*-time rather than a *commit*-time constraint, and how an
implementing agent verifies it is satisfied before touching any code.

## The symbol

mod-core's phase 1 task `001-add-effective-actor-accessor` adds, to
`mod-core/api/opctx/opctx.go`:

```go
func EffectiveActorEntityID(ctx context.Context) (int64, bool)
```

Semantics: returns `SudoActorEntityID(ctx)` when a sudo actor is set on the
context, otherwise falls through to `ActorEntityID(ctx)`; returns `0, false`
when neither is set. This is byte-for-byte the policy mod-users'
`effectiveActor` implements locally today, which is what makes the delegation a
zero-behavior-change swap rather than a policy change.

The `(int64, bool)` return shape is deliberate on mod-core's side —
mod-core's task document names mod-users' delegation as the reason — so
`effectiveActor` can delegate without adapting the result.

mod-core's phase 1 also adds `authz.RequireAuthenticated(ctx) error`. **This
project does not consume it.** mod-users keeps its own
`if !ok { return ErrUnauthenticated }` branch inside `Authorize`, because that
branch is part of `Authorize`'s flow, not a standalone precondition check.

## Why the gate is "merged", not "committed"

`mod-users/api/go.mod` resolves the module through a local replace directive:

```
replace github.com/moduleforge/core-api v0.0.0 => ../../mod-core/api
```

`scripts/link-siblings.sh` makes that relative path resolve from an arbitrarily
nested worktree by planting compatibility symlinks that point at the **sibling
repositories' main checkouts** under the moduleforge aggregate root — not at any
plan or task worktree. So a mod-users task worktree compiles against whatever is
checked out at `/Users/zane/playground/moduleforge/mod-core/api` right then,
which is mod-core's working branch.

Consequence for sequencing: mod-core's task 001 must be **merged to mod-core's
working branch** before mod-users' task is dispatched. A mod-core task branch
that is committed but not yet merged is invisible to this project's build.

## Verification before starting

Run from the task worktree root, after the build prerequisites in
[worktree build constraints](./worktree-build-constraints.md):

```sh
cd api && go doc github.com/moduleforge/core-api/opctx EffectiveActorEntityID
```

Prefer this over grepping a relative path into `../../mod-core` — it resolves
the symbol through the same replace directive and symlink layer the compiler
uses, so it cannot pass against a checkout the build will not actually see.

If the symbol is absent, the precondition is unmet: halt and report rather than
adding the accessor to mod-core from this project's worktree, or working around
it locally.

## Recorded elsewhere

The same dependency is recorded in this project's `plan/manifest.yaml` under the
`require-authenticated` entry, keyed on
`/Users/zane/playground/moduleforge/mod-core`.
