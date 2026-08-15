# Delegate Effective Actor

## Purpose and scope

Rewrite the body of the unexported helper `effectiveActor` in
`api/internal/authz/authz.go` so that it delegates to mod-core's new
`opctx.EffectiveActorEntityID(ctx)` accessor instead of re-deriving the
sudo-first-then-actor policy locally. After this change that policy has exactly
one implementation across the workspace instead of two that can silently
diverge.

This is a purely internal implementation swap with **zero behavior change**. The
function's name, signature, package, and unexported status are unchanged; its
single caller is unchanged; every 401 / 403 / success outcome is unchanged.

No standard skill covers this; it is a small, fully-specified Go edit. Follow
the `## Procedure` below.

Scope is one file: `api/internal/authz/authz.go`. No architecture or spec doc in
this repository documents `effectiveActor` or the local derivation, so none goes
stale here — the canonical cross-module
`docs/mf-standards/architecture/authorization-design.md` (a git submodule shared
across the workspace) is reconciled by mod-core's own documentation phase, not
by this task.

## Requirements

1. **Verify the upstream symbol exists before editing anything.** After running
   the build prerequisites in the `## Procedure` below, confirm:

   ```sh
   cd api && go doc github.com/moduleforge/core-api/opctx EffectiveActorEntityID
   ```

   prints the accessor. This task is gated on mod-core's phase 1 task
   `001-add-effective-actor-accessor` having been merged to mod-core's working
   branch — mod-users resolves `github.com/moduleforge/core-api` through
   `api/go.mod`'s `../../mod-core/api` replace directive, which reaches
   mod-core's **main checkout**, never a plan or task worktree.

   If the symbol is absent, **halt and report** the unmet precondition. Do not
   add the accessor to mod-core from this worktree, do not vendor a local copy,
   and do not work around it.

2. **Replace `effectiveActor`'s body** (currently at
   `api/internal/authz/authz.go:191-196`) with a single delegating statement:

   ```go
   func effectiveActor(ctx context.Context) (int64, bool) {
       return opctx.EffectiveActorEntityID(ctx)
   }
   ```

   The signature line must be byte-identical to what is there today. Only the
   body changes.

3. **Do not remove the `opctx` import.** It is still used — by the new
   delegation — and remains the package's only reference to
   `github.com/moduleforge/core-api/opctx`. Do not add any import.

4. **Refresh the `effectiveActor` doc comment**
   (`api/internal/authz/authz.go:188-190`). It currently reads:

   > `// effectiveActor returns the entity ID that should be used for policy checks.`
   > `// If a sudo actor is set (admin assuming another user's identity), that`
   > `// entity ID is returned, since the admin is acting as the sudo user.`

   Keep the first sentence's framing (what the function returns and what it is
   for), and rewrite the rest so it attributes the sudo-first-then-actor policy
   to `opctx.EffectiveActorEntityID` rather than restating a derivation this
   function no longer performs. State that the helper is retained as a
   package-local name for the concept rather than being inlined at the call
   site. Match the file's existing comment voice; do not introduce a new house
   style, and do not turn it into a changelog entry ("was previously...",
   "refactored to...").

5. **Correct the one stale sentence in the package doc comment**
   (`api/internal/authz/authz.go:12-13`), which currently reads:

   > `// The implementation resolves the acting user from ctx via opctx.ActorEntityID`
   > `// (and opctx.SudoActorEntityID for assume sessions).`

   It names the two low-level accessors as the resolution mechanism, which is no
   longer what this package calls. Rewrite it to name
   `opctx.EffectiveActorEntityID` as the accessor used, noting that it applies
   the sudo-first-then-actor policy. Keep it to roughly the same length — this
   is a one-or-two-line correction inside an existing package comment, not a
   rewrite of the package doc.

