# Plan Summary: users-action-required-migration

## What was planned and why

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

## What shipped

### Phase 01 — Go Backend Action-Required Migration

1. **Add Action-Code Registry** (`001-add-action-code-registry.md`, tier `sonnet-low`) — Added api/internal/useraction package declaring mod-users' three registered apiresp.ActionCode values (EmailUnverified 403, StepUpRequired 409, OIDCNotConfirmed 503) exactly as specified, with package doc comment and table-driven test. Self-contained additive change, no other package imports useraction yet. All five validation checks pass.
   Commit `4e6528b`, merged at `3a1908246b36f9924b9e0d6eba3146c0a6c2efb0`.

2. **Fold In ZVum Email Taken Conflict** (`002-fold-in-zvum-email-taken-conflict.md`, tier `sonnet-med`) — Closed out followup ZVum: svc.ErrEmailTaken now carries its users.email_taken field detail directly via apiresp.Conflict(...), and writeServiceError collapsed to a plain apiresp.WriteError(w,r,err) pass-through with no special-cased branch. Both doc comments rewritten. Added TestErrEmailTaken_ConflictDetail asserting errors.Is(..., apiresp.ErrConflict) and exact field detail. Wire output provably unchanged (build/vet/test/gofmt/grep all pass).
   Commit `23347ed`, merged at `a2d54d00e79fc10e0b43632b40f5e7b5451b45ff`.

3. **Migrate RequireVerifiedEmail Middleware To Apiresp** (`003-migrate-require-verified-middleware.md`, tier `sonnet-med`) — Migrated both response branches of RequireVerifiedEmail off the bespoke server.JSON flat envelope. Missing-context branch now calls apiresp.WriteError with an untyped errors.New(...) programmer error (internal_error/500 default). Email-unverified branch now calls apiresp.WriteActionRequired with useraction.EmailUnverified, existing message text, and resolved /verify-email navigation path, data: nil. Updated doc comments. Removed unused server import, added errors/apiresp/useraction. Rewrote all four tests. Confirmed Recoverer middleware satisfies WriteActionRequired's panic-recovery precondition. Full validation suite green, no regressions.
   Commit `19c171b`, merged at `a72a5451104b831d500ad8a94624e2f5b6a381df`.

4. **Migrate RequireOIDCConfirmed Middleware To Apiresp** (`004-migrate-require-oidc-confirmed-middleware.md`, tier `sonnet-med`) — Migrated RequireOIDCConfirmed's not-confirmed branch off bespoke server.JSON envelope onto apiresp.WriteActionRequired using useraction.OIDCNotConfirmed, preserving 503 status (verbatim, never remapped), /oidc-config path, and boot-state value now nested under action.data.state. Removed dead server import, rewrote doc comment. Updated test to decode/assert new shape plus verbatim-503-never-remapped invariant comment. All validation passed cleanly. No new mfgen/stale-interface break beyond the one task 001 already resolved.
   Commit `b34db4b`, merged at `1eb1b2a32e7eee67061a31b0ede81e4aaa98b8d0`.

5. **Migrate Identities Step-Up And Last-Identity Responses** (`005-migrate-identities-step-up-and-last-identity.md`, tier `sonnet-med`) — Migrated identities.go's two flat-envelope helpers to apiresp mechanisms: writeStepUpRequired now calls apiresp.WriteActionRequired with useraction.StepUpRequired and /step-up path; writeLastIdentityError now calls apiresp.WriteError(apiresp.Conflict(...)) with Code: users.last_identity. Both helpers gained r *http.Request param threaded through six call sites. Updated tests to assert new nested action/error shapes. Swept for stray retired-field references - none found beyond migrated ones. All validation passed cleanly.
   Commit `be2ac56`, merged at `69a08305e3dae755b67c7a2f154b286aa6a4dc5d`.

### Phase 02 — GUI Action-Required Client And Wiring

1. **Fix Api Action Discrimination** (`001-fix-api-action-discrimination.md`, tier `sonnet-med`) — Fixed request()'s non-2xx handling in gui/src/lib/api.ts to parse the response body once and check for a top-level action member before classifying as error - a 403/409/503 body carrying action now short-circuits to a new ApiActionRequiredError (code/message/path/status/data) and never reaches the error-body branch. Added local ApiAction/ApiActionResponse wire types (flagged as future core-gui promotion candidates) and a defense-in-depth same-origin-relative guard on action.path. Fixed latent bug where flat-string error was assumed to always be an object. request<T>()'s public signature and all existing client methods unchanged. Discovered gui/ now has a working test runner (contradicting follow-up KXNZ); added 11 unit tests. Full suite (20 tests) plus typecheck/lint/build all green.
   Commit `e2c7d9f`, merged at `33db32dff57df86edfa7a11d592b060b82a9f81a`.

