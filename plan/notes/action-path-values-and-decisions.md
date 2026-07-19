# Action-required migration — investigation notes and decisions

## Purpose and scope

Records the research behind the Wave-1 (`users-action-required-migration`) plan: the exact
source sites, the shape of the mod-core `apiresp` symbols now available, the build-environment
reality, and — most importantly — the **one unresolved decision** that blocks authoring the Go
migration task content precisely: the `action.path` values for `users.email_unverified` and
`users.step_up_required`.

## The five deferred backend sites (verified in this worktree)

| Site | Current code | Target mechanism |
|---|---|---|
| `api/internal/auth/require_verified.go:23-26` | `server.JSON(w, 500, {"error":"internal_error","message":"server misconfiguration…"})` | reserved-core `internal_error` default via `apiresp.WriteError` with an untyped programmer error |
| `api/internal/handlers/identities.go:670-675` (`writeLastIdentityError`) | `server.JSON(w, 409, {"error":"last_identity","message":"You can't remove your last sign-in method…"})` | `apiresp.WriteError(w, r, apiresp.Conflict(FieldError{Code:"users.last_identity", Message:…}))` → top-level `conflict` + message-bound detail |
| `api/internal/auth/require_verified.go:30-38` | `server.JSON(w, 403, {"error":"email_unverified","message":…,"verify_path":"/v1/auth/email-code/request"})` | **`apiresp.WriteActionRequired`** — `users.email_unverified` (403). **path value = OPEN QUESTION** |
| `api/internal/handlers/identities.go:650-655` (`writeStepUpRequired`) | `server.JSON(w, 409, {"error":"step_up_required","challenge_path":"/v1/self/credential/step-up"})` | **`apiresp.WriteActionRequired`** — `users.step_up_required` (409). **path value = OPEN QUESTION** |
| `api/internal/auth/require_confirmed.go:35-39` | `server.JSON(w, 503, {"error":"oidc_not_confirmed","config_path":"/oidc-config","state":string(state)})` | **`apiresp.WriteActionRequired`** — `users.oidc_not_confirmed` (503, never remapped), `data={"state":string(state)}`, path = `/oidc-config` (RESOLVED — see below) |

Plus the ZVum fold-in: `api/internal/handlers/user_accounts.go:56-70` (`writeServiceError`) hand-builds
the 409 `users.email_taken` envelope via `apiresp.WriteJSON`/`Envelope`/`ErrorBody`, duplicating
`apiresp`'s own `publicMessage("conflict")` string. `svc.ErrEmailTaken` is defined at
`api/internal/service/user_accounts.go:108` as `fmt.Errorf("%w: email already registered", apiresp.ErrConflict)`
— a plain sentinel carrying no field detail.

## mod-core `apiresp` symbols now available (verified)

The real sibling checkout `/Users/zane/playground/moduleforge/mod-core/api/apiresp` contains, freshly
merged:

- `WriteActionRequired(w, r, action ActionCode, message, path string, data any)` (`action.go:87`).
  **It PANICS if `path` is not application-relative** (open-redirect guard: rejects scheme, `//`,
  backslashes, 2+ leading slashes). Callers MUST have panic-recovery middleware. It writes
  `action.Status` verbatim (503 never remapped). `data` is omitted (not `null`) when nil / typed-nil.
- `ActionCode struct{ Code string; Status int }` (`action.go:43`). **The package holds NO registry
  and does NOT validate the code or status** — "the closed set of action codes and their bound
  statuses is owned per-module, in each module's own API reference." So mod-users must own its own
  registered `ActionCode` values.
- `ActionBody` / `ActionEnvelope` wire types.
- `Conflict(details ...FieldError) error` (`conflict.go:38`) — returns a `*conflictError` that
  `errors.Is`-matches `ErrConflict` and carries field details for `WriteError` to surface. Mirrors
  `InvalidInput`.

## Decisions resolved from the spec + repo (no user input needed)

