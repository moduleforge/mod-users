# Plan Closeout: self-route-manifest

## What was planned and why

The mod-users `SelfHandler.Get`/`Put` business logic (`api/internal/handlers/self.go`)
already worked, and a hand-written, non-generated dev server
(`api/cmd/server/main.go`) demonstrated the intended `/self` wiring — but
`moduleforge.module.yaml` had **no** `register:` entry for `/self`, so the
manifest-driven `mfgen` codegen path never emitted `/self` into any generated
`main.go` (confirmed: zero `self` hits in `app-mftodo/cmd/server/main.go`). This
plan's goal was to close that gap: wire `GET/PUT /v1/self` into the manifest so
any app composing mod-users (starting with `app-mftodo`) actually receives
`/self` routes in its generated server, while preserving the hard constraint
that `GET /self` stay reachable to accounts with an unverified email and `PUT
/self` require a verified email. The plan was scoped strictly to `mod-users`
itself — no edits to `mfgen/`, `app-mftodo/`, or any sibling module/app were in
scope, and none were made.

## What shipped

### Phase 1 — Self-Route Wiring (2 tasks)

- **Task 1 — Wire Self Routes Into The Manifest Codegen Path**
  (`phase-01-self-route-wiring/001-wire-self-routes-manifest.md`, merge
  `ddc648b`): Implemented the initial version of all three required edits —
  a `RegisterSelfRoutes` function gating `PUT` only via a nested `r.Group`; a
  `selfHandler` service entry, a `/v1` route entry using
  `expr:requireVerifiedEmail` as a register-arg, and a `coreServices`
  requires-entry in `moduleforge.module.yaml`; and a dev-server (`main.go`)
  reconciliation comment, with the working hand-written block left untouched.
  `make build.api`, `go vet ./...`, and all structural validation checks
  passed. The only gap noted was a pre-existing, out-of-scope `gofmt` failure
  in `api/config/config.go`.

- **Task 2 — Redesign Self Route Registration Using Two Manifest Entries**
  (`phase-01-self-route-wiring/002-split-self-routes-two-entries.md`, merge
  `ce11bb4`): Replaced the single `RegisterSelfRoutes`/`expr:` wiring from
  Task 1 with two single-verb register functions (`RegisterSelfGetRoute`,
  `RegisterSelfPutRoute`) and two manifest entries differentiated purely by
  each entry's `middleware:` list — matching the sibling
  `RegisterAccountRoutes` convention exactly, per the user's explicit
  redesign choice made after the phase-1 architecture-conformance review (see
  Key Decisions below). Updated the dev-server reconciliation comment
  accordingly, and added a non-tautological test proving the
  GET-unguarded/PUT-gated split holds. All validation was green, and the
  diff stayed confined to the `mod-users/` tree.

### Phase 2 — Documentation Updates (1 task)

- **Task 1 — Update Architecture Docs**
  (`phase-02-doc-updates/001-update-architecture-docs.md`, merge `502d938`):
  Updated `docs/architecture.md` and `docs/mod-users-spec.md` to document the
  `GET`-unverified/`PUT`-verified `/self` contract as an instance of the
  existing per-entry-middleware convention (the `expr:` pattern was **not**
  documented as sanctioned, per the revised scope following Task 2's
  redesign). Appended a correction section to
  `plan/notes/mfgen-expr-middleware-pattern.md` noting that `/self` did not
  end up using `expr:` and that the note's original fragility premise had
  been independently shown incorrect. Also verified `GET`/`PUT /v1/self` were
  already present in `api/openapi.yaml`.

## Key decisions

- **Redesign from `expr:`-gated single route to two per-verb manifest
  entries.** A phase-1 architecture-conformance review — dispatched at the
  phase-1 boundary gate — found that the original `expr:`-based approach
  (Task 1) was an unnecessary deviation from the codebase's existing
  per-route-middleware convention (as exemplified by the sibling
  `RegisterAccountRoutes`). The trade-off (keep `expr:` and document it as a
  new sanctioned pattern, vs. redesign to match the existing convention) was
  presented to the user, who explicitly chose the redesign. Task 2 executed
  that redesign, and Phase 2's docs were scoped to match — the `expr:`
  register-arg pattern was deliberately **not** documented as a sanctioned
  approach, since it was no longer the shipped design.

- **Independent security-lens review confirmed no bypass path, and corrected
  a fragility claim in the plan's own notes.** A security-focused review
  traced the actual `mfgen` generator source directly and (a) confirmed the
  `GET`-unverified/`PUT`-verified split holds with no bypass path in both the
  original (`expr:`) and redesigned (two-entry) versions, and (b) found that
  a fragility claim in the plan's working notes
  (`plan/notes/mfgen-expr-middleware-pattern.md`) was factually incorrect —
  `mfgen` middleware nodes are unconditional reachability roots in the
  generator, not conditionally dropped from generated output as the notes
  had assumed. This correction was folded into Phase 2's doc-update task.

## Follow-up items

One open follow-up was logged against this plan (tag `self-route-wiring`; no
other tags from this plan's phases/tasks — `split-self-routes-two-entries` and
this plan's `doc-updates` task — had any follow-ups recorded):

- **`oyo6`** (2026-07-15, `phase-01-self-route-wiring/001-wire-self-routes-manifest.md`):
  The task worktree required a local, gitignored `go.work` file to make
  `make build.api`/`go build` runnable at all — a pre-existing structural
  issue where `api/go.mod`'s replace-directive relative paths assume a
  shallower nesting depth than `mod-users/worktrees/<task>/` actually
  provides. The workaround was not committed. Future task worktrees in this
  repo will likely hit the same issue and may benefit from a standardized
  fix (e.g., a prepare-task-worktree step that auto-generates the `go.work`
  file, or an `AGENTS.md` note documenting the workaround) rather than each
  task agent rediscovering it independently.

No other open follow-ups were found tagged to this plan's phases or tasks.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — Self-Route Wiring

- [x] [001-wire-self-routes-manifest.md](./phase-01-self-route-wiring/001-wire-self-routes-manifest.md) — tier `sonnet-high` · branch `phase-01-task-01-wire-self-routes-manifest` · commit `6687689` · merge `ddc648b`
- [x] [002-split-self-routes-two-entries.md](./phase-01-self-route-wiring/002-split-self-routes-two-entries.md) — tier `sonnet-high` · branch `phase-01-task-02-split-self-routes-two-entries` · commit `ec548a1` · merge `ce11bb4`

### Phase 02 — Documentation Updates

- [x] [001-update-architecture-docs.md](./phase-02-doc-updates/001-update-architecture-docs.md) — tier `sonnet-high` · branch `…` · commit `45852c3` · merge `502d938`
