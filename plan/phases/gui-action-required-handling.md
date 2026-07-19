# Phase — GUI Action-Required Client And Wiring

## Goals

Fix the GUI client's error/action discrimination bug and implement real action-required handling in
`gui/src/lib/api.ts`, then wire the reachable call sites to navigate on an action-required signal
rather than surfacing an error. This phase depends only on the finalized action-code contract from the
design doc (not on the Go phase's build output), so it is **parallel-eligible with Phase 1**. The
`action.path` navigation targets are now resolved: `/verify-email` (`users.email_unverified`),
`/step-up` (`users.step_up_required`), and `/oidc-config` (`users.oidc_not_confirmed`).

Scope of work:

- Define `ApiAction` / `ApiActionResponse` wire types **locally** in `gui/src/lib/api.ts` (core-gui
  does not yet export them — Wave 0 was Go-only), per the design doc's TS sketch (D9). Flag as a
  future promotion candidate to `@moduleforge/core-gui`.
- Define a dedicated `ApiActionRequiredError` class carrying `code`/`message`/`path`/`data`, parallel
  to `ApiRequestError` (D8).
- Fix `request()`: on a non-2xx response (after the existing 401 special-case), parse the body once
  and inspect the **top-level member** — a present `action` short-circuits error handling and throws
  `ApiActionRequiredError`; it is **never** thrown as `ApiRequestError` and never surfaces a banner.
  Add a defense-in-depth same-origin-relative guard on `action.path` (reject and fall back to a safe
  default route if not same-origin-relative), mirroring the server-side guard.
- Wire the reachable `api.self.get()` call sites in `gui/src/lib/auth-context.tsx` (the mount effect,
  `refreshUser`, and `completeExternalLogin`) to catch `ApiActionRequiredError` and navigate via the
  injected `onNavigate`. These paths can surface `users.email_unverified` (403) and
  `users.oidc_not_confirmed` (503) from `GET /v1/self`.
- Add unit tests for the new `request()` discrimination and the navigation wiring, **if** GUI test
  infrastructure can be stood up — see the Inputs note on followup `KXNZ`.

## Inputs

- The finalized contract, especially `### Action-required: navigate, don't alarm` and the
  `ApiAction`/`ApiActionResponse` TS sketch in
  `docs/mf-standards/architecture/api-response-design.md`.
- Current `gui/src/lib/api.ts` (`request()` helper; already imports error types from
  `@moduleforge/core-gui`) and `gui/src/lib/auth-context.tsx` (the `api.self.get()` callers).
- **Reachability finding:** there are **no identities/credential/step-up client methods** in
  `api.ts`, so `users.step_up_required` has no currently-reachable GUI call site — its handling is
  limited to the generic transport-layer throw; actual step-up navigation is a follow-up (no such
  screen exists to wire).
- **Resolved user answer (was blocking):** the `action.path` values the client navigates to are
  `/verify-email` (`users.email_unverified`) and `/step-up` (`users.step_up_required`) — Reading A,
  the same resolution as Phase 1; `/oidc-config` for `users.oidc_not_confirmed` was already resolved.
- Build/validation caveats: `gui/` needs the yalc `@moduleforge/core-gui` link for
  typecheck/build (AGENTS.md); followup `KXNZ` notes `gui/` currently has **no test runner set up**,
  so `bun test` may be unsatisfiable without first standing up test infrastructure — the task must
  verify current state rather than assume.

## Outputs

- `gui/src/lib/api.ts` correctly discriminates action-required responses from errors: a `403`/`409`/
  `503` carrying a top-level `action` is thrown as `ApiActionRequiredError`, never as
  `ApiRequestError`. The pre-existing latent bug (treating a truthy `error` field as always an object)
  is also resolved for these sites since they no longer emit a string `error`.
- Local `ApiAction`/`ApiActionResponse`/`ApiActionRequiredError` exports (flagged for future core-gui
  promotion).
- `auth-context.tsx` navigates the already-authenticated user to `action.path` on an action-required
  response from `GET /v1/self`, keeping the session (distinct from the 401 token-clear redirect).
- A recorded follow-up for the unreachable `users.step_up_required` call site and for the
  core-gui promotion.
</content>
