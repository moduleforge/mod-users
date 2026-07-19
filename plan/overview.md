# Users Action-Required Migration

## Purpose and scope

This plan is **Wave 1 (final wave)** of a 3-wave, 3-repo cross-repo effort (moduleforge/mod-users
followup `eiF8`, tag `users-apiresp-migration`). Wave -1 (docs-mf-standards) designed the
"action-required" response envelope; Wave 0 (mod-core) implemented it in
`github.com/moduleforge/core-api/apiresp` (`WriteActionRequired`/`ActionCode`/`ActionBody`/
`ActionEnvelope`, plus a public `Conflict(...)` constructor). This wave migrates **mod-users** onto
those mechanisms.

The finalized contract is `docs/mf-standards/architecture/api-response-design.md` (hydrated in this
worktree) — the literal spec this plan conforms to.

Concretely, this plan:

1. Migrates mod-users' **five deferred flat-envelope backend sites** onto the correct `apiresp`
   mechanism each, per the design doc's deferred-sites migration table:
   - `require_verified.go` internal-error branch → reserved-core `internal_error` default via
     `apiresp.WriteError`.
   - `identities.go` `writeLastIdentityError` → `apiresp.WriteError(apiresp.Conflict(...))` (409
     `conflict` + `details[].code: users.last_identity`).
   - `require_verified.go` email-unverified branch → `apiresp.WriteActionRequired` /
     `users.email_unverified` (403).
   - `identities.go` `writeStepUpRequired` → `apiresp.WriteActionRequired` / `users.step_up_required`
     (409).
   - `require_confirmed.go` OIDC-not-confirmed branch → `apiresp.WriteActionRequired` /
     `users.oidc_not_confirmed` (503, never remapped, with `data.state`).
2. Folds in followup **`ZVum`**: collapse `writeServiceError`'s `svc.ErrEmailTaken` branch onto
   `apiresp.WriteError` using the now-available `apiresp.Conflict(...)`, removing the duplicated
   `publicMessage("conflict")` string.
3. Fixes the **GUI client's error/action discrimination bug** in `gui/src/lib/api.ts` and implements
   real action-required handling: local `ApiAction`/`ApiActionResponse` wire types, a dedicated
   `ApiActionRequiredError`, top-level-`action` short-circuit in `request()`, a client-side
   same-origin-relative guard, and navigation wiring at the reachable `api.self.get()` call sites in
   `auth-context.tsx`.

**Out of scope / flagged:** any change to `app-mfdemo`/`app-mftodo` (separate repos, out of reach —
item 5 verification is flagged for the manager); promotion of the new action types to
`@moduleforge/core-gui` (flagged as a future candidate); building new email-verification/step-up UI
screens that do not already exist.

Detailed findings and the resolved design decisions (D1–D10) are in
[action-path values and decisions](./notes/action-path-values-and-decisions.md).

## Current status

**Status: ready for phase-decomposition.** Planning has investigated all five sites, the ZVum
fold-in, the mod-core `apiresp` API surface, the go.mod/build-environment reality, and the GUI call
sites. Two phases are registered (`go-action-required-migration`, `gui-action-required-handling`),
and they are **parallel-eligible** — the GUI phase depends only on the finalized action-code contract
(fully specified by the design doc), not on the Go build output.

**The sole blocking decision is now resolved.** The `action.path` values for `users.email_unverified`
and `users.step_up_required` were the one open question. The user selected **Reading A** (adopt
GUI-navigation semantics per D1): `users.email_unverified` → `/verify-email` and
`users.step_up_required` → `/step-up` (matching the design doc's worked examples); see the
[Answer](./notes/action-path-values-and-decisions.md#answer). `users.oidc_not_confirmed` →
`/oidc-config` was already resolved (D2). Requiring the consuming apps (`app-mfdemo`/`app-mftodo`) to
mount `/verify-email` and `/step-up` is a cross-repo coordination item flagged for the manager, not
built in this plan (those repos are out of this worktree's reach).

With no open questions remaining, both phases are ready for concurrent phase-decomposition (one agent
per phase, authoring the task documents). **After** decomposition, the architectural-implications
check should register a `doc-updates` phase — this migration changes the wire shape of five endpoints
(spec/openapi-documented behavior), so `docs/mod-users-spec.md`, `docs/architecture.md`, and
`api/openapi.yaml` will need review for the new action-required envelope and the two reserved-mechanism
changes. That check is deferred to after decomposition because it must reference the implementation
task-doc paths that the phase-decomposition agents author.

## Overview

### Phase 1 — Go Backend Action-Required Migration (`go-action-required-migration`)

Migrates the five deferred backend sites and folds in `ZVum`, in the `api/` Go sub-project. Introduces
a small mod-users-owned registered `ActionCode` registry (the three codes + bound statuses), adopts
`apiresp.WriteError`/`WriteActionRequired`/`Conflict`, preserves all existing behavior/tests for
untouched paths, and adds table-driven unit tests for the new response bodies (status, top-level
member, code, path, and — for oidc — `data.state` and the verbatim-503 invariant). Build prerequisite:
the worktree-local `go.work` recipe from `building-common.md` (no committed `go.mod`/`go.sum` change;
core-api is a local-path replace). The `action.path` values are now resolved:
`users.email_unverified` → `/verify-email`, `users.step_up_required` → `/step-up` (Answer, Reading A),
and `users.oidc_not_confirmed` → `/oidc-config` (D2).

### Phase 2 — GUI Action-Required Client And Wiring (`gui-action-required-handling`)

In `gui/src/lib/api.ts`: define `ApiAction`/`ApiActionResponse` locally and a dedicated
`ApiActionRequiredError`; fix `request()` so a non-2xx body is inspected for a top-level `action`
member **before** it is classified as an `ApiRequestError`, short-circuiting error handling and
throwing `ApiActionRequiredError` (with a defense-in-depth same-origin-relative guard on `path`). Wire
the reachable `api.self.get()` call sites in `auth-context.tsx` (mount effect, `refreshUser`,
`completeExternalLogin`) to catch `ApiActionRequiredError` and navigate via the injected `onNavigate`.
`users.step_up_required` has no reachable GUI call site today (no identities client methods) — its
handling is limited to the generic transport-layer throw, with actual step-up navigation flagged as a
follow-up. Depends only on the finalized action codes (design doc), so **parallel-eligible with Phase
1**; the `action.path` values it navigates to are now resolved (`/verify-email`, `/step-up`,
`/oidc-config`).

### Anticipated Phase — Documentation Updates (`doc-updates`)

Registered after phase-decomposition, per the architectural-implications check: the migration changes
spec-defined response behavior for five endpoints, so `docs/mod-users-spec.md`, `docs/architecture.md`,
and `api/openapi.yaml` need review for the new action-required envelope and the two reserved-mechanism
changes.
</content>
