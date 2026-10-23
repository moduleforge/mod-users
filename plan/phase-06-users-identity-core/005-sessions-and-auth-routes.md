# Sessions And Auth Routes

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Implement `SessionService` (session creation with factors, landing, token issue, refresh lineages
with reuse detection, switch, fork, step-up, logout) and rewrite the `/v1/auth` handlers for
register, login, anonymous, email code, password reset, token (refresh and client credentials) and
handoff, plus a new `/v1/session` handler. OIDC start and callback are task 006.

Scope: `api/internal/service/` (session service), `api/internal/handlers/auth/*.go` (rewritten,
except `oidc.go`), a new session handler under `api/internal/handlers/`.

Suggested role: `plugins/flow/roles/developer-go.md`; `plugins/flow/roles/architect-backend.md`
for the session and refresh design.

Dependencies within the phase: hard on 003 (carrier, `Identity`) and 004 (user, credential and
challenge services). Task 006 depends on this task's session creation and handoff; task 007
depends on its session list and revocation. Task 008 mounts the routes.

`api/` as a whole stays uncompilable until task 008; this task makes its own packages build.

## Requirements

1. **Session creation.** A login creates a `mod_users.sessions` row (principal, `app_id`,
   `expires_at = now + AUTH_SESSION_MAX_AGE`) and its `session_factors`; `acr` from
   `mod_users.session_acr`; `amr` from the factor kinds; `auth_time = now()`.
2. **Landing chain** (API surface 7): credential `default_profile_id`, `users.default_profile_id`,
   personal profile, the only available profile; each candidate checked with
   `profile_availability`. No result: `access_token` null and `selection_required` true with the
   `profiles` list. Token response shape exactly as in section 7 (binding for GUIs), including
   `ProfileView` with `available` and `lock` (reason and remedy).
3. **App gate (stub until the engine phase).** When the session's app has `invite_only`, deny
   unless the candidate profile holds a wildcard grant (`users.app_login_not_granted`); otherwise
   open. Phase `users-authorization-engine` replaces this with `Authorize(login, app)` (U15).
4. **Refresh lineages (U6).** Login and fork mint a lineage in `session_refresh_tokens`; `POST
   /v1/auth/token` with `grant_type=refresh_token` and `profile_uuid` rotates the token, re-runs
   availability for the named profile, and on reuse of a spent token revokes the whole session
   (`refresh_reuse`, 401 `users.refresh_reuse`). Lineage expiry follows
   `AUTH_SESSION_IDLE_TIMEOUT`.
5. **Client credentials.** `grant_type=client_credentials` with `client_id`, `client_secret`,
   optional `profile_uuid`: verifies `cred_api_key`, runs `credential_profile_availability`, issues
   a `cid` token (`AUTH_CLIENT_CREDENTIALS_TTL`, `amr ["swk"]`); no session row.
6. **Routes** under `/v1/auth` (behind `requireOIDCConfirmed`): `POST /register`, `POST /login`
   (`email`, `password`, optional `app`, `profile_uuid`), `POST /anonymous` (guest user with `anon`
   credential), `POST /email-code/request`, `POST /email-code/verify`, `POST
   /password-reset/request`, `POST /password-reset/confirm`, `POST /token`, `POST /handoff`
   (consumes a `login_handoff` challenge once and returns the token response), `GET /providers`.
   `allow_byo_identity` (and `NO_OIDC_ACCOUNTS` where it applies) refuses self-registration with
   `users.byo_identity_disabled`.
7. **`/v1/session` routes**: `GET /`, `POST /switch` (`profile_uuid`; new access token, no session
   write), `POST /fork`, `POST /step-up/password`, `POST /step-up/email-code/request`, `POST
   /step-up/email-code/verify`, `POST /logout` (revokes with `logout`). Step-up (U8) adds or
   re-verifies a factor, recomputes `acr`, sets `auth_time = now()`, returns a fresh access token.
   Any step-up refusal on these routes uses the shared `apiresp` action-required error added in mod-core phase 1 (followup `Uure`), not a local response shape.
   `POST /step-up/oidc/{provider}/start` is provided by task 006; task 008 mounts it here.
8. Expose `SessionService` methods task 007 needs: list a principal's sessions and revoke one
   session; and the revocation helper task 007's principal disable uses (`principal_disabled`).
