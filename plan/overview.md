# Users Apiresp Migration

## Purpose and scope

This is **Wave 1** of the multi-phase API response & error standardization effort, scoped to
**`mod-users` only** (this repo). It migrates `mod-users`' HTTP `api/` surface and its `gui/` client
onto the canonical shared contract defined in
[`docs/mf-standards/architecture/api-response-design.md`](../docs/mf-standards/architecture/api-response-design.md)
(the required architecture reference; read it in full before executing any task).

`mod-users` is unusually close to the target already — it originated the nested-envelope
`{error:{code,message[,details]}}` shape the design doc adopts — but it has the largest ad-hoc-code
migration surface in the ecosystem. This plan:

- Promotes `mod-users`' local `internal/authz` sentinels (`ErrUnauthenticated`, `ErrForbidden`) onto
  the shared `apiresp` sentinels so other modules can `errors.Is` against them (killing the
  string-matching shims documented in the design doc).
- Routes error writing through the shared `apiresp.WriteError` implementation (single classification
  + envelope construction), rather than `mod-users`' parallel `respond.go`.
- Migrates every ad-hoc top-level `error.code` string to the reserved core vocabulary, moving
  finer-grained distinctions into namespaced `details[].code` (`users.<rule>`) per the design doc's
  mapping table.
- Reconciles the `gui/` client types and error widget with the promoted `@moduleforge/core-gui`
  primitives.

### Hard precondition — Wave 0 must merge first

**Every task in this plan has a hard dependency on Wave 0** (`mod-core` plan slug
`apiresp-error-widgets`, a sibling, separately-authored plan that is **not yet merged**). Wave 0
delivers:

- **Go:** the shared package `github.com/moduleforge/core-api/apiresp` — canonical sentinels
  (`ErrUnauthenticated`, `ErrForbidden`, `ErrNotFound`, `ErrInvalidInput`, `ErrConflict`),
  `WriteJSON`, `WriteError(w, r, err)`, and `InvalidInput(...)`.
- **GUI:** the `@moduleforge/core-gui` error/toast toolkit — wire types
  (`FieldError`/`ApiError`/`ApiErrorResponse`), `ApiRequestError`, a shared typed `request()` client
  helper, `<FieldError>`/`<ErrorBanner>` widgets, a Toast provider/`useToast`, and a `useApiError`
  hook.

**Implementation of this plan cannot start until Wave 0 lands.** The plan is authored now (the design
doc fully specifies both API surfaces), but the manager must gate dispatch of Phase 1 on Wave 0's
merge. Every task document restates this precondition in its `## Assumptions`.

### Explicitly out of scope