2. **Wire Auth Context Action Navigation** (`002-wire-auth-context-action-navigation.md`, tier `sonnet-med`) — Wired the three reachable api.self.get() call sites in gui/src/lib/auth-context.tsx (mount effect, refreshUser, completeExternalLogin) to catch ApiActionRequiredError ahead of other error handling and navigate via onNavigate, per the design doc's action-required navigation distinction. Each site preserves documented existing behavior for non-action-required errors. Added 8 new unit tests; full suite 28/28 passing. Typecheck/lint/build all green.
   Commit `33d0189`, merged at `132551b33a0b08a4df0066815420076fede5e43c`.

### Phase 03 — Documentation Updates

1. **Document Action-Required Envelope In Spec And Architecture** (`001-document-action-required-in-spec-and-architecture.md`, tier `sonnet-high`) — Documented the action-required response envelope in docs/mod-users-spec.md (new General features bullet naming the three action codes/statuses/paths, cross-referencing api-response-design.md) and updated use case 9's Outcome paragraph for step_up_required (now action-required) and last_identity (now a conflict detail). Added a proportional Action-required responses note to docs/architecture.md's API layer section. Every code/status/path cross-checked directly against shipped Phase 1 Go source and confirmed exact. Neither doc had ever pinned retired flat field names, so no removal needed. api/openapi.yaml left untouched per sibling task's disjoint scope.
   Commit `eb86f98`, merged at `ae7f335d50fe3bc1d6355fcd3cc5b4e90dc15e2d`.

2. **Document Action-Required Envelope In OpenAPI** (`002-document-action-required-in-openapi.md`, tier `sonnet-high`) — Added the Action action-required envelope schema to api/openapi.yaml (mirroring Error/FieldError, enumerating all three registered mod-users action codes, documenting the oidc_not_confirmed-only data.state shape against real BootState values). Wired 403 email_unverified and 503 oidc_not_confirmed onto every already-documented endpoint the shipped middleware chain actually gates (traced via main.go and moduleforge.module.yaml), broader than the task doc's single named example since the code confirmed apply-broadly was literal. Found and corrected an inaccuracy in the task doc's own example. Confirmed retire-flat-bodies requirement was already vacuously satisfied (flat fields never existed in openapi.yaml).
   Commit `77bf5d0`, merged at `3ce4c5cbb96afe9e57eb739a650db4843542af8c`.

## Key decisions

_No `## Why this shape` section is recorded in `plan/overview.md`, so this plan's cross-task rationale was never written down. Per-task outcomes are under "What shipped" above._

## Follow-up items

- **`bdZJ`** — **Task doc's own Assumptions (three ../ up) and** — Task doc's own Assumptions (three ../ up) and plan notes doc action-path-values-and-decisions.md (decision D10, followup UaNK, five ../ up for doubly-nested task worktrees) both mismatch this worktree's empirically-verified reality (four ../ up). Worth reconciling building-common.md guidance and/or plan notes so tasks 003/004/005 in this phase don't rediscover the same mismatch independently.

- **`IuaZ`** — **api/cmd/server/main.go fieldcrypto.NewFromEnv** — api/cmd/server/main.go fieldcrypto.NewFromEnv -> NewFromEnvOrGenerate fix scoped to the one call site needed to unblock this task's build; no broader audit of main.go or other mfgen-wired constructors performed - worth a dedicated pass if other call sites carry similarly stale mfgen wiring.

- **`0bqZ`** — **Build-environment observation: this worktree** — Build-environment observation: this worktree needed no go.work workaround at all — plain make build.api / go build / go vet / go test all succeeded directly using api/go.mod's existing local-path replace directives. This differs from both task 001's three-../ assumption and the dispatch note's corrected four-../ recipe (and the plan notes' five-../ figure). The sibling docs-mf-standards checkout's building-common.md actually describes a different, apparently newer mechanism (scripts/link-siblings.sh + make preflight) superseding the go.work recipe, which mod-users does not yet carry (no scripts/link-siblings.sh present) despite being listed as a repo that should carry it. Not blocking — build/test were green — but worth manager awareness since the go.work-workaround guidance for tasks 004/005 may already be stale.

- **`BqdP`** — **The task doc's Assumptions (worktree-local go** — The task doc's Assumptions (worktree-local go.work recipe) and the dispatcher's build-environment heads-up (three-vs-four ../ up) both turned out not to apply to this worktree — no go.work was needed at all, since api/go.mod's existing replace directives already resolved against pre-existing sibling symlinks. Recorded in task doc Status; manager may want to reconcile this with the plan-wide heads-up for other tasks in this phase.

