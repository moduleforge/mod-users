# Rebuild API Client and Auth Context

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Rebuild the `@moduleforge/users-gui` library layer (`gui/src/lib/`) on the final mod-users HTTP API
and implement the token side of the frontend contract of resolution (c): per-tab token storage, one
refresh lineage per tab, Bearer-only requests with `credentials: 'omit'`, fork of a new tab's
lineage over `BroadcastChannel`, the OIDC `#handoff=` exchange, profile switching and landing
selection state, step-up retry plumbing, and 401 handling without a hard-coded navigation.

This task is the foundation of the phase: it fixes the client methods, types, auth-context values
and route constants that tasks 002 to 005 build components on, so those four tasks can run in
parallel. It does not build UI components beyond what the existing auth context already renders.

No standard skill covers this task; follow the Procedure below. Suggested role:
`plugins/flow/roles/developer-react.md`.

## Requirements

1. **Typed client (`gui/src/lib/api.ts`).** Replace the request surface with methods and types for
   every route of [API surface section 7](../notes/mod-users-api-surface.md#7-http-api) that the GUI
   consumes, coded against the merged handlers and `api/openapi.yaml` from phases 6 to 8:
   - `auth`: register, login (`email`, `password`, optional `app`, `profile_uuid`), anonymous,
     email-code request/verify, password-reset request/confirm, token (`refresh_token` grant with
     `profile_uuid`), handoff (`code`), providers.
   - `session`: get, switch, fork, step-up (password, email-code request/verify, OIDC start returning
     `authorize_url`), logout.
   - `me`: get/update, profiles (list, accept, decline, leave), identifiers (list, add, verify,
     resend verification, set primary, delete), credentials (list, add password, add SSH key, OIDC
     link start, patch `label`/`default_profile_uuid`, delete), sessions (list, revoke), guest
     upgrade.
   - `impersonations`: start, renew current, end current.
   - `bootstrap`: status, bootstrap (with `X-Setup-Token`).
   - Types for the token response, `ProfileView` (including `available` and `lock` with `remedy`),
     session view, and the new error/action codes listed in section 7.
   - Remove the client methods and types for removed routes: `self`, `userAccounts` (including
     `grantAdmin`/`revokeAdmin`/`assume`), `/v1/self/ssh-keys*`, `/v1/self/credential/step-up*`,
     `UserAccount*`, `StepUpVerifyResponse`, `StepUpOptions` and the `X-Step-Up-Token` header path.
   - Apps client (followup `PRvD`): align `apps.create`/`apps.update` with mod-core's apps contract
     (required `slug`, no `description`, `invite_only`) or remove the apps client and its types if
     nothing in the GUI uses it; remove `apps` member routes (`/v1/apps/{uuid}/user-accounts*`, removed
     by U16). State in the report which option was taken.
   - Leave the mod-audit `audit` client methods untouched unless they reference removed routes.
   - `gui/src/lib/oidc-config.ts` and `gui/src/lib/oidc-provider.ts` are owned by task 005; do not
     edit them here.
2. **Per-tab token storage (`gui/src/lib/config.ts`).** Access token, refresh token and the active
   profile uuid live in `sessionStorage` (or memory when unavailable), never `localStorage` or
   cookies. The access token is stored under the key core-gui's api-client reads (today `auth_token`,
   moving to `sessionStorage` per mod-core decision D17) so host apps' other module clients send the
   same tab's token; verify the key against the updated `@moduleforge/core-gui` and keep
   `configureUsersApi`'s storage-key option working. Every request sends `Authorization: Bearer` and
   `credentials: 'omit'`.
3. **Refresh lineage.** On access-token expiry (or a `401` that is not a revocation code), call
   `POST /v1/auth/token` with the tab's refresh token and active `profile_uuid`, store the rotated
   refresh token, and retry once. Concurrent requests in one tab share a single in-flight refresh
   (a second rotation of the same token would trip reuse detection). On `users.session_revoked` or
   `users.refresh_reuse`, clear the tab's tokens and sign out.
4. **Fork (`BroadcastChannel`).** A tab that starts with no tokens asks sibling tabs over a
   same-origin `BroadcastChannel` for an access token, and on receipt calls `POST /v1/session/fork`
   to obtain its own lineage. Only access tokens cross the channel, never refresh tokens. With no
   answer within a short timeout, the tab is signed out. Degrade cleanly where `BroadcastChannel` is
   undefined (tests, SSR).
5. **Handoff.** Provide `completeHandoff(code)` in the auth context: one call to
   `POST /v1/auth/handoff`, then store the token response like a login. Replaces
   `completeExternalLogin(token)`.
6. **Auth context (`gui/src/lib/auth-context.tsx`).** Expose, at minimum: the current session view,
   the active `ProfileView`, the profile list, `selectionRequired`, `selectProfile(uuid)` (refresh
   grant with the chosen profile), `switchProfile(uuid)` (`POST /v1/session/switch`, replaces only
   this tab's access token and active profile), `profileLock` (set from
   `401 users.profile_unavailable` with reason and remedy), `impersonating`, `login`, `register`,
   `logout`, `completeHandoff`, and a step-up hook. Remove `isAdmin` and anything derived from the
   removed `/v1/self` payload: platform-admin authority is an operator-party profile plus grants
   (U16) and cannot be derived client-side; the server's 403 is the authority.
7. **Step-up plumbing.** On `409 users.step_up_required`, the client invokes a step-up handler
   registered through the auth context (a provider-level slot or hook whose prompt UI task 004
   supplies), and retries the original request once after the handler resolves with the fresh access
   token. Define and export the handler's TypeScript interface here; without a registered handler the
   original `ApiActionRequiredError` propagates.
