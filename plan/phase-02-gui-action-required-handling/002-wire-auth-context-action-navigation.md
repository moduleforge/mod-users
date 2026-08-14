# Wire Auth Context Action Navigation

## Purpose and scope

Wire the three reachable `api.self.get()` call sites in `gui/src/lib/auth-context.tsx` — the mount
effect, `refreshUser`, and `completeExternalLogin` — to catch the `ApiActionRequiredError`
introduced by the prior task and navigate the already-authenticated user to `action.path` via the
provider's injected `onNavigate`, instead of treating the response as a generic failure or clearing
the session. Depends on task `fix-api-action-discrimination` for the `ApiActionRequiredError` export
and the `ApiAction` field shape. Scope is limited to `gui/src/lib/auth-context.tsx`; no new
navigation-target screens are built — neither `/verify-email` nor `/step-up` exist as routes in this
repo (that is out of reach; see Requirement 3). No dedicated skill covers this change; implement
directly per the requirements below.

## Requirements

1. Import `ApiActionRequiredError` from `./api` in `auth-context.tsx` alongside the existing
   `ApiRequestError` import.
2. At each of the three `api.self.get()` call sites, add a catch branch (or extend the existing
   catch) that checks `err instanceof ApiActionRequiredError` **before** any other error-shaped
   handling, and calls the provider's `navigate(err.path)` (the same `navigate` binding `logout()`
   already uses). Preserve the following per-site distinctions — the current behavior for every
   non-action-required error must not regress:
   - **Mount effect** (the `useEffect` validating the stored token; today it clears the token and
     de-authenticates on any failure). On `ApiActionRequiredError`, do **not** clear the stored
     token — navigate to `err.path` and keep the token/session intact. This matches the design doc's
     "Action-required: navigate, don't alarm" distinction: action-required keeps the session, unlike
     the 401 token-clear-and-redirect path. On any other error, keep the existing clear-token
     behavior unchanged.
   - **`refreshUser`** (today only special-cases 401 → `logout()`). Add an
     `ApiActionRequiredError` branch that navigates to `err.path` without invoking `logout()`.
     Non-401, non-action-required errors keep their current silent-swallow behavior.
   - **`completeExternalLogin`** (today clears the freshly-stored token and rethrows on any
     failure). On `ApiActionRequiredError`, keep the token already written to `localStorage` at the
     top of the function (do not remove it) and navigate to `err.path`, since an action-required
     response means the token/session is valid but an out-of-band step (e.g. email verification)
     must complete first. Do **not** rethrow in this case — action-required is "not an error" per
     the design doc, and the caller must not render an error banner for it. For any other error
     (including a genuine `ApiRequestError`), keep the existing clear-token-and-rethrow behavior
     unchanged. Use judgment on whether `setToken(newToken)` should be called in the
     action-required branch so the provider's `token` state reflects the kept session — `setUser`
     cannot be called here, since no `UserAccountSelf` was returned by the failed `self.get()` call.
3. Do not invent a `/verify-email` or `/step-up` screen — no such route exists in this repo (the
   mod-users GUI does not mount consumer-app routes); the `onNavigate` call is the full extent of
   this task's responsibility, and mounting those routes in the consuming apps
   (`app-mfdemo`/`app-mftodo`) is a cross-repo coordination item already flagged for the manager (see
   `plan/notes/action-path-values-and-decisions.md`, "Cross-repo consequence to verify"). No new
   route work is needed for `/oidc-config` either — it already exists as a route in this repo
   (`gui/src/components/client-layout.tsx`).