- Any change to any **other** module repo (`mod-core`, `mod-tasks`, etc.).
- The design doc itself.
- `model/` — this work spans `api/` (Go) and `gui/` (TypeScript) only.
- The 5 flat-envelope error sites discovered during research (see
  [Open scope question](#open-scope-question-flat-envelope-sites) below) — deferred pending a manager
  decision.

## Current status

Plan authored; **not yet started.** Phase 1 (`go-apiresp-foundation`) begins first, but only after
Wave 0 (`mod-core` `apiresp-error-widgets`) has merged and `github.com/moduleforge/core-api/apiresp`
resolves in `api/go.mod` (currently `replace ... => ../../mod-core/api`).

Two conditions block returning this plan as fully registered/complete:

1. **flow-mcp tooling unavailable.** The `mcp__flow-mcp__todo_*` tools returned
   "No such tool available" during authoring, so the phases/tasks below could not be registered into
   `plan/TODO.yaml` (the only sanctioned mechanism; hand-editing is forbidden). The exact registration
   calls are enumerated in [Overview](#overview) and must be run once the tooling is available.
2. **Open scope question** on the flat-envelope sites (below) — a manager/user decision.

## Overview

The plan is **four phases**. Phase order encodes the Go internal dependency (foundation before
vocabulary migration); the GUI phase depends only on Wave 0 and is independent of the Go phases (it
may be dispatched concurrently with them if the manager wishes). All phases depend on Wave 0.

### Phase 1 — `go-apiresp-foundation` (1 task; sequential prerequisite for Phase 2)

Adopt the shared `apiresp` sentinels and error writer. This is the foundation every downstream Go
task builds on.

- **001 — `adopt-apiresp-sentinels-and-writer`** (tier `sonnet-high`): Wire the `apiresp` dependency;
  promote `internal/authz`'s `ErrUnauthenticated`/`ErrForbidden` onto `apiresp`'s sentinels (alias or
  replace); collapse the three sentinel-classifying helpers
  (`handlers/errors.go` `writeAuthzError`, `handlers/user_accounts.go` `writeServiceError`,
  `handlers/self.go` `writeCoreServiceErr`) onto `apiresp.WriteError`; decide `server/respond.go`'s
  fate (thin wrapper delegating to `apiresp`, or migrate callers — but `apiresp` must own the single
  classification/envelope implementation). Includes the `svc.ErrEmailTaken` → `conflict` +
  `details[].code: users.email_taken` and `svc.ErrInvalidInput` → `invalid_input` + per-field detail
  mappings (design doc worked example). Update the affected sentinel-path test assertions.

### Phase 2 — `go-vocab-migration` (2 parallel tasks; depends on Phase 1)

Migrate every ad-hoc top-level `error.code` literal in `handlers/*.go` (the nested-envelope
`server.Error` sites) to the reserved vocabulary, splitting distinctions into `details[].code`. The
two tasks touch **disjoint files** and are **parallel-eligible**.

- **001 — `migrate-apps-oidc-handlers`** (tier `sonnet-high`): `apps.go`, `oidc_providers.go`,
  `oidc_config.go`. Homogeneous: `bad_request` → `invalid_input`; `validation_error` →
  `invalid_input` + per-field `users.<rule>` detail. ~20 sites. Update the matching `_test.go`
  assertions.
- **002 — `migrate-identity-account-self-handlers`** (tier `sonnet-high`): `identities.go`,
  `user_accounts.go` (literal sites only — its `writeServiceError` is Phase 1), `self.go`,
  `assume.go`. Includes the interesting cases: `unauthorized` → `unauthenticated`; `bad_credentials`
  → `unauthenticated` + `users.bad_credentials`; `identity_not_found` → **`not_found`** +
  `users.identity_not_found` (decision recorded in the task doc: the identity-unlink lookup is
  self-scoped and not `EntityResolver`-mediated, so masking does not apply and 404 is correct).
  ~17 sites. Update the matching `_test.go` assertions.

### Phase 3 — `gui-core-gui-adoption` (2 parallel tasks; depends only on Wave 0)

Reconcile the `gui/` client and error widget onto `@moduleforge/core-gui`. Both tasks require the
yalc link (per AGENTS.md / `.claude/CLAUDE.md`). They touch **disjoint files** and are
**parallel-eligible**. This phase has **no dependency on Phases 1–2**.

- **001 — `reconcile-api-client-types`** (tier `sonnet-high`): `gui/src/lib/api.ts`. Reconcile the
  local `ApiError`/`ApiErrorResponse`/`ApiRequestError` with `@moduleforge/core-gui`'s canonical
  versions (import/re-export preferred; at minimum add `details?: FieldError[]`). Update the 401
  synthesized code `'unauthorized'` → `'unauthenticated'` while **preserving** the existing
  throw-always behavior (decision recorded in the task doc — the `skipAuthRedirect` opt-out only skips
  the redirect side-effect; the throw is intentional and relied on by the OAuth-return caller).
- **002 — `adopt-error-banner-widget`** (tier `sonnet-med`): `gui/src/components/error-message.tsx`.
  Switch `ErrorMessage` to consume `@moduleforge/core-gui`'s `<ErrorBanner>` (or retire it in favor
  of direct `<ErrorBanner>` at call sites); the direct `Alert`/`AlertDescription` import must be
  removed.

### Phase 4 — `doc-updates` (1 task; depends on Phases 1–2 landing)

The plan changes the public HTTP error contract (top-level `error.code` values, and the new optional
`details` array), so the architectural-implications check applies.

- **001 — `update-architecture-docs`** (tier `sonnet-high`, role `architect-backend`): Update
  `api/openapi.yaml` (the `Error` schema — change the `example: bad_request` to `invalid_input`, add
  the optional `details` `FieldError[]` array, document the reserved code vocabulary); review
  `docs/architecture.md` and `docs/mod-users-spec.md` for error-contract descriptions and reconcile
  them. Runs after the implementation phases land.

### Manager-run cross-repo validation (not a task in this worktree)

`mod-users` is wired into both `app-mfdemo` and `app-mftodo` (both exist as sibling repos). As a final
validation, both apps must be regenerated via `mfgen generate` and confirmed to build. This is
**cross-repo** and cannot be performed from within this repo's task worktree, so it is a
**manager-run validation step** after all task branches merge — not a task registered in this plan.

### flow-mcp registration calls (run once tooling is available)

The phases/tasks above must be registered via the MCP tools (never by hand-editing `plan/TODO.yaml`).
Registration sequence:

1. `todo_add_phase` slug `go-apiresp-foundation`, title `Go Apiresp Foundation`.
2. `todo_add_item` phase 1, slug `adopt-apiresp-sentinels-and-writer`, title
   `Adopt Apiresp Sentinels And Writer`, tier `sonnet-high`.
3. `todo_add_phase` slug `go-vocab-migration`, title `Go Vocab Migration`.
4. `todo_add_item` phase 2, slug `migrate-apps-oidc-handlers`, title `Migrate Apps Oidc Handlers`,
   tier `sonnet-high`.
5. `todo_add_item` phase 2, slug `migrate-identity-account-self-handlers`, title
   `Migrate Identity Account Self Handlers`, tier `sonnet-high`.
6. `todo_add_phase` slug `gui-core-gui-adoption`, title `Gui Core Gui Adoption`.
7. `todo_add_item` phase 3, slug `reconcile-api-client-types`, title `Reconcile Api Client Types`,
   tier `sonnet-high`.
8. `todo_add_item` phase 3, slug `adopt-error-banner-widget`, title `Adopt Error Banner Widget`,
   tier `sonnet-med`.
9. `todo_add_phase` slug `doc-updates`, title `Documentation Updates`.
10. `todo_add_item` phase 4, slug `update-architecture-docs`, title `Update Architecture Docs`,
    tier `sonnet-high`.

The authored task-doc paths already match the default derivation (`phase-NN-<slug>/NNN-<task-slug>.md`),
so registering with the default `path` derivation will line up with the files on disk.

## Open scope question — flat-envelope sites

Research surfaced **5 error sites the manager's `server.Error`-based scoping did not enumerate**,
because they are written via `server.JSON` directly with a **flat** envelope (`error` is a bare
*string*, not the nested `{code,message}` object the design doc mandates):

| File | Line | Code | Status | Extra fields |
|---|---|---|---|---|
| `api/internal/auth/require_verified.go` | 24 | `internal_error` (flat) | 5xx | — |
| `api/internal/auth/require_verified.go` | 34 | `email_unverified` | (auth gate) | — |
| `api/internal/auth/require_confirmed.go` | 36 | `oidc_not_confirmed` | (auth gate) | — |
| `api/internal/handlers/identities.go` | 626 | `step_up_required` | 409 | `challenge_path` |
| `api/internal/handlers/identities.go` | 646 | `last_identity` | 409 | `message` |

These are the **most** non-conformant sites in the module (flat `error: string`, and codes outside
both the reserved vocabulary and the manager's enumerated set). The manager's research method (grepping
`server.Error(...)` code arguments) systematically missed them, and two of them live in
`api/internal/auth/` — outside the manager's stated `api/internal/handlers/*.go` file scope.

**They are deferred out of this plan, pending a manager/user decision,** because migrating them is not
a clean same-shape code-value change:

- `writeStepUpRequired`'s own comment states the wire format is stable and *"the GUI keys off the
  `error` field value"* — i.e. an app-level consumer reads `error` as a **string**. Changing `error`
  from a string to an object is a **breaking structural change to cross-repo app consumers**
  (`app-mfdemo`/`app-mftodo`), and cross-repo changes are explicitly out of scope for this plan.
- `step_up_required` carries an extra `challenge_path` field that has no home in the nested envelope
  without a design decision.
- The `internal/auth/` sites drive auth-flow redirects (`email_unverified`, `oidc_not_confirmed`).

**Recommendation:** treat this as a coordinated cross-repo follow-up (a later wave that migrates these
flat sites *together with* their `app-mfdemo`/`app-mftodo` consumers), not as in-scope here. The
manager should confirm whether to (a) fold them into this plan as an added task (expanding scope to
`internal/auth/` + structural flat→nested + `challenge_path` handling + app coordination), or
(b) record them as a follow-up. This plan is authored assuming (b).

## References

- [`docs/mf-standards/architecture/api-response-design.md`](../docs/mf-standards/architecture/api-response-design.md)
  — the required architecture reference; source of truth for the target envelope, vocabulary, sentinel
  mapping, `apiresp` package surface, and GUI error-data contract.
- [`AGENTS.md`](../AGENTS.md) / [`.claude/CLAUDE.md`](../.claude/CLAUDE.md) — build/test commands and
  the yalc-link gotcha for `gui/` work.