8. **401 handling (followup `HPJi`).** Remove the hard navigation to the literal `/auth/login` from
   the shared fetch path. Unauthenticated handling goes through the configurable callback in
   `configureUsersApi` (`UnauthenticatedContext`); the default navigates to the configured login
   route, and an app can override it.
9. **Route constants (`gui/src/lib/routes.ts`).** Add every new page path tasks 002 to 005 mount so
   they do not edit this file concurrently: landing profile selection, invitations, account
   settings (credentials, identifiers, sessions), bootstrap, and the OIDC return path now carrying
   `#handoff=`. Mark backend-fixed paths as such.
10. **`gui/src/lib/return-path.ts`** keeps working with the new login and handoff flow.
11. **Exports (`gui/src/index.ts`).** Export the new client, types, auth-context values and step-up
    interface; drop removed symbols. Task-owned component exports are added by tasks 002 to 005.
12. **Tests.** Rewrite `api.test.ts`, `auth-context.test.tsx`, `config.test.ts`,
    `unauthenticated.test.ts`, `routes.test.ts` and `return-path.test.ts` to cover: Bearer and
    `credentials: 'omit'` on every request; sessionStorage-only storage; single in-flight refresh
    and retry; sign-out on `session_revoked`/`refresh_reuse`; fork over a mocked
    `BroadcastChannel`; handoff single call; switch replacing only the tab's token;
    `selection_required` flow; `profile_unavailable` populating `profileLock`; step-up retry with
    and without a handler; no hard navigation on 401.
13. **`gui/README.md`.** Add a "Frontend contract" section documenting
    [API surface section 12](../notes/mod-users-api-surface.md#12-frontend-contract-resolution-c)
    as implemented (storage, lineage, fork, switch, error handling, handoff), the new
    `configureUsersApi` options and the auth-context API; remove documentation of removed symbols.

## Validation

- `make -C gui build` succeeds.
- `make -C gui test` passes for every test under `gui/src/lib/`.
- `make -C gui lint` (tsc): errors are allowed only in the component files owned by tasks 002 to 005
  (`sidebar-nav.tsx`, `require-auth.tsx`, `client-layout.tsx`, `login-form.tsx`,
  `register-form.tsx`, `auth-page.tsx`, `email-code-page.tsx`, `oidc-callback-page.tsx`,
  `verify-email-page.tsx`, `forgot-password-page.tsx`, `reset-password-page.tsx`,
  `ssh-keys-panel.tsx`, `oidc-config-page.tsx`, `oidc-setup-gate.tsx`, the OIDC provider modals and
  their tests/stories); list every remaining error and its owning task in the report.
- `grep -rn "localStorage" gui/src/lib` returns only test assertions that it is unused.
- `grep -rnE "/v1/self|/v1/user-accounts|/v1/assume|X-Step-Up-Token|step_up_token" gui/src/lib`
  returns nothing.
- `grep -rn "'/auth/login'" gui/src/lib` returns only the route constant/default, not a fetch-path
  navigation.

## Metadata

architectural_impact: true

## Assumptions

- Phases 6 to 8 are merged: the `/v1/auth`, `/v1/session`, `/v1/me`, `/v1/impersonations` and
  `/v1/bootstrap` routes and `api/openapi.yaml` are final. Where the API surface note and the merged
  handlers disagree, the handlers win; record the difference in the report.
- `@moduleforge/core-gui` resolves through the root bun workspace's sibling member
  `../mod-core/gui` (see `.claude/CLAUDE.md` and `AGENTS.md` first-time setup), which must point at
  mod-core's plan worktree (`scripts/link-siblings.sh`) so it carries D17's per-tab api-client
  default; build it with `bun run build` in that sibling before `make -C gui build`/`lint`. If the
  sibling does not carry D17 yet, report it rather than guessing core-gui's storage key.
- This task intentionally leaves component call sites of removed client methods broken for tasks
  002 to 005 to repair (see Validation).
- Any followups change (for example closing `HPJi` or `PRvD`) is not made by this task: state the
  exact intended change in the structured report for the manager to apply.

## References

- [API surface sections 7 and 12](../notes/mod-users-api-surface.md) — routes, token response,
  `ProfileView`, error codes, frontend contract.
- [Decisions U6, U7, U8, U16](../notes/decisions-and-resolutions.md) — refresh lineages, handoff,
  step-up, removed routes.
- [Work packages, phase `users-gui-and-frontend-contract` item 1](../notes/work-packages.md).
- mod-core plan worktree `plan/notes/mod-core-api-surface.md` section 7.8 and decision D17 —
  core-gui per-tab storage and `credentials: 'omit'`.
- Followups `HPJi` (hard 401 navigation) and `PRvD` (apps client body shape).

## Procedure

1. Read `api/openapi.yaml` and the merged handlers for the consumed routes; draft types first.
2. Rewrite `config.ts` storage, then the `request` function in `api.ts` (Bearer, omit, refresh,
   step-up retry, unauthenticated callback), then the client methods.
3. Rewrite `auth-context.tsx` state around the token response; add fork and handoff.
4. Update `routes.ts`, `index.ts`, tests, then `gui/README.md`.

## Checkpoint hints

- After `config.ts` and the `request` function with their tests.
- After the client methods and types.
- After `auth-context.tsx` with fork and handoff and its tests.
- After `routes.ts`, `index.ts` and `gui/README.md`.