9. Error order 401, 400, 403, 404/409 unchanged; error codes of section 7
   (`users.invalid_credentials`, `users.session_revoked`, `users.profile_unavailable`, ...).

   **Audit events (decision U21).** Emit the `session` observer resource with exactly the ops of
   [API surface section 10.1](../notes/mod-users-api-surface.md), which follow mod-audit's API
   surface section 2.2. Target is the principal in every case.
   - `login` when a sign-in creates a session: register, login, email-code verify, anonymous, and
     the session task 006's OIDC callback creates through this service.
   - `update` on step-up.
   - `revoke` on logout, refresh-reuse revocation and any other session revocation, including the
     principal-disable helper of requirement 8.
   - No event on refresh rotation, fork or switch.

   No other verb is allowed. `assume` does not exist any more, and mod-audit's CHECK rejects
   unknown ops. Under the `propagate` policy that rejection rolls back the sign-in itself. The
   `/v1/auth` routes (including the refresh-reuse revocation) run before any `AuthContext` exists,
   so for their observations install the
   `AuthContext` that section 10.1 prescribes for unauthenticated flows:
   - `ProfileID`: the landed profile, else the personal profile, else the oldest non-ended
     profile, including when landing returns `selection_required`;
   - `PrincipalID`: that profile's holder;
   - `SessionID`: the new session, with `CredentialID` 0.

   A failed sign-in writes no observation. Client-credentials token issue creates no session and
   writes no `login` event.
10. Export the handler constructor and registration function names task 008 needs
    (`SessionHandler`, `RegisterSessionRoutes`, `authhandlers.Handler`, `authhandlers.RegisterRoutes`).

## Validation

- `cd api && go build ./internal/service/... ./internal/handlers/auth/...` and the new session
  handler package build.
- `grep -rnE 'Set-Cookie|http.Cookie|SetCookie' api/internal/handlers/auth --include='*.go' | grep -v _test.go`
  shows only the `oidc_state` cookie code owned by task 006 (none in the files this task wrote).
- `grep -rnE 'first.?user|FirstUserHook' api/internal/handlers/auth --include='*.go' | grep -v _test.go`
  returns nothing.
- `grep -rnE '"assume"' api/internal/service api/internal/handlers --include='*.go' | grep -v _test.go`
  returns nothing in the files this task wrote, and every `Observe` op they use is `login`,
  `update` or `revoke`.
- Behavior is tested in task 010; the report lists the cases task 010 must cover beyond its
  document.

## Metadata

architectural_impact: true

## References

- [API surface sections 2.5, 2.9, 3, 5, 7, 8, 12](../notes/mod-users-api-surface.md).
- [Decisions U5, U6, U7, U8, U15, U21](../notes/decisions-and-resolutions.md), resolution (c).
- [API surface section 10.1](../notes/mod-users-api-surface.md): the audit op table and the
  acting-profile rule for unauthenticated flows.
- mod-audit plan worktree, `plan/notes/mod-audit-api-surface.md` sections 2.2 (op vocabulary),
  5.1 (observer field mapping, `ErrNoActor`) and 7 (notes for mod-users).
- [Work packages, package 5](../notes/work-packages.md).

## Checkpoint hints

- After session creation, landing and token issue.
- After refresh lineages and client credentials.
- After the `/v1/auth` handlers.
- After the `/v1/session` handler.

## Status