6. **Change nothing else in the file.** Specifically leave untouched:
   - `Authorize`'s doc comment (lines 90-107), including its
     `SudoActorEntityID` / `ActorEntityID` bullets — they describe the *policy
     semantics*, which are unchanged, not the call site.
   - `Authorize`'s body, including the `effectiveActor(ctx)` call and the
     `ErrUnauthenticated` return at lines 110-113.
   - The `Authorizer` struct, `New`, `ErrUnauthenticated`, `ErrForbidden`, the
     `coreAuthz.Authorizer` compile-time assertion, both `*Dispatch` helpers,
     `checkWildcardGrant`, and `checkGrantOrOwn`.

7. **Change no test file.** `api/internal/authz/authz_test.go`,
   `export_test.go`, `authz_integration_test.go`, and
   `anonymous_actor_integration_test.go` must all be byte-identical before and
   after. If a test fails, the refactor is wrong — fix the refactor. Do not
   weaken, skip, retarget, or delete any test to make the suite pass, and do not
   add new tests: the existing suite already covers the sudo-actor, real-actor,
   and no-actor paths through `Authorize`, and covering the delegation itself is
   mod-core's task's job.

8. **Do not consume `authz.RequireAuthenticated`.** mod-core's sibling task adds
   it, but this package keeps its own `if !ok { return ErrUnauthenticated }`
   branch inside `Authorize` — that branch is a step in `Authorize`'s flow, not
   a standalone precondition check. Introducing it here would change the call
   shape this task is required to leave alone.

## Validation

- `grep -n "func effectiveActor" api/internal/authz/authz.go` shows
  `func effectiveActor(ctx context.Context) (int64, bool) {` — unchanged.
- `grep -nE 'opctx\.(Sudo)?ActorEntityID\(' api/internal/authz/authz.go` returns
  no matches (exit 1): neither low-level accessor is called from this file any
  more.
- `grep -n "opctx.EffectiveActorEntityID(ctx)" api/internal/authz/authz.go`
  shows exactly one match, inside `effectiveActor`.
- `grep -n "effectiveActor(ctx)" api/internal/authz/authz.go` shows exactly one
  call site, still inside `Authorize`, immediately followed by the
  `return ErrUnauthenticated` branch.
- `grep -n "core-api/opctx" api/internal/authz/authz.go` still shows the import.
- `cd api && gofmt -l internal/authz` prints nothing.
- `cd api && go vet ./internal/authz/...` is clean.
- `cd api && go build ./...` succeeds.
- `cd api && go test ./internal/authz/...` passes — every pre-existing test in
  `authz_test.go`, unmodified. This is the zero-behavior-change gate.
- `cd api && go test ./...` (the module-wide unit suite) passes. If module
  download is unavailable, the package-scoped run above is the minimum bar;
  report the module-wide result either way rather than silently skipping it.
- `git diff --stat` shows **exactly one** changed file,
  `api/internal/authz/authz.go`, with a small line count (the body swap plus the
  two comment corrections). Any second changed file under `api/` — a test file
  above all — means a requirement was violated.
- `git status --short` shows no modification under `docs/mf-standards/`.

## Assumptions

- mod-core's phase 1 task `001-add-effective-actor-accessor` has been merged to
  mod-core's working branch before this task is dispatched, so
  `opctx.EffectiveActorEntityID` is present in the checkout the build resolves.
  Requirement 1 verifies this rather than trusting it.
- `opctx.EffectiveActorEntityID` implements exactly the policy `effectiveActor`
  implements today (sudo actor wins; otherwise the real actor; `0, false` when
  neither is set) with the identical `(int64, bool)` return shape. This
  equivalence is what makes the swap behavior-preserving; mod-core's task
  document specifies that shape and semantics explicitly.
- Go dependencies are **not** installed in this checkout; a first build or test
  may need to download modules.
- The sibling `replace` paths do not resolve from a nested worktree until
  `scripts/link-siblings.sh` has run — step 1 of the procedure.
- `docs/mf-standards/` may be an empty directory (uninitialized submodule). No
  step of this task reads or edits anything under it.
