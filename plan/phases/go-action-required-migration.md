# Phase — Go Backend Action-Required Migration

## Goals

Migrate mod-users' five deferred flat-envelope backend sites onto the correct `apiresp` mechanism
each (per the design doc's deferred-sites table), and fold in followup `ZVum`, so the `api/` Go
surface stops emitting bespoke `server.JSON` flat bodies at these sites and conforms to the finalized
response contract. This phase comes first in intent (it is the substantive contract change) but is
technically parallel-eligible with the GUI phase, since the GUI depends on the finalized codes from
the design doc rather than on this phase's build output.

Scope of work:

- Introduce a small mod-users-owned registered `ActionCode` registry (the three codes with their
  bound statuses: `users.email_unverified`/403, `users.step_up_required`/409,
  `users.oidc_not_confirmed`/503), shared by the `auth` and `handlers` packages (decision D4).
- `require_verified.go` internal-error branch → `apiresp.WriteError` with an untyped programmer error
  → reserved-core `internal_error` 500 default (D6).
- `identities.go` `writeLastIdentityError` → `apiresp.WriteError(apiresp.Conflict(FieldError{
  Code:"users.last_identity", Message:…}))` → 409 `conflict` + message-bound detail (D7).
- `require_verified.go` email-unverified branch → `apiresp.WriteActionRequired`,
  `users.email_unverified` (403), `path = /verify-email` (Answer, Reading A).
- `identities.go` `writeStepUpRequired` → `apiresp.WriteActionRequired`, `users.step_up_required`
  (409), `path = /step-up` (Answer, Reading A).
- `require_confirmed.go` OIDC-not-confirmed branch → `apiresp.WriteActionRequired`,
  `users.oidc_not_confirmed` (503, never remapped), `path = /oidc-config` (D2),
  `data = {"state": string(state)}` (D3).
- `ZVum` fold-in: redefine `svc.ErrEmailTaken` to carry the `users.email_taken` detail via
  `apiresp.Conflict(...)` and collapse `writeServiceError` onto `apiresp.WriteError(w, r, err)`,
  removing the duplicated conflict message string (D5).

## Inputs

- The finalized contract: `docs/mf-standards/architecture/api-response-design.md` (action-required
  envelope, action-code vocabulary, reserved-core codes, deferred-sites migration table,
  Go-layer-ownership sketch).
- mod-core `apiresp` symbols, available via the local-path replace (`core-api v0.0.0 =>
  ../../mod-core/api`) and verified present: `WriteActionRequired`, `ActionCode`, `Conflict`,
  `WriteError`, `ErrConflict`, `InvalidInput`, `Envelope`/`ErrorBody`/`FieldError`.
- The five source sites and the `ZVum` site (exact locations in
  [notes/action-path-values-and-decisions.md](../notes/action-path-values-and-decisions.md)).
- Existing `*_test.go` coverage near each site (test conventions to match).
- Build-environment prerequisite: the worktree-local `go.work` recipe from
  `docs/mf-standards/building-common.md` "Building inside a task worktree" — no committed
  `go.mod`/`go.sum` change (D10).
- **Resolved user answer (was blocking):** the `action.path` values are `/verify-email` for
  `users.email_unverified` and `/step-up` for `users.step_up_required` (Reading A — adopt
  GUI-navigation semantics; see the notes' `## Answer`). `/oidc-config` was already resolved (D2).
  Consuming apps must mount `/verify-email` and `/step-up` — a cross-repo item flagged for the manager.

## Outputs

- The `api/` Go surface emits the action-required envelope at the three flow-control sites (via the
  shared `apiresp.WriteActionRequired` writer and the mod-users action-code registry) and the nested
  error envelope at the two reserved-mechanism sites — no remaining bespoke `server.JSON` flat bodies
  at these five sites.
- A reusable mod-users action-code registry other handlers can import.
- `writeServiceError` reduced to `apiresp.WriteError` with no local envelope construction; the
  duplicated `publicMessage("conflict")` string removed.
- Table-driven unit tests asserting each new response's status, top-level member (`action` vs
  `error`), code, path/detail, and — for `oidc_not_confirmed` — `data.state` presence and the
  verbatim-503 (never-remapped) invariant. All pre-existing tests for untouched behavior stay green.
- The finalized `action.code`/status/path values other waves and the consuming apps depend on.
</content>