- **Outcome:** succeeded (2026-10-23).
- **Validation:** `cd api && go build ./internal/service/... ./internal/handlers/auth/... ./internal/handlers/session/...`
  passes. The cookie grep shows only `oidc.go` (task 006's `oidc_state` cookie). The first-user grep
  returns nothing. The `"assume"` grep shows only a comment in `api/internal/service/helpers.go`.
  The `Observe` ops in the new files are `login`, `update` and `revoke` (resource `session`).
  Beyond the required checks, `go vet` is clean for the three packages with the stale test files
  moved aside, and task 004's service unit tests still pass. A temporary smoke test ran against a
  throwaway Postgres 16 with the mod-core, mod-authz and mod-users migrations and was then
  deleted. It covered guest sign-in, `GET /v1/session`, refresh rotation, switch, fork, a step-up
  refusal, refresh reuse revoking the session, device-token sign-in, logout, and the 401/400
  shapes of login, token, handoff, email-code verify, reset confirm and register. It ran without
  observers.
- **Affected sources:** `api/internal/service/session_service.go`, `session_flows.go`,
  `session_views.go`, `session_errors.go` (new); `helpers.go` (`opLogin`, `ValidateEmail`,
  `ValidatePassword`); `api/internal/handlers/auth/handler.go`, `token.go` (new), `register.go`,
  `login.go`, `anonymous.go`, `emailcode.go`, `reset.go`, `routes.go` (rewritten);
  `api/internal/handlers/session/handler.go` (new package `session`: `SessionHandler`,
  `NewSessionHandler`, `RegisterSessionRoutes`).
- **`oidc.go`:** excluded from the build with `//go:build oidc_resolver_pending` so the package
  builds. Task 006 removes the constraint when it rewrites the file. `RegisterRoutes(r, h, oidc
  OIDCRoutes)` takes an interface (`Start`, `Callback`) that task 006's `OIDCHandler` satisfies.
  `nil` skips the two OIDC routes. `GET /providers` moved to `Handler.Providers`.
- **Decisions:**
  - Session creation inserts acr 1 as a placeholder, adds the factors, then sets acr from
    `session_acr` and amr from `ListSessionAMR`.
  - A disabled principal is refused before any write (401 `users.profile_unavailable`, reason
    `principal_disabled`). A principal with no non-ended profile is refused with reason
    `not_held`.
  - When the requested `profile_uuid` at login is unavailable, the response is
    `selection_required`. The chain does not fall back to another profile.
  - App gate stub: an invite-only app admits a profile only when
    `Authorize(ctx-with-candidate, "login", nil)` succeeds, which needs a wildcard grant. A refused
    profile gets lock reason `app_login_not_granted`. Sign-in is 403 `users.app_login_not_granted`
    when no available profile is admitted.
  - Lock remedies: `sign_in_with_provider` (with `provider_id`), `step_up`,
    `sign_in_with_authorized_credential`, `accept_invitation`. Otherwise the remedy is null.
  - Every step-up refusal (wrong password or code, no credential, an impersonation session) is
    `ErrStepUpRequired`, the shared `apiresp` action-required error (`useraction.StepUpRequired`,
    409).
  - `POST /v1/auth/anonymous` returns the token response plus `device_token` once. A later call
    with `device_id` and `device_token` signs the guest back in (200). This field is an addition
    to the section 7 shape that task 008's OpenAPI must document.
  - Refresh tokens are `mfr_` plus 64 hex characters, stored as `HashToken`. Each one expires at
    `min(now + AUTH_SESSION_IDLE_TIMEOUT, session expiry)`. A refresh against an unavailable
    profile rolls back, so the token stays usable.
  - A refresh token that is unknown or expired returns 401 `users.session_revoked`.
  - Refresh-reuse revocation observes as the personal or oldest profile. If the principal has no
    non-ended profile, the revocation is logged instead of observed so that it still commits.
  - Password reset confirm revokes the identifier's password (`superseded`), which also revokes
    sessions holding it. It then creates a new one that keeps the label, default profile and
    confinement. The old password's party authorizations are not copied.
  - Registration emails a `verify_identifier` code. The public email-code routes accept only
    purpose `login`, and email sign-in needs a verified email (`email_otp` credential, U12).
  - `NO_OIDC_ACCOUNTS` applies to OIDC just-in-time accounts (task 006), not to local
    registration.
- **Requirement 8 API for task 007:** `ListSessions`, `RevokeSessionInTx(principalID,
  sessionUUID, reason)`, `RevokePrincipalSessionsInTx(principalID, reason)`.
- **API for task 006:** `CreateSessionInTx`, `ExchangeHandoff`, `AddFactorInTx`,
  `TokenForSessionInTx`, `UnauthenticatedActingContext`.
- **Wiring for task 008:** `NewSessionService(pool, q, coreQ, az, obs, signer, challenges,
  SessionConfigFrom(cfg))`. `authhandlers.New(pool, q, coreQ, users, creds, challenges, sessions,
  oauth, sender, guiBase)`.
- **Cases for task 010 beyond its document:** `selection_required` followed by a refresh with a
  chosen profile; a requested unavailable profile at login; the invite-only gate (admitted,
  refused, all refused → 403); a refresh against an unavailable profile leaving the token usable;
  a concurrent double refresh (one succeeds, one is reuse); reuse when the principal has no
  non-ended profile; registration blocked by `allow_byo_identity`; password reset revoking
  sessions and keeping the default profile and confinement; device-token sign-in with a
  mismatched `device_id`; step-up raising acr with a second factor class (password plus
  `email_otp` → 2); impersonation sessions refused by fork and step-up; `login`, `update` and
  `revoke` observations under mod-audit (`MF401`, `ErrNoActor`).