4. `users.step_up_required` has no reachable GUI call site today — `gui/src/lib/api.ts` exposes no
   identities/credential/step-up client methods, so the generic `request()`-level
   `ApiActionRequiredError` throw for that code is never observed by any of the three call sites
   this task wires. Do not add a step-up call site or invent one to exercise it; this is a known,
   already-investigated gap (see `plan/notes/action-path-values-and-decisions.md`, "GUI call-site
   reachability finding"). Do not build a step-up screen or wiring beyond the generic
   transport-layer throw already provided by the prior task.
5. Record a follow-up (via the `followups_add` flow-mcp tool, `type:enhancement`) noting that
   `users.step_up_required` has no reachable GUI call site in `mod-users` (no identities-management
   UI exists to trigger it), so its action-required handling is presently limited to the generic
   `request()`-level throw with no navigation wiring. Reference `gui/src/lib/auth-context.tsx` and
   `gui/src/lib/api.ts` by path in the follow-up text; do not reference plan/phase/task names.

## Validation

- `cd gui && bun run typecheck` (yalc link required — see the prior task's validation note).
- `make lint.gui`.
- `make build.gui`.
- Re-check the gui/ test-infrastructure state (follow-up `KXNZ`) as the prior task did. If a test
  runner now exists (e.g., stood up by the prior task or a subsequent session), add unit tests for
  the navigation wiring: a mocked `api.self.get()` rejection with an `ApiActionRequiredError`
  results in exactly one `onNavigate(path)` call and no token-clear for the mount-effect and
  `refreshUser` cases, and no rethrow for `completeExternalLogin`; a plain
  `ApiRequestError`/network failure at each site continues to behave exactly as before (regression
  check). If no test runner exists, do not stand one up; rely on typecheck/lint/build, and rely on
  `KXNZ` for the tracked gap rather than duplicating it.
- Manual/behavioral check: read through all three modified call sites and confirm no site
  accidentally calls `navigate()` for a non-action-required error, and no site accidentally clears
  the token on an action-required response.
- Confirm the follow-up from Requirement 5 was recorded exactly once (`followups_list`, or inspect
  `plan/followups.yaml` in this worktree), tagged `type:enhancement`, with no plan/phase/task-name
  references in its text.

## Metadata

architectural_impact: false

## Assumptions

- The prior task (`fix-api-action-discrimination`) has landed first and `ApiActionRequiredError` /
  `ApiAction` / `ApiActionResponse` are already exported from `gui/src/lib/api.ts` with the fields
  `code`/`message`/`path`/`data`. If the actual exported shape differs from what this task assumes
  (e.g. a differently-named path field), adapt the call sites to the real shape rather than the
  shape assumed here.
- `onNavigate` (the `navigate` binding inside `AuthProvider`) is already wired by every consumer that
  needs real navigation; a no-op default exists for isolated usage (stories/tests) and it is
  acceptable for that no-op to receive `navigate('/verify-email')` / `navigate('/step-up')` /
  `navigate('/oidc-config')` calls with no observable effect in those contexts.
- No `/verify-email` or `/step-up` screen exists in this repo, and building one is out of scope for
  this plan (per the phase's "Out of scope / flagged" note) — the navigation call itself is the
  deliverable, not the destination.

## Status

**Outcome:** succeeded. Date: 2026-08-14.

Wired all three reachable `api.self.get()` call sites in `gui/src/lib/auth-context.tsx` to catch
`ApiActionRequiredError` before any other error-shaped handling and navigate via the provider's
`navigate` binding:

- **Mount effect** (`useEffect` validating the stored token): on `ApiActionRequiredError`, navigates
  to `err.path` and returns without clearing the stored token; any other error keeps the pre-existing
  clear-token behavior.
- **`refreshUser`**: added an `ApiActionRequiredError` branch that navigates to `err.path` without
  calling `logout()`; the existing 401 → `logout()` special case and the silent-swallow behavior for
  all other errors are unchanged.
- **`completeExternalLogin`**: on `ApiActionRequiredError`, keeps the token already written to
  `localStorage`, calls `setToken(newToken)` so the provider's reactive `token` state reflects the
  kept session (no `setUser` — the failed call returned no `UserAccountSelf`), navigates to
  `err.path`, and does **not** rethrow. Every other error (including a genuine `ApiRequestError`)
  keeps the existing clear-token-and-rethrow behavior.

**Validation summary:**
- `cd gui && bun run typecheck` — passed (yalc link for `@moduleforge/core-gui` set up in this
  worktree per `AGENTS.md` First-time setup step 4; it was not already present).
- `make lint.gui` — passed (gui's `lint` target is `tsc --noEmit`; no eslint config for this
  library).
- `make build.gui` — passed (tsup build succeeded, DTS included).
- Test-infrastructure check: a test runner exists in this worktree (confirmed by the prior task).
  Added `gui/src/lib/auth-context.test.tsx` (8 new tests, using `@testing-library/react` against
  `AuthProvider`/`useAuth`) covering, for each of the three call sites: an `ApiActionRequiredError`
  resulting in exactly one `onNavigate(path)` call with no token-clear (and, for
  `completeExternalLogin`, no rethrow), plus a regression case per site (a plain
  `ApiRequestError`/network failure continuing to behave exactly as before — 401 → `logout()` for
  `refreshUser`, clear-token for the mount effect and `completeExternalLogin`). Full suite:
  `bun test` — 28 pass, 0 fail across 4 files (up from 20 pre-existing).
- Manual read-through of all three modified call sites confirms no site calls `navigate()` for a
  non-action-required error, and no site clears the token on an action-required response.

**Affected source files:**
- `gui/src/lib/auth-context.tsx`
- `gui/src/lib/auth-context.test.tsx` (new)

**Assumptions applied:** all three `## Assumptions` bullets, as stated — the prior task's exported
`ApiActionRequiredError`/`ApiAction`/`ApiActionResponse` shape (`code`/`message`/`path`/`data`)
matched exactly, no adaptation needed; `onNavigate`'s no-op default is acceptable for isolated usage;
no `/verify-email`/`/step-up` screen was built.

**Caveat — Requirement 5 not completed directly:** this task agent's available MCP tool set did not
include a `followups_add` (or equivalently-named `add-followup`) tool (same gap noted by the prior
task, `phase-02-gui-action-required-handling/001-fix-api-action-discrimination.md`, whose own
Requirement 7 follow-up is still outstanding as of this task). See the dispatching report's
`flagged_for_manager` for the exact follow-up content to record.

## References

- `gui/src/lib/auth-context.tsx` — the file this task modifies; the three call sites (mount effect,
  `refreshUser`, `completeExternalLogin`) and the existing `logout()`/`navigate` pattern to mirror.
- `gui/src/lib/api.ts` (as modified by the prior task) — `ApiActionRequiredError`, `ApiAction`,
  `ApiActionResponse`.
- `docs/mf-standards/architecture/api-response-design.md` — "Action-required: navigate, don't alarm"
  (session-keeping vs. 401-redirect distinction) and "Client contract (`ApiRequestError`)" (the 401
  special case this task must not disturb).
- `plan/notes/action-path-values-and-decisions.md` — "GUI call-site reachability finding" (step-up
  non-reachability) and the resolved `action.path` values (`/verify-email`, `/step-up`,
  `/oidc-config`).