- **`QhoW`** — **fieldcrypto key-provisioning posture shift** — [phase-01-go-action-required-migration architecture-conformance gate, arch-review-002] api/cmd/server/main.go's field-cipher init call was forced from fieldcrypto.NewFromEnv() to fieldcrypto.NewFromEnvOrGenerate(ctx, coredb.New(pool)) in task 001, because NewFromEnv no longer exists in the sibling mod-core/api/fieldcrypto package (confirmed: only NewFromKey and the moduleforge.module.yaml-pinned NewFromEnvOrGenerate remain) — a forced adaptation to an upstream interface removal, not a discretionary choice by this plan. The stale doc comment describing the old fail-fast-on-missing-key behavior has been corrected (see commit ead0baa on plan/users-action-required-migration). Worth separate awareness/review: the security posture for SSN/EIN field-encryption key provisioning has shifted repo-wide from fail-fast-if-missing to auto-generate-if-absent, as a side effect of mod-core's own interface consolidation (not something mod-users controls or chose). Not blocking this plan; flagging for whoever owns key-management/security policy to confirm this is the intended posture.

- **`ojPJ`** — **docs/mf-standards submodule was not initializ** — docs/mf-standards submodule was not initialized in this task worktree at dispatch time (unlike the plan worktree it was cut from) - this task agent initialized it read-only to reach the referenced contract doc. Other task worktrees cut from this plan that reference docs/mf-standards docs may need the same step.

## Final Task State

# TODO

## Purpose and scope

Tracking document for the active plan.

## Tasks

### Phase 01 — Go Backend Action-Required Migration

- [x] [001-add-action-code-registry.md](./phase-01-go-action-required-migration/001-add-action-code-registry.md) — tier `sonnet-low` · branch `plan/users-action-required-migration-01-001` · commit `4e6528b` · merge `3a1908246b36f9924b9e0d6eba3146c0a6c2efb0`
- [x] [002-fold-in-zvum-email-taken-conflict.md](./phase-01-go-action-required-migration/002-fold-in-zvum-email-taken-conflict.md) — tier `sonnet-med` · branch `plan/users-action-required-migration-01-002` · commit `23347ed` · merge `a2d54d00e79fc10e0b43632b40f5e7b5451b45ff`
- [x] [003-migrate-require-verified-middleware.md](./phase-01-go-action-required-migration/003-migrate-require-verified-middleware.md) — tier `sonnet-med` · branch `plan/users-action-required-migration-01-003` · commit `19c171b` · merge `a72a5451104b831d500ad8a94624e2f5b6a381df`
- [x] [004-migrate-require-oidc-confirmed-middleware.md](./phase-01-go-action-required-migration/004-migrate-require-oidc-confirmed-middleware.md) — tier `sonnet-med` · branch `plan/users-action-required-migration-01-004` · commit `b34db4b` · merge `1eb1b2a32e7eee67061a31b0ede81e4aaa98b8d0`
- [x] [005-migrate-identities-step-up-and-last-identity.md](./phase-01-go-action-required-migration/005-migrate-identities-step-up-and-last-identity.md) — tier `sonnet-med` · branch `plan/users-action-required-migration-01-005` · commit `be2ac56` · merge `69a08305e3dae755b67c7a2f154b286aa6a4dc5d`

### Phase 02 — GUI Action-Required Client And Wiring

- [x] [001-fix-api-action-discrimination.md](./phase-02-gui-action-required-handling/001-fix-api-action-discrimination.md) — tier `sonnet-med` · branch `plan/users-action-required-migration-02-001` · commit `e2c7d9f` · merge `33db32dff57df86edfa7a11d592b060b82a9f81a`
- [x] [002-wire-auth-context-action-navigation.md](./phase-02-gui-action-required-handling/002-wire-auth-context-action-navigation.md) — tier `sonnet-med` · branch `plan/users-action-required-migration-02-002` · commit `33d0189` · merge `132551b33a0b08a4df0066815420076fede5e43c`

### Phase 03 — Documentation Updates

- [x] [001-document-action-required-in-spec-and-architecture.md](./phase-03-doc-updates/001-document-action-required-in-spec-and-architecture.md) — tier `sonnet-high` · branch `plan/users-action-required-migration-03-001` · commit `eb86f98` · merge `ae7f335d50fe3bc1d6355fcd3cc5b4e90dc15e2d`
- [x] [002-document-action-required-in-openapi.md](./phase-03-doc-updates/002-document-action-required-in-openapi.md) — tier `sonnet-high` · branch `plan/users-action-required-migration-03-002` · commit `77bf5d0` · merge `3ce4c5cbb96afe9e57eb739a650db4843542af8c`
