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

**Status: refreshed after rebase onto current `main`; architectural-implications check run;
`doc-updates` phase registered. Plan is ready for `execute-implementation-plan`.**

Update (2026-07-20): the plan branch was rebased onto current `main` (was ~101 commits behind); the
rebase was clean apart from `plan/followups.yaml` (kept main's already-v2, more up-to-date copy) and
the `docs/mf-standards` submodule was synced to main's pin (`7f5b6898`, a strict descendant of the
design-doc dependency pin `07ab37a` — the spec is present and current). All seven original task docs
were re-verified against current `main` state in this worktree; the concrete file paths, line numbers,
function/struct names, and referenced `apiresp` symbols still hold (the GUI files are at
`gui/src/lib/api.ts` and `gui/src/lib/auth-context.tsx`; D2's `/oidc-config` route, D9's core-gui
surface, and D10's local-path `replace` are all confirmed). Two refinements were recorded: (a) D10 now
notes the **doubly-nested worktree** build wrinkle (followup `UaNK`) — task worktrees carved from this
plan worktree need five `../` plus per-dep `go work edit -replace @v0.0.0` overrides, not the plain
single-nested `building-common.md` recipe; and (b) **task 002 (`fold-in-zvum-email-taken-conflict`) is
only *partially* satisfied on `main`.** Commit `d91b699` already did the handler-side envelope-mechanism
swap (`writeServiceError` now calls `apiresp.WriteError(apiresp.Conflict(...))`, no `WriteJSON`/
`Envelope` literal, wire output already correct), but the mechanical crux of the ZVum fold-in is **not**
done: `svc.ErrEmailTaken` is still a plain `fmt.Errorf` sentinel carrying no detail, `writeServiceError`
still has the `errors.Is(err, svc.ErrEmailTaken)` special case (not collapsed to a pass-through), and
the required service-level `TestErrEmailTaken_ConflictDetail` does not exist. **Task 002 therefore
stays `done: false`** — a "Pre-implementation state" note in its task doc records exactly what remains.

The architectural-implications check was run and, as anticipated, a **Phase 3 — `doc-updates`** is now
registered with two task documents (see below): the migration changes spec-defined response behavior on
five endpoints, so `docs/mod-users-spec.md`/`docs/architecture.md` (prose) and `api/openapi.yaml`
(schema) need the new action-required envelope and the two reserved-mechanism changes documented.

Planning had investigated all five sites, the ZVum fold-in, the mod-core `apiresp` API surface, the
go.mod/build-environment reality, and the GUI call sites. The sole blocking decision (the
`action.path` values for `users.email_unverified` and `users.step_up_required`) is resolved — the user
selected **Reading A** (adopt GUI-navigation semantics per D1): `users.email_unverified` →
`/verify-email` and `users.step_up_required` → `/step-up` (matching the design doc's worked examples);
see the [Answer](./notes/action-path-values-and-decisions.md#answer). `users.oidc_not_confirmed` →
`/oidc-config` was already resolved (D2). Requiring the consuming apps (`app-mfdemo`/`app-mftodo`) to
mount `/verify-email` and `/step-up` is a cross-repo coordination item flagged for the manager, not
built in this plan (those repos are out of this worktree's reach).

Both phases have been decomposed into registered, committed task documents (7 tasks total, verified
via `todo_list_all`):

- **Phase 1 — `go-action-required-migration`** (5 tasks): `add-action-code-registry` (foundation, no
  deps) → `fold-in-zvum-email-taken-conflict`, `migrate-require-verified-middleware`,
  `migrate-require-oidc-confirmed-middleware`, `migrate-identities-step-up-and-last-identity` (all
  depend on task 1; mutually parallel-eligible once it lands). The decomposing agent marked all five
  task documents `architectural_impact: true` by analogy with the `centralize-server-error` phase's
  precedent (wire-shape changes to five live endpoints plus auth-middleware surface) — **flagged for
  manager confirmation that a full phase-review gate is intended**, not dictated verbatim by the phase
  file.
- **Phase 2 — `gui-action-required-handling`** (2 tasks): `fix-api-action-discrimination` →
  `wire-auth-context-action-navigation` (depends on task 1's new exports).
- **Phase 3 — `doc-updates`** (2 tasks, registered 2026-07-20 after the architectural-implications
  check): `document-action-required-in-spec-and-architecture` (prose: `docs/mod-users-spec.md` +
  `docs/architecture.md`) and `document-action-required-in-openapi` (`api/openapi.yaml` schema +
  responses; the identities/credential `409` OpenAPI coverage is deferred to the pre-existing followup
  `biJk` gap). Both depend on Phase 1 having landed (they document the shipped shapes).

**Next step:** proceed to `execute-implementation-plan`. The `architectural_impact: true` marking on
all five Phase 1 task docs (a full phase-review gate, marked by analogy with the `centralize-server-error`
precedent rather than dictated verbatim by the phase file) remains **flagged for manager confirmation**
— it has not been resolved here, only carried forward.

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

### Phase 3 — Documentation Updates (`doc-updates`)

Registered 2026-07-20 per the architectural-implications check: the migration changes spec-defined
response behavior for five endpoints, so the docs need the new action-required envelope and the two
reserved-mechanism changes documented. Two tasks:

- **`document-action-required-in-spec-and-architecture`** — updates `docs/mod-users-spec.md` (adds the
  action-required response-kind description parallel to the error-envelope bullet; updates use case 9's
  `step_up_required`/`last_identity` response wording) and `docs/architecture.md` (the
  Identities/Credentials row and, where present, the auth-middleware/response-contract notes), and
  retires the old flat field names (`verify_path`/`challenge_path`/`config_path`).
- **`document-action-required-in-openapi`** — adds an `Action` envelope schema to `api/openapi.yaml`
  mirroring the `Error`/`FieldError` convention and wires the middleware-level `403`
  (`users.email_unverified`) and `503` (`users.oidc_not_confirmed`, with `data.state`) responses it can
  express today. The `409` `step_up_required`/`last_identity` OpenAPI responses live on the
  `/v1/self/identities` + `/v1/self/credential/*` surface, which is entirely absent from
  `api/openapi.yaml` (pre-existing followup `biJk`); documenting them is deferred to that gap, with the
  new `Action` schema made ready for when those endpoints are added.

Both depend on Phase 1 having landed (they reference the shipped shapes from tasks 003/004/005).
</content>