- **D1 — `action.path` is a GUI navigation route, not an API endpoint.** The design doc field
  semantics: "The navigation/redirect target the client must send the user to in order to complete
  the out-of-band action," and the GUI section says the client "navigates to `action.path`" (not
  "calls" it). Confirmed by the client-side same-origin-relative guard requirement.
- **D2 — `users.oidc_not_confirmed` path = `/oidc-config`.** This is RESOLVED because `/oidc-config`
  is the actual GUI navigation route in this ecosystem: `gui/src/components/client-layout.tsx`
  redirects to `CONFIG_PATH = '/oidc-config'` and `gui/src/components/sidebar-nav.tsx` links to it.
  The API endpoints live under `/v1/oidc-config/*`; the bare `/oidc-config` is the GUI route. The
  current code value is already correct. (The design doc's worked example `/setup/oidc` is
  illustrative/generalized, not the ecosystem's real route.)
- **D3 — `action.data` = `{"state": string(state)}`** using the actual `config.BootState` value
  (`require_confirmed.go` already passes `string(state)`). The doc's `"awaiting_oidc_config"` is an
  example value.
- **D4 — mod-users owns a small registered `ActionCode` registry.** A new internal package (e.g.
  `api/internal/useraction`) exports the three registered values
  (`EmailUnverified{Code:"users.email_unverified", Status:403}`,
  `StepUpRequired{Code:"users.step_up_required", Status:409}`,
  `OIDCNotConfirmed{Code:"users.oidc_not_confirmed", Status:503}`), imported by both the `auth` and
  `handlers` packages — "codes are declared once in the owning module's registry."
- **D5 — ZVum fix.** Move the `users.email_taken` detail into the sentinel so the writer surfaces it:
  redefine `svc.ErrEmailTaken` via `apiresp.Conflict(apiresp.FieldError{Field:"email",
  Code:"users.email_taken", Message:…})` (a stable package-level singleton, so `errors.Is` still
  works by identity), then collapse `writeServiceError`'s special case so email-taken flows through
  the same `apiresp.WriteError(w, r, err)` as everything else — removing the duplicated
  `publicMessage("conflict")` string and the local `WriteJSON`/`Envelope` construction.
- **D6 — internal_error branch.** Adopt the nested writer: `apiresp.WriteError(w, r, err)` with an
  untyped programmer error (e.g. `errors.New("RequireVerifiedEmail mounted before RequireAuth")`),
  which classifies to the reserved-core `internal_error` (500) default. No new capability.
- **D7 — last_identity.** `apiresp.WriteError(w, r, apiresp.Conflict(apiresp.FieldError{
  Code:"users.last_identity", Message:"You can't remove your last sign-in method. Add another
  first."}))` — top-level `conflict` + message-bound detail (field left empty per the design table's
  "message-only" entry).
- **D8 — GUI `request()` throws a dedicated `ApiActionRequiredError`** (carrying `code`/`message`/
  `path`/`data`), parallel to `ApiRequestError`. Adopted as the manager's recommended default.
  Alternatives considered and rejected: (a) returning a sentinel value/discriminated union — would
  force every `request<T>()` caller to branch on the union, a wide-blast-radius signature change;
  (b) reusing `ApiRequestError` — semantically wrong, would let callers render a navigation signal as
  an error banner, defeating the "navigate, don't alarm" contract.
- **D9 — `ApiAction`/`ApiActionResponse`/`ApiActionRequiredError` defined locally** in
  `gui/src/lib/api.ts` (core-gui does NOT yet export the action types — Wave 0 was Go-only; only the
  error types `ApiError`/`ApiErrorResponse`/`FieldErrorData`/`ApiRequestError` are already imported
  from `@moduleforge/core-gui`). Flag as a future promotion candidate to core-gui, following the same
  originate-locally-then-promote precedent the error types themselves went through.
- **D10 — go.mod/go.sum: no pin bump.** `api/go.mod` depends on core-api via a **local-path replace**
  (`replace github.com/moduleforge/core-api v0.0.0 => ../../mod-core/api`), not a version pin. Local
  replaces bypass go.sum, and the new symbols are picked up automatically from the resolved local
  checkout (verified present). The *actual* prerequisite is the worktree build-environment fix
  (`docs/mf-standards/building-common.md` "Building inside a task worktree"): a gitignored
  worktree-local `go.work` with three-`../`-corrected sibling paths plus `go work edit -replace`
  overrides for `core-model`/`core-api`. Each Go task worktree must run this recipe before building;
  no committed `go.mod`/`go.sum` change results.

## The OPEN QUESTION (blocks precise Go task content)

`users.email_unverified` and `users.step_up_required` currently carry **API-endpoint** path values
(`/v1/auth/email-code/request`, `/v1/self/credential/step-up`), but per D1 `action.path` must be a
**GUI navigation route**. Unlike `/oidc-config` (D2), neither the mod-users GUI nor this repo defines
an email-verification or step-up navigation route — those routes are mounted by the consuming apps
(`app-mfdemo`/`app-mftodo`), which are separate repos out of this worktree's reach. So the correct
route strings are not determinable here, and changing them from the current API-endpoint values is a
cross-repo breaking-behavior decision.

Two readings of the spec are both defensible:

- **Reading A (adopt navigation semantics — recommended).** `action.path` is a GUI route per D1;
  set `users.email_unverified` → `/verify-email` and `users.step_up_required` → `/step-up` (matching
  the design doc's worked examples), and require consuming apps to mount those routes. Semantically
  correct; consistent with the "navigate, don't alarm" GUI contract.
- **Reading B (preserve current values).** Read "the envelope normalises them to one field" as
  changing only the field *name*, carrying the current path *values* unchanged into `action.path`
  (`/v1/auth/email-code/request`, `/v1/self/credential/step-up`). Smaller wire-value diff, but leaves
  `action.path` pointing at POST API endpoints a browser cannot meaningfully navigate to.

This is surfaced as a user question. `oidc_not_confirmed` (`/oidc-config`) is not in question.

## Cross-repo consequence to verify (item 5)

`app-mfdemo`/`app-mftodo` are out of reach (separate repos). The manager's expectation is that a
`@moduleforge/users-gui` dependency bump suffices with no app-level code change. But followup `eiF8`
warns these apps "key off these bespoke [flat] shapes directly," and the `action.path` decision above
determines what navigation routes those apps must expose. This tension, plus the wire-shape change
itself, must be verified by someone who can see those repos — flagged for the manager, not
dispatchable from this worktree (same constraint as followups `0PHW`/`nzlS`).

## GUI call-site reachability finding

`gui/src/lib/api.ts` exposes **no identities/credential/step-up client methods**, so
`users.step_up_required` has **no currently-reachable GUI call site** in mod-users. The generic
`request()`-level `ApiActionRequiredError` throw covers all three codes uniformly, but only
`users.email_unverified` (403) and `users.oidc_not_confirmed` (503) are reachable via `GET /v1/self`
(the `auth-context.tsx` mount / `refreshUser` / `completeExternalLogin` paths). Wiring an actual
step-up navigation would need an identities-management UI that does not exist here — flag as a
follow-up rather than inventing a screen.

## Answer

The user selected **Reading A (adopt navigation semantics — recommended)**: `action.path` is a GUI
navigation route per D1. Set `users.email_unverified` → `/verify-email` and `users.step_up_required`
→ `/step-up`, matching the design doc's worked examples. Consuming apps (`app-mfdemo`/`app-mftodo`)
are required to mount those routes — this is a cross-repo coordination item, flagged for the manager
per the "Cross-repo consequence to verify (item 5)" section above, not built in this plan (out of
this repo's reach). Proceed with `/verify-email` and `/step-up` as the literal `action.path` values
for these two codes in the Go migration task content.
</content>
</invoke>