- All tests in `api/internal/authz/authz_test.go` pass before this task starts.

## References

- `api/internal/authz/authz.go` — the only file this task edits. Package doc
  lines 1-27 (the stale sentence is at 12-13); `Authorize` at 108-186 (its
  `effectiveActor` call and 401 return at 110-113); `effectiveActor` at 188-196.
- `api/internal/authz/authz_test.go` — the suite that must pass unchanged;
  `TestAuthorize_NoActor` is the direct no-effective-actor / 401 case, and the
  `ctxWithActor` / `ctxWithSudoActor` helpers at the top of the file are how the
  sudo-first-then-actor paths are exercised.
- [mod-core dependency note](../notes/mod-core-dependency.md) — the upstream
  symbol's contract, why the gate is merge-time rather than commit-time, and the
  verification command.
- [worktree build constraints note](../notes/worktree-build-constraints.md) —
  sibling symlinks, why the api-scoped test command is used rather than the root
  one, and the build-tagged integration tests.
- `AGENTS.md` — "Working in worktrees" and "Test commands".
- `plan/overview.md` — the federated plan's scope and hard constraints.

## Procedure

1. From the worktree root, run `make preflight.siblings` (equivalently
   `bash scripts/link-siblings.sh`) so `api/go.mod`'s sibling `replace` paths
   resolve at this worktree's nesting depth.
2. Run the requirement-1 `go doc` check. Halt and report if the symbol is
   absent.
3. Establish the baseline: `cd api && go test ./internal/authz/...` passes
   before any edit.
4. Read `api/internal/authz/authz.go` in full.
5. Replace `effectiveActor`'s body with the delegation, then refresh its doc
   comment and the stale package-doc sentence.
6. Run `gofmt -l internal/authz`, `go vet ./internal/authz/...`,
   `go build ./...`, `go test ./internal/authz/...`, then `go test ./...`, all
   from `api/`.
7. Run the `grep`, `git diff --stat`, and `git status --short` checks from
   `## Validation`.

## Status

- **Outcome:** succeeded
- **Date:** 2026-08-15
- **Summary:** Verified `opctx.EffectiveActorEntityID` is present in
  mod-core's main checkout (`go doc` succeeded — mod-core's phase 1 task
  `001-add-effective-actor-accessor` had already merged, per dispatch note),
  then rewrote `effectiveActor`'s body in `api/internal/authz/authz.go` as a
  single delegating `return opctx.EffectiveActorEntityID(ctx)` statement,
  refreshed its doc comment, and corrected the one stale sentence in the
  package doc comment. Signature, imports, call site, and all other code in
  the file are unchanged.
- **Validation:** `gofmt -l internal/authz` clean; `go vet
  ./internal/authz/...` clean; `cd api && go build ./...` succeeds; `cd api
  && go test ./internal/authz/...` passes (pre- and post-edit, byte-identical
  test files); `cd api && go test ./...` run module-wide — two pre-existing
  failures in unrelated packages (`api/auth`
  `TestNewStepUpConsumedCache_JanitorStopsOnCancel`, a goroutine-count timing
  flake, and `api/internal/auth`
  `TestResolveActorOrAnonymous_ComposedWithRequireVerifiedEmailFailsClosed`)
  were confirmed present and byte-identical on the pre-edit baseline via
  `git stash`, so neither is attributable to this change. All `## Validation`
  grep/diff/status checks pass; `git diff --stat` shows exactly one changed
  file, `api/internal/authz/authz.go` (8 insertions, 8 deletions); no
  modification under `docs/mf-standards/`.
- **Affected source files:** `api/internal/authz/authz.go`.
- **Assumptions relied on:** the `## Assumptions` section's claim that
  `opctx.EffectiveActorEntityID` implements exactly the sudo-first-then-actor
  policy `effectiveActor` implemented locally, with the identical
  `(int64, bool)` return shape — confirmed by the `go doc` output, which
  documents that exact behavior.
