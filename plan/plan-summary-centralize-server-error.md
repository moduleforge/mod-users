# Plan summary: centralize-server-error

## What was planned and why

This plan addressed followup `8iRl` ("Literal server.Error sites not centralized",
tag `go-vocab-migration`): collapsing the remaining ~96 literal
`server.Error`/`server.ErrorWithDetails` call sites across seven handler files in
`api/internal/handlers/` (`apps.go`, `oidc_providers.go`, `oidc_config.go`,
`identities.go`, `self.go`, `assume.go`, `user_accounts.go`) onto sentinel-driven
`apiresp.WriteError(w, r, err)` calls. The goal, per the design doc's "Go-layer
ownership" section, is to have the status/code/envelope decision live in one place
(the shared `mod-core/api/apiresp` package) rather than being re-stated as literals
at each handler site.

This work was **deliberately deferred** by the earlier `users-apiresp-migration`
plan's phases 1-2, which established the correct sentinel vocabulary at every site
(Phase 2) and collapsed the three sentinel-classifying helpers
(`writeServiceError`, `writeAuthzError`, `writeCoreServiceErr`) onto
`apiresp.WriteError` (Phase 1), but explicitly left the remaining literal sites in
place as out of scope. This plan is a mechanical-but-careful refactor of that
already-established pattern — not new architecture — routing each remaining site
through the single classification point instead of re-stating status+code inline.
The followup's original "~28 sites" estimate was stale by the time this plan was
scoped; the verified count against source was 96 sites across the same seven files.

## What shipped

Single phase, four parallel-eligible tasks split by handler file (no two tasks
touched the same file). All four landed:

1. **`migrate-apps-handler`** (`api/internal/handlers/apps.go`) — collapsed all 27
   literal `server.Error`/`server.ErrorWithDetails` sites onto `apiresp.WriteError`.
   Zero remaining sites; 0 carve-outs. Build, vet, gofmt, unit tests all clean.
   Merge: `7ec28d0` (commit `7d0c558`).
2. **`migrate-oidc-handlers`** (`oidc_providers.go` + `oidc_config.go`) — collapsed
   33 sites (`oidc_providers.go`: 20, `oidc_config.go`: 13). One carve-out retained
   at `oidc_providers.go:222` (409 conflict), preserved as a literal call with a
   `ZVum`-referencing justification comment. Build, vet, gofmt, unit tests all
   clean. Merge: `7bf624c` (commit `b29c0b9`).
3. **`migrate-identities-handler`** (`identities.go`) — migrated all 23 literal
   sites onto `apiresp.WriteError`. Three carve-outs retained (lines 310, 389, 391),
   each with a `ZVum`-referencing justification comment;
   `writeStepUpRequired`/`writeLastIdentityError` (the flat-envelope sites deferred
   by followup `eiF8`) were left untouched. Build, vet, gofmt, unit tests all clean.
   Merge: `9deb4ee` (commit `03e1470`).
4. **`migrate-self-assume-account-handlers`** (`self.go` + `assume.go` +
   `user_accounts.go`) — collapsed all 13 literal sites (`self.go`: 6, `assume.go`:
   3, `user_accounts.go`: 4). Shared helpers `writeServiceError`/
   `writeCoreServiceErr` left untouched. Zero remaining sites; 0 carve-outs. Build,
   vet, gofmt, unit tests all clean. Merge: `29e1093` (commit `d3f6bd4`).

Because all four task docs carried `architectural_impact: true`, this phase went
through a **full phase-review gate** (decomposed, independently-dispatched review
lenses) rather than an inline self-review. Results: the **correctness**,
**efficiency**, and **security** lenses all returned "no findings." The
**architecture-conformance** lens returned "approve," with one minor-severity and
one suggestion-severity finding, both non-blocking and both recorded as new
follow-up items (`Wkx3` and `6vir` — see below) rather than requiring rework.

## Key decisions

- **Message-genericization behavior change is intentional and safe.**
  `apiresp.WriteError` sets the top-level `error.message` from apiresp's fixed,
  generic, per-code `publicMessage` (e.g. `invalid_input` → "one or more fields are
  invalid"), with the site's original bespoke text now only logged server-side, not
  returned to the client. `error.code`, HTTP `status`, and `error.details[]` — the
  fields the GUI's `ApiRequestError` actually branches on — are unchanged at every
  migrated site. Verified safe because the GUI keys off `code`, not message text,
  and handler tests assert on status/code/details rather than top-level message
  strings.
- **Four sites are documented carve-outs, not migrated.** `apiresp` exposes only
  `InvalidInput(...)` as a public detail-carrying constructor (no public
  conflict/not_found/unauthenticated equivalent — followup `ZVum`). The four sites
  that need a non-`invalid_input` code with a `details[]` entry or an actionable
  message stay as literal `server.Error`/`server.ErrorWithDetails` calls, each with
  a justification comment referencing `ZVum`: `oidc_providers.go:222` (conflict),
  and `identities.go:310`, `:389`, `:391` (not_found+detail and
  unauthenticated+detail). This broadens `ZVum`'s scope from conflict-only to also
  cover the not_found+details and unauthenticated+details cases.
- **Redundant preceding `slog.ErrorContext` calls at 500 sites were removed.**
  `apiresp.WriteError` logs all 5xx responses server-side with request context, so
  a separate log call immediately before it is redundant; any distinct structured
  context (op label, etc.) that mattered was folded into the error passed to
  `WriteError` instead of being lost.
- **Followup `8iRl` is the followup this plan closes.** Per the plan's own
  framing, `8iRl` is superseded by this plan's completion (it should be considered
  closed now that all in-scope sites are migrated) — see the Follow-up items
  section below for a note on its current state in `followups.yaml`.

## Follow-up items

- **`Wkx3` — No lint guardrail against `server.Error` drift.** This phase collapsed
  92 of 96 literal sites onto `apiresp.WriteError`, but nothing (lint rule,
  `forbidigo`/staticcheck config, CI check) prevents a newly-added handler from
  reintroducing a literal `server.Error`/`server.ErrorWithDetails` call outside the
  four documented carve-outs. The invariant is enforced by convention and this
  one-time sweep, not tooling, and can silently erode — including in
  `handlers/auth/*.go` (followup `biPE`, ~10+ of its own literal sites) once that
  migration happens. Recommends a `forbidigo` pattern banning
  `server.Error(`/`server.ErrorWithDetails(` outside an explicit allowlist of the
  four carve-out lines, wired into `make lint`.
- **`6vir` — Carve-out grep verification is comment-fragile.** Task 003's
  completion check (`grep -n "server\.Error\|server\.ErrorWithDetails" identities.go`,
  expecting exactly 3 matches) required wording the carve-out justification
  comments to avoid literally containing the substring `server.ErrorWithDetails` so
  the comment text wouldn't inflate the grep count — a brittle, comment-text-based
  verification rather than a structural one. A future edit to a carve-out comment
  could reintroduce the substring and desync the count from reality. Recommends a
  call-site-anchored grep (e.g. matching only `server\.Error(With)?Details?\(w,` at
  line-start) if this carve-out pattern is reused for a future site-migration
  batch.
- **`Z6Vf` — Minor `id` field addition in `oidc_providers.go` (self-flagged).** Two
  rebuild-failure sites in `oidc_providers.go` now carry an `id` field in the
  wrapped error even though the original `slog.ErrorContext` call at that exact
  site didn't log one — purely additive diagnostic context within the same line
  being edited, not a scope change. Flagged by the implementing task agent itself.
- **`ZVum` — apiresp needs a public `Conflict()`/detail-carrying constructor
  beyond `InvalidInput`.** Originally scoped to the conflict case
  (`writeServiceError`'s `svc.ErrEmailTaken` branch), broadened by this plan to
  also cover not_found+details and unauthenticated+details, since all four of this
  plan's carve-out sites hit the same gap. Cross-repo (`mod-core/api/apiresp`), not
  fixable from `mod-users`.
- **`8iRl` — the followup this plan was chartered to close.** As of this summary's
  authoring, `8iRl` is still present in `plan/followups.yaml` (tag
  `go-vocab-migration`) rather than removed; only a same-tag sibling entry
  (`WGZV`, an unrelated message-casing note) was removed in an earlier cleanup
  pass. The dispatching context for this closeout described `8iRl` as "already
  removed," which does not match the current file state — flagged here so the
  manager can remove/close `8iRl` explicitly now that this plan has completed its
  scope.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — Centralize server.Error Sites

- [x] [001-migrate-apps-handler.md](./phase-01-centralize-server-error/001-migrate-apps-handler.md) — tier `sonnet-high` · branch `phase-01-task-01-migrate-apps-handler` · commit `7d0c558` · merge `7ec28d0`
- [x] [002-migrate-oidc-handlers.md](./phase-01-centralize-server-error/002-migrate-oidc-handlers.md) — tier `sonnet-high` · branch `phase-01-task-02-migrate-oidc-handlers` · commit `b29c0b9` · merge `7bf624c`
- [x] [003-migrate-identities-handler.md](./phase-01-centralize-server-error/003-migrate-identities-handler.md) — tier `sonnet-high` · branch `phase-01-task-03-migrate-identities-handler` · commit `03e1470` · merge `9deb4ee`
- [x] [004-migrate-self-assume-account-handlers.md](./phase-01-centralize-server-error/004-migrate-self-assume-account-handlers.md) — tier `sonnet-high` · branch `phase-01-task-04-migrate-self-assume-account-ha` · commit `d3f6bd4` · merge `29e1093`
