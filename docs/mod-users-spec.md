# mod-users Specification

## Purpose and scope

This document is the canonical functional specification for `@moduleforge/mod-users`. It describes what the module does and is required to do — the behavioral contracts an implementation must satisfy. It is written for developers and AI agents implementing, modifying, or integrating the module.

This spec assumes familiarity with the ModuleForge module model described in [mod-core's architecture overview](./mf-standards/architecture.md). It does not repeat cross-cutting design rationale (authorization, state management, entity typing, database conventions) that is documented there. The identity model it implements (principals, credentials, sessions, profiles and parties) is the one accepted in mod-core's [identity model ADR](https://github.com/moduleforge/mod-core/blob/main/docs/architecture/identity-model.md).

The spec covers three sub-packages — `model`, `api`, and `gui`. A demo application (`app-mfdemo`) that exercises the module end-to-end is a separate project at the aggregate level. For design choices and the *how* of each sub-package, see [docs/architecture.md](./architecture.md); for how sessions, tokens, liveness, step-up and impersonation work, see [Sessions and tokens](./architecture/sessions-and-tokens.md). For directory layout and sub-project conventions, see [docs/project-structure.md](./project-structure.md). For build, test, and development commands, see [AGENTS.md](../AGENTS.md).

## Table of contents

1. [Purpose and scope](#purpose-and-scope)
2. [Key use cases](#key-use-cases)
3. [General features](#general-features)
4. [Data model](#data-model)
5. [API definition](#api-definition)
6. [Security requirements](#security-requirements)
7. [Non-goals](#non-goals)
8. [Pointers to deeper docs](#pointers-to-deeper-docs)

## Key use cases

### 1. Register a new account via email and password

**Actor:** Unauthenticated end user.

**Action:** Submits email address, password (at least 12 characters), given name, and family name to `POST /v1/auth/register`.

**Outcome:** In one transaction a new bring-your-own `user` principal is created with its own `natural_person` record and personal profile, an unverified primary email identifier, and a password credential bound to that identifier. A session is created with the password as its factor, a verification code is emailed, and the response is the token response (access token, refresh token, session, landing profile). If the email is already registered, the request is rejected with `409` (`users.identifier_taken`); `403 users.byo_identity_disabled` when the platform does not allow bring-your-own identities.

---

### 2. Log in with email and password

**Actor:** Registered end user with a password credential.

**Action:** Submits email and password to `POST /v1/auth/login`.

**Outcome:** The password is verified against the stored argon2id hash. On success a session is created and the token response is returned; `access_token` is null and `selection_required` true when the landing chain finds no available profile, in which case the client chooses one through the refresh grant. On failure, `401 users.invalid_credentials` is returned. The response does not distinguish an unknown email from a wrong password (no account enumeration).

---

### 3. Log in via email one-time code

**Actor:** Registered end user.

**Action:** Requests a code with `POST /v1/auth/email-code/request` (providing email), then submits the code via `POST /v1/auth/email-code/verify`.

**Outcome:** The code request always returns `204` regardless of whether the email matches a known account (prevents enumeration). A hashed, time-limited code is generated and sent to the email address. A request inside the 60 s cooldown of the newest code for that subject is silently skipped (no email is sent) and still returns `204`. On successful verification the code is consumed and the token response is returned (`200`). Wrong, expired, already-consumed, or exhausted (5 failed attempts) codes are rejected with `401 users.invalid_credentials`.

---

### 4. Reset a forgotten password

**Actor:** Registered end user with a password credential.

**Action:** Requests a reset email with `POST /v1/auth/password-reset/request` (providing email), then submits the reset token and new password to `POST /v1/auth/password-reset/confirm`.

**Outcome:** The reset request always returns `204` (prevents enumeration). A hashed, time-limited, single-use reset token is generated and sent to the email address. On successful confirmation the password bound to the token's email identifier is superseded, which also revokes every session holding it as a factor; confirmation does not sign in. Invalid, expired or spent tokens are rejected with `401 users.invalid_credentials`.

---

### 5. Log in via OIDC provider (OAuth 2.0 authorization-code flow)

**Actor:** End user with an account at a configured OIDC provider (e.g. Google, Microsoft Entra ID).

**Action:** The browser generates a random verifier and is directed to `GET /v1/auth/oidc/{provider}/start?handoff_challenge=<base64url SHA-256 of the verifier>`, which carries the challenge in a signed state token, sets the `oidc_state` cookie, and redirects to the provider's authorization endpoint. A sign-in start without a valid challenge is refused with `400`. After the user authenticates at the provider, the browser is redirected back to `GET /v1/auth/oidc/{provider}/callback`.

**Outcome:** The callback handler validates the state cookie, exchanges the authorization code for an ID token, verifies the token (signature, issuer, audience, nonce), and resolves the verified `(issuer, subject)` against active `oidc` credentials. The identity is **never matched to an account by email**. A known identity signs its principal in; an unknown one creates a user only when the provider's just-in-time mode allows it (`byo` creates a bring-your-own user, `managed` a user managed by the provider's party), and otherwise is refused. On success the browser is redirected to the configured return URL with a one-time `#handoff=<code>` in the fragment — never a token — and the client redeems it, with the verifier that never left the browser, at `POST /v1/auth/handoff`. A refusal redirects with `#error=<code>` (`account_exists_sign_in_and_link`, `no_account`, `authentication_failed`, `missing_email`). The `return` query parameter on the start endpoint restricts to site-relative paths to prevent open-redirect attacks.

---

### 6. Link an OIDC identity to an existing account

**Actor:** Authenticated end user.

**Action:** Calls `POST /v1/me/credentials/oidc/{provider}/start`, which returns an authorize URL (and sets the signed state cookie); the user authenticates at the provider and the browser returns through the OIDC callback.

**Outcome:** The callback links the verified `(issuer, subject)` pair as an `oidc` credential of the calling session's principal and redirects with an outcome. A subject already linked to any principal is `409 users.oidc_identity_in_use`. A guest must upgrade first (`409 users.guest_upgrade_required`); a service account or a managed user cannot link (`403`). A provider bound to a party confines the new credential to that party. A single user may hold several `oidc` credentials (one per provider identity). This is also how a person whose sign-in was refused with `account_exists_sign_in_and_link` attaches the provider to their existing account.

---

### 7. View and update own user record

**Actor:** Authenticated end user.

**Action:** Calls `GET /v1/me` to retrieve the user record, or `PUT /v1/me` to update given name, family name, default profile or default application.

**Outcome:** On `GET`, the caller's user is returned including `is_guest`, `managed`, names, display name, primary email, `email_verified` (true when the user holds at least one verified email identifier), and the default profile and app. On `PUT`, the provided fields are updated (names live on the user's own person record) and the updated record is returned; absent fields are unchanged and the two default fields accept `null` to clear. Both are reachable to an account whose email is unverified (the GUI relies on this to render the "verify your email" prompt).

---

### 8. Manage identifiers and credentials (self-service)

**Actor:** Authenticated end user.

**Action:** Calls `GET`/`POST /v1/me/identifiers` and `DELETE /v1/me/identifiers/{uuid}` to list, add (email) and remove identifiers, `POST .../verification` and `POST .../verify` to verify one with an emailed code, and `PUT .../primary` to make one primary; `GET /v1/me/credentials` to list active credentials, `POST /v1/me/credentials/password` to add a password, `POST /v1/me/credentials/ssh-keys` to add an SSH key (use case 16), `POST /v1/me/credentials/oidc/{provider}/start` to link an OIDC identity (use case 6), `PATCH /v1/me/credentials/{uuid}` to change a label or landing profile, and `DELETE /v1/me/credentials/{uuid}` to revoke one.

**Outcome:** Verifying an email identifier automatically creates an `email_otp` credential bound to it, so passwordless sign-in keeps working (the user may revoke it). Revoking a credential revokes every session that holds it as a factor; revoking the last interactive credential is rejected with `409 users.last_credential`, so a user cannot lock themselves out. Every self-service write authorizes on the session's principal being the target, not on grants, and requires the session's `auth_time` to be within `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (default 10 minutes): a stale authentication is rejected with the action-required response `409 users.step_up_required`, and the client runs a [step-up](./architecture/sessions-and-tokens.md#step-up) (`POST /v1/session/step-up/password`, `.../email-code/request` and `.../verify`, or `.../oidc/{provider}/start`), which re-verifies a factor in the current session, resets `auth_time` and returns a fresh access token, then retries once. A just-signed-in user passes without friction. No step-up token or `X-Step-Up-Token` header exists.

---

### 9. Admin: manage principals

**Actor:** Authenticated administrator holding the relevant grants on the target principal.

**Action:** Calls `GET /v1/principals` (list, filter by `kind`), `GET /v1/principals/{uuid}`, `POST /v1/principals/{uuid}/disable` or `POST /v1/principals/{uuid}/enable`.

**Outcome:** The list returns the users and service accounts the caller may list, through the row-scoped access functions, with the same owning-party gate as a single-row read; a caller with no list grants gets an empty page. A principal the caller may not read is `404`, the same as an unknown one. Disabling needs `update` on the principal and a fresh authentication and revokes every session of the principal (`principal_disabled`); disabling the principal of the last effective operator administrator is refused with `409 users.last_operator_admin`. All mutating operations are audited. Platform administrators are operator-party profiles holding grants; there is no admin flag on a user.

---

### 10. Admin: impersonate a profile

**Actor:** Authenticated administrator holding `impersonate` on the target profile (a party administrator's owned-scope `manage` grant reaches the party's profiles; a personal profile needs an operator-party profile with a wildcard `impersonate` or `manage` grant).

**Action:** Calls `POST /v1/impersonations` with a `profile_uuid` and a `reason`, then `POST /v1/impersonations/current/renew` to extend it and `DELETE /v1/impersonations/current` to end it.

**Outcome:** A child session of the administrator's own session is created, and the response is a token response without a refresh token whose `act.sub` is the administrator's principal uuid and whose lifetime is at most `AUTH_IMPERSONATION_TTL` (default 30 minutes). Starting needs an authentication no older than ten minutes (`409 users.step_up_required` otherwise); the child inherits the administrator's `auth_time`. While impersonating, the impersonated profile's authorizations decide what the session may do, and every party's authentication policy applies as for that profile, evaluated on the administrator's session — so impersonation never skips a party's `credential_rule`, except that an impersonation started through a platform operator profile is exempt from a customer party's `credential_rule` (never from `min_acr`). An impersonation never nests, cannot switch or fork profiles, and cannot touch credential or identification data (`403 users.impersonation_not_allowed`). A renewal re-checks that the administrator may still impersonate the profile, from the administrator's session, and extends to now plus the TTL up to `AUTH_IMPERSONATION_MAX` (default 60 minutes) from the start; revoking or expiring the administrator's session ends the impersonation. The administrator is recorded as the impersonator on every request, and start, renew and end are audited (`impersonate`, `update`, `revoke` on resource `impersonation`).

---

### 11. Party administration: profiles, managed users, policies

**Actor:** Authenticated party administrator.

**Action:** Calls the `/v1/parties/{party_uuid}` routes: `GET`/`POST .../profiles` to list a party's profiles and invite a user (`holder_email` or `holder_user_uuid`; an invitation never creates an account); `POST .../managed-users` to create a user owned and managed by the party; `GET`/`PUT .../authn-policy` to read and set the party's `credential_rule` (`any`, `authorized`, `provider`) and `min_acr`; `GET .../credential-authorizations` and `PUT`/`DELETE .../credential-authorizations/{credential_uuid}` to authorize credentials for the party's `authorized` rule; `PUT`/`DELETE .../confinements/{credential_uuid}` to confine credentials to the party; and `PATCH /v1/profiles/{uuid}` to suspend, resume or end a profile. Invited users act through `POST /v1/me/profiles/{uuid}/accept`, `/decline` and `/leave`.

**Outcome:** A party or profile the caller may not read is `404` before any `403`. Inviting a user whose credentials cannot meet the party's policy is `409 users.policy_mismatch_confirmation_required` unless the request sets `confirm_policy_mismatch`, which needs `update` on the party; at invite time the `authorized` rule counts as met by any usable interactive credential, and the member stays locked until a credential is authorized after acceptance. Authentication-policy writes, authorizations, confinements and profile status changes need a fresh authentication. Ending a profile deletes the holder's credential authorizations for the party, revokes the holder's credentials confined to it and disables a managed user left with no open profile. Changing a party's `credential_rule` clears the party's policy confirmations. All mutating operations are audited.

---

### 12. Admin: bootstrap the deployment and configure OIDC providers

**Actor:** Operator holding the setup token printed at boot; afterwards an authenticated operator administrator.

**Action:** On a fresh deployment, calls `POST /v1/bootstrap` with the token in the `X-Setup-Token` header and the operator and administrator details, then uses the OIDC configuration UI (served by `app-mfdemo`) to add, edit, or disable OIDC providers. Changes are persisted to `oidc_providers`. The "Test configuration" flow exercises a full round-trip against the provider without affecting the admin's session and without creating user records.

**Outcome:** Bootstrap runs once. In one transaction it creates the first user (with a verified primary email, password, email-code credential, person record and personal profile), the operator party (an organization, or the first user's own natural person), claims it as the operator party (the race-free single winner; a second attempt is `409 users.already_bootstrapped`), the administrator's party profile with a wildcard `manage` grant, and the operator party's authentication policy, and returns a session that lands on the administrator profile. The effective provider configuration is the merged result of environment variables and any `oidc_providers` DB overrides — DB rows take precedence. The `oidc_config` singleton tracks whether OIDC is opted out entirely, and holds the setup-token hash. The one setup token authorizes both the bootstrap and the OIDC confirm; it stays active while either is pending and is cleared when both are done, after which configuration requires an operator administrator session (an acting profile that is an operator profile holding wildcard `manage`).

---

### 13. View audit log

**Actor:** Authenticated admin user.

**Action:** Calls `GET /v1/audit` (all recent entries) or `GET /v1/audit/{resource_type}/{resource_uuid}` (entries by target resource), served by mod-audit when composed. Both support pagination and the first an optional `resource_type` filter.

**Outcome:** Paginated audit entries are returned. Each entry records the operation, resource type, resource UUID, the acting profile and principal (and the impersonator, session or credential when there is one), before/after state snapshots, and timestamp.

---

### 14. GUI component rendering and demo app

**Actor:** Developer or AI agent composing a ModuleForge-based application.

**Action:** Imports components from `@moduleforge/users-gui` — via a **bun workspace** the
composing app owns (e.g. `app-mftodo`; see
[`docs/mf-standards/building-applications.md`'s First-time setup
section](./mf-standards/building-applications.md#first-time-setup)), or via npm if this package
is ever published to a registry — and composes them into an application shell.

**Outcome:** Components render the sign-in, credential and session self-service, profile-switching, impersonation, bootstrap and OIDC-config surfaces. The `app-mfdemo` Next.js project (at the aggregate level) demonstrates all components in a working context; it is the component showcase (the role Ladle plays in other projects). The demo app requires the API running locally.

The library is router-agnostic: it owns no route files and reads no router; the app creates each route and injects navigation through callback props. It implements the Bearer-only, per-tab frontend contract (tokens held per tab, `credentials: 'omit'` on every request except the two OIDC authorize-URL start calls, which send `credentials: 'include'` so the signed `oidc_state` cookie is kept; see [Sessions and tokens](./architecture/sessions-and-tokens.md#client-contract)). It ships page components for the paths the backend fixes — `/verify-email` (`VerifyEmailPage`, target of `users.email_unverified`), `/oidc-config` (`OidcConfigPage`, with `OidcSetupGate`, target of `users.oidc_not_confirmed`), and `/reset-password` (`ResetPasswordPage`, the password-reset email's link target) — and exports the standard path set as `USERS_GUI_ROUTES`; mounting those routes and wiring `AuthProvider`'s `onNavigate` to a real navigation is the app's job. A `409 users.step_up_required` is not a page: `StepUpDialog` registers as the step-up handler and prompts for the password, an emailed code or an OIDC provider, then the client retries the interrupted request once. The app configures the client once at startup through `configureUsersApi`: API base URL, token storage key, the unauthenticated redirect URL, an optional return-path query parameter (opt-in return handling on login), and an `onUnauthenticated` handler that replaces the default redirect. Components accept further props (for example `AuthPage allowRegistration`, `returnPath`, `onForgotPassword`). The package ships no CSS file (consumers `@source`-scan `dist/`). The field-level reference and integration examples are in [`gui/README.md`](../gui/README.md).

---

### 15. Create a guest account and optionally upgrade it

**Actor:** Unauthenticated end user (typically a client application acting on behalf of a new visitor with no credentials).

**Action:** Submits a `device_id` (stable device fingerprint) to `POST /v1/auth/anonymous`.

**Outcome:** A new *guest user* is created: a real, per-device principal with a personal profile, no person record and one `anon` credential, distinct from the shared, zero-authority *anonymous actor* mechanism described in [Security requirements](#security-requirements) (the two are separate and are not converged; see the comparison there). The response (`201`) is the token response for the new session plus a one-time `device_token`. The `device_token` can be supplied by the client on a later visit to sign the same guest back in (`200`, no new token), so the guest keeps its identity and data across sessions. A guest can hold only `anon` credentials and no party profile.

The guest later upgrades in place with `POST /v1/me/upgrade` (email, password, given name, family name): the same principal and everything it owns become a registered user with its own person record and a primary email identifier, and the `anon` credential is revoked, which ends the guest's sessions, so the client signs in again with the email and password it just registered. Both flows are refused with `403 users.byo_identity_disabled` when the platform does not allow bring-your-own identities.

---

### 16. Manage own SSH public keys (self-service)

**Actor:** Authenticated end user (not a guest).

**Action:** Calls `GET /v1/me/credentials` to list the caller's credentials, `POST /v1/me/credentials/ssh-keys` with an `authorized_keys`-format public key (and an optional `label`) to register a new one, and `DELETE /v1/me/credentials/{uuid}` to revoke one.

**Outcome:** SSH keys are one credential kind (`ssh_key`). Registration validates the key against the module's fixed algorithm-acceptance policy (see [Security requirements](#security-requirements)) and rejects a refused type or malformed input with `400`. A duplicate active key — already registered to the caller or to any other principal — is rejected with `409` (`users.ssh_key_in_use`), without revealing who holds it. Revocation revokes the credential (it is never deleted); the key stops resolving on the caller's very next SSH connection. Registering and revoking are self-service writes: they authorize on the session's principal being the target and require the session's `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE` (default 10 minutes), otherwise the action-required `409 users.step_up_required` is returned and the client steps up and retries once, as in use case 8 — there is no `X-Step-Up-Token` header and no per-deployment switch. `@moduleforge/users-gui`'s `CredentialsPanel` component provides a standard, ready-made UI for this self-service flow, with the step-up prompt supplied by its `StepUpDialog` (see [use case 14](#14-gui-component-rendering-and-demo-app)); calling the endpoints above directly, without that component, remains fully supported.

---

### 17. Administer a principal's credentials (operator)

**Actor:** Authenticated operator or owner holding the `credentials` operation on the target principal's entity.

**Action:** Calls `GET /v1/principals/{uuid}/credentials` (list active credentials, never a hash or secret), `POST /v1/principals/{uuid}/credentials/api-keys` (issue an API key to a service account), `POST /v1/principals/{uuid}/credentials/ssh-keys` (install an SSH key) and `DELETE /v1/principals/{uuid}/credentials/{credential_uuid}` (revoke a credential, with reason `admin`).

**Outcome:** The `credentials` operation carries a ten-minute `max_auth_age`, so a stale authentication is `409 users.step_up_required`; installing a key is credential-install, access-granting authority, which is why it is its own operation rather than `update`, and `manage` confers it because it implies `credentials`. An API key's `client_secret` is shown once and stored only as a hash. A caller addressing its own principal is refused with `403` for every caller, service accounts included: self-service changes stay on `/v1/me`, and keys are rotated by their owner or an operator. An impersonating session is refused `403 users.impersonation_not_allowed`. Revoking a credential also ends every session that holds it as a factor; there is no last-credential check on the administrator door.

---

### 18. Work across profiles and sessions

**Actor:** Authenticated end user holding more than one profile (a personal profile and party profiles).

**Action:** Reads `GET /v1/session`, switches the acting profile with `POST /v1/session/switch`, opens a new browser tab's refresh lineage with `POST /v1/session/fork`, refreshes with `POST /v1/auth/token`, lists and revokes live sessions with `GET /v1/me/sessions` and `DELETE /v1/me/sessions/{uuid}`, and signs out with `POST /v1/session/logout`.

**Outcome:** Each sign-in lands on one profile (the credential's default profile, then the user's default, then the personal profile, then the only available one) and lists the others with an `available` flag and, when locked, a lock reason and remedy. A switch returns a new access token for the same session and writes nothing, so several tabs act as different profiles under one session. Each tab holds its own refresh lineage; reusing a spent refresh token revokes the session (`401 users.refresh_reuse`). Revoking a session, disabling a principal, or revoking one of the session's factor credentials takes effect on the very next request of any token of that session (`401 users.session_revoked`). Machines authenticate with `POST /v1/auth/token` and `grant_type: client_credentials`, which issues a short-lived token without a session or refresh token.

## General features

- **All authenticated endpoints require a valid bearer access token.** The token is passed as `Authorization: Bearer <token>`. No route reads an authentication cookie. Endpoints explicitly marked `security: []` in the OpenAPI spec (health probes, the `/v1/auth` sign-in and token routes, provider listing, bootstrap) are the only public endpoints.

- **Every request is re-checked against the database.** The access token is a carrier for a server-side session (or a non-interactive credential); the session or credential and the acting profile are checked on every request in one round trip, so a revocation, a disabled principal or a party policy change takes effect on the next request. See [Sessions and tokens](./architecture/sessions-and-tokens.md#per-request-liveness).

- **All errors return a structured payload**: a top-level `error` object with a machine-readable `code` (drawn from a reserved, closed vocabulary — `unauthenticated`, `forbidden`, `not_found`, `invalid_input`, `conflict`, `internal_error`), a human-readable `message`, and an optional `details` array of `{field, code, message}` entries carrying finer-grained, module-namespaced distinctions (e.g. `users.identifier_taken`) — `{"error": {"code": ..., "message": ..., "details": [...]}}`. See [`api/openapi.yaml`](../api/openapi.yaml)'s `Error`/`FieldError` schemas for the authoritative shape.

- **Some responses signal an out-of-band action instead of an error — navigate, don't alarm.** A top-level `action` object, mutually exclusive with `error`, carries a module-namespaced `code` (drawn from a closed, per-module set), a `message` that is a prompt to act rather than an error, `path` (an application-relative navigation target), and an optional `data` object of kind-specific auxiliary state — `{"action": {"code": ..., "message": ..., "path": ..., "data": ...}}`. On receiving an `action` response the client navigates the user to `action.path` instead of surfacing an error banner. `mod-users` registers three action codes: `users.email_unverified` (`403`, path `/verify-email`, no `data`), `users.step_up_required` (`409`, path `/step-up`, no `data`; a client library may handle it in place rather than navigate, as `StepUpDialog` does), and `users.oidc_not_confirmed` (`503`, path `/oidc-config`, `data.state` carrying the deployment boot state). See [`docs/mf-standards/architecture/api-response-design.md`](./mf-standards/architecture/api-response-design.md)'s "Action-required responses" section for the authoritative contract.

- **All responses use JSON.** Request bodies where required are JSON. Content-type negotiation is not supported.

- **All list endpoints are paginated** using `limit` (max 200, default 20) and `offset` query parameters. Responses include a `total` count.

- **Deletion is by revocation or archiving.** Credentials are revoked (`revoked_at`), never deleted; sessions are revoked; profiles end; entities are archived via `archived_at`. No hard deletes are exposed through the API.

- **All write operations are audited.** Mutating API operations produce audit log entries recording before/after state, the acting profile and principal, the session or credential, and the impersonator when there is one. Sign-in creating a session is `login`, session revocation is `revoke`, impersonation start is `impersonate`; refresh rotation, fork and profile switch write none.

- **Anti-enumeration responses.** Email-code request and password-reset request endpoints always return `204` regardless of whether the submitted email matches a known account, preventing account existence probing.

- **Impersonation preserves the administrator's identity.** An impersonation token carries the administrator's principal uuid in `act.sub`, and the audit log records the administrator as the impersonator on every request made inside the session, so actions under the impersonated profile trace back to the acting administrator. The impersonation token is never refreshed or replayed as the administrator.

- **OIDC provider configuration merges env and DB.** Environment variables (prefixed `AUTH_PROVIDER_{ID}_*`) supply defaults; `oidc_providers` DB rows are overlaid at runtime. Any column that is NULL in the DB row means "no override — use env value." Deleting a DB row fully reverts to env.

- **SSH public keys are a standing credential, not a session token.** A registered key authenticates a git-over-SSH session, not this module's own HTTP API. A key is globally unique among active credentials (enforced in the schema); revocation is immediate (the resolver caches nothing and excludes revoked or expired keys and archived or disabled principals); registration is validated against a fixed algorithm-acceptance policy. See [Security requirements](#security-requirements) for the full policy.

- **The data model is normalized per ModuleForge conventions.** Internal identifiers are integers (never exposed in API responses); external identifiers are UUIDs. Every table lives in the `mod_users` schema. A `user` is a mod-core principal; it references `natural_persons` (its own person record) and `legal_entities` (the party that manages it) from `mod-core`.

## Data model

The module owns its tables, all in the `mod_users` Postgres schema and all managed by the single baseline migration `model/migrations/sql/0100_baseline.sql`; Go query code is generated by sqlc. See [docs/architecture.md's Data model section](./architecture.md#data-model) for the schema-placement rationale, the database invariants (`MF101` to `MF103`) and the operational requirements for composing hosts.

| Table | Purpose |
|---|---|
| `users` | The `user` principal subtype (mod-core owns the `user` entity type and the `principals` row). Holds `person_id` (the user's own `natural_person`, null for a guest or a managed user), `managing_party_id` (immutable; the party that owns and manages a managed user), `is_guest` (may only go true to false), `default_profile_id` and `default_app_id` (against mod-core's `apps` table). |
| `user_identifiers` | Email (and phone and username) identifiers of a user: `value_canon` (email: trimmed, lowercased), `verified_at`, `is_primary`. `UNIQUE (kind, value_canon)`; at most one primary email per user. Only users have identifiers. A read surface for other modules (billing, tokens). |
| `credential_kinds` | Seeded registry of credential kinds with class, factor class, base `acr`, `amr` and whether a service account may hold the kind. |
| `credentials`, `cred_password`, `cred_email_otp`, `cred_oidc`, `cred_ssh_key`, `cred_api_key`, `cred_anon` | One `credentials` row per credential of a principal (label, optional confinement to a party, optional default profile, expiry, revocation with reason) plus one per-kind subtable row. Partial unique indexes make an OIDC `(issuer, subject)` pair and an SSH key fingerprint unique among active credentials. |
| `auth_challenges` | Time-limited, single-use challenges: emailed codes (`email_code`, `verify_identifier`, `step_up`; `failed_attempts` counts wrong guesses), password-reset tokens and OIDC `login_handoff` codes. Request cooldown is enforced by `create_challenge_with_cooldown`. |
| `oidc_config` | Singleton operator-level OIDC configuration (opt-out flag, setup-token hash). |
| `oidc_providers` | Per-provider DB overrides (display name, issuer URL, client ID/secret, scopes, enabled flag) plus the party the provider is bound to (`party_id`), its just-in-time mode (`jit_mode`) and `trust_acr`. |
| `party_authn_policies` | A party's `credential_rule` and `min_acr`. No row means `any` and `min_acr` 1. |
| `credential_party_authorizations` | The credentials authorized for a party's `authorized` rule, with who authorized them. |
| `sessions`, `session_factors`, `session_refresh_tokens` | Sessions (the revocation unit: `acr`, `amr`, `auth_time`, expiry, revoke reason, impersonation columns), the credentials that authenticated each, and the per-tab refresh tokens. |
| `auth_signing_keys` | The single persisted Ed25519 access-token signing key, used when `JWT_SIGNING_KEY` is not supplied. |

Principals, profiles, party records, system actors (including the anonymous actor), `platform_settings` and `profile_policy_confirmations` are defined in `mod-core/model` and apps in mod-core's `apps`; they are composed into the migration set at the application level, not duplicated here. This module's manifest declares `after: [core]` so mod-core's migrations run before this module's, and the foreign keys into those tables resolve at composition time.

## API definition

The HTTP API is versioned under `/v1/`. The full OpenAPI 3.0 definition is at [`api/openapi.yaml`](../api/openapi.yaml); the capability checklist below is the normative summary.

### Health

- [ ] `GET /healthz` — liveness probe (no auth, always `200`).
- [ ] `GET /readyz` — readiness probe (no auth, `200` when DB is reachable, `503` otherwise, with a fixed body `{"status":"not_ready","db":"unavailable"}` and no error detail).

### Authentication (no auth required)

- [ ] `POST /v1/auth/register` — create a user with email, password, given name, family name; returns the token response.
- [ ] `POST /v1/auth/login` — authenticate with email and password; returns the token response.
- [ ] `POST /v1/auth/anonymous` — create a guest user (or sign a guest back in with its `device_token`); returns the token response and, on creation, a one-time `device_token`.
- [ ] `POST /v1/auth/email-code/request` — request a one-time login code (always `204`).
- [ ] `POST /v1/auth/email-code/verify` — verify the code; `200` with the token response. `401` on a wrong, expired, consumed, or exhausted code.
- [ ] `POST /v1/auth/password-reset/request` — request a password-reset email (always `204`).
- [ ] `POST /v1/auth/password-reset/confirm` — set a new password with a reset token (does not sign in).
- [ ] `POST /v1/auth/token` — `grant_type` `refresh_token` (rotate a tab's refresh token and name the profile to act as) or `client_credentials` (an API key; no session, no refresh token).
- [ ] `POST /v1/auth/handoff` — exchange the one-time OIDC handoff `code`, with the `verifier` of the challenge sent at start, for the token response.
- [ ] `GET /v1/auth/providers` — list enabled OIDC providers (public metadata only; never returns client secrets).
- [ ] `GET /v1/auth/oidc/{provider}/start` — begin OIDC authorization-code flow; redirects to provider.
- [ ] `GET /v1/auth/oidc/{provider}/callback` — complete OIDC flow (sign in, link or step up); redirects to the frontend with `#handoff=`, `#linked=1`, `#stepped_up=1` or `#error=`.

mod-users also exports `resolveOrAnonymous` — opt-in middleware, not mounted on any route in this module — that other ModuleForge modules can add to their own `scope: public` route groups to accept a genuinely tokenless request (no `Authorization` header at all) and still resolve a real, zero-authority actor identity before their authorization checks run. See [Security requirements](#security-requirements) for the full behavior and required preconditions.

### Bootstrap (no auth required)

- [ ] `GET /v1/bootstrap/status` — whether the deployment is bootstrapped and its OIDC configuration confirmed.
- [ ] `POST /v1/bootstrap` — one-time first-run bootstrap with the setup token in `X-Setup-Token` (`401 users.setup_token_invalid`, checked before the body; `409 users.already_bootstrapped`); `201` with a token response landing on the administrator profile.

### OIDC configuration

- [ ] `GET /v1/oidc-config/status`, `POST /v1/oidc-config/confirm`, `GET /v1/oidc-config/saved`, `POST /v1/oidc-config/providers`, `GET`/`PUT`/`DELETE /v1/oidc-config/providers/{id}` and the loopback `GET /v1/oidc-config/setup-token` — OIDC onboarding and provider management; the credential is an operator administrator session or, while OIDC is unconfirmed, the setup token.

### Session (authenticated)

- [ ] `GET /v1/session` — the session, the acting profile and the principal's profiles with their availability.
- [ ] `POST /v1/session/switch` — a new access token acting as another available profile of the same session (no session write; refused during an impersonation).
- [ ] `POST /v1/session/fork` — start a new refresh lineage for a new tab (refused during an impersonation).
- [ ] `POST /v1/session/step-up/password`, `POST /v1/session/step-up/email-code/request`, `POST /v1/session/step-up/email-code/verify`, `POST /v1/session/step-up/oidc/{provider}/start` — re-verify or add a factor to the current session; any refusal is the `409 users.step_up_required` action.
- [ ] `POST /v1/session/logout` — revoke the caller's session and its refresh lineages.

### Impersonations (authenticated)

- [ ] `POST /v1/impersonations` — start an impersonation of `profile_uuid` with a `reason` (needs `impersonate` on the profile and an authentication no older than ten minutes); returns a token response without a refresh token.
- [ ] `POST /v1/impersonations/current/renew` — extend the impersonation, up to `AUTH_IMPERSONATION_MAX` from its start; re-authorizes `impersonate` from the administrator's session.
- [ ] `DELETE /v1/impersonations/current` — end the impersonation.

### Me (authenticated; reachable with an unverified email)

- [ ] `GET /v1/me`, `PUT /v1/me` — read and update the caller's user record.
- [ ] `GET /v1/me/profiles`, `POST /v1/me/profiles/{uuid}/accept`, `/decline`, `/leave` — the caller's profiles and the holder's own invitation and departure actions.
- [ ] `GET`/`POST /v1/me/identifiers`, `DELETE /v1/me/identifiers/{uuid}`, `POST /v1/me/identifiers/{uuid}/verification`, `POST /v1/me/identifiers/{uuid}/verify`, `PUT /v1/me/identifiers/{uuid}/primary` — identifiers.
- [ ] `GET /v1/me/credentials`, `POST /v1/me/credentials/password`, `POST /v1/me/credentials/ssh-keys`, `POST /v1/me/credentials/oidc/{provider}/start`, `PATCH`/`DELETE /v1/me/credentials/{uuid}` — credentials (revoking the last interactive credential is `409 users.last_credential`).
- [ ] `GET /v1/me/sessions`, `DELETE /v1/me/sessions/{uuid}` — the caller's live sessions.
- [ ] `POST /v1/me/upgrade` — upgrade a guest to a registered user.

Every write above requires the session's `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE` and is authorized on the session's principal being the target; a stale authentication is the `409 users.step_up_required` action.

### Principals (authenticated)

- [ ] `GET /v1/principals` — list users and service accounts the caller may list (optional `kind`), through the row-scoped access functions.
- [ ] `GET /v1/principals/{uuid}` — read a principal (`404` for an unknown one and for one the caller may not read).
- [ ] `POST /v1/principals/{uuid}/disable`, `POST /v1/principals/{uuid}/enable` — disable (revokes the principal's sessions; needs `update` and a fresh authentication) and enable.
- [ ] `GET /v1/principals/{uuid}/credentials`, `POST .../credentials/api-keys`, `POST .../credentials/ssh-keys`, `DELETE .../credentials/{credential_uuid}` — credential administration under the `credentials` operation; refused for the caller's own principal and while impersonating.

### Parties and profiles (authenticated)

- [ ] `GET`/`POST /v1/parties/{party_uuid}/profiles` — list a party's profiles and invite a user.
- [ ] `POST /v1/parties/{party_uuid}/managed-users` — create a user owned and managed by the party.
- [ ] `GET`/`PUT /v1/parties/{party_uuid}/authn-policy` — read the effective policy and set `credential_rule` and `min_acr`.
- [ ] `GET /v1/parties/{party_uuid}/credential-authorizations`, `PUT`/`DELETE .../credential-authorizations/{credential_uuid}` — authorize or deauthorize a credential for the party.
- [ ] `PUT`/`DELETE /v1/parties/{party_uuid}/confinements/{credential_uuid}` — confine a credential to the party or clear that.
- [ ] `PATCH /v1/profiles/{uuid}` — suspend, resume or end a party profile.

### Audit log (admin auth required)

- [ ] `GET /v1/audit` — list recent audit entries with pagination and optional `resource_type` filter.
- [ ] `GET /v1/audit/{resource_type}/{resource_uuid}` — audit entries for a specific resource.

## Security requirements

- **Authentication:** All non-public endpoints require a valid signed access token passed as a bearer token. Access tokens are issued by this module and verified on every request, and each is a carrier for a server-side session (`sid`) or a non-interactive credential (`cid`), re-checked in the database on every request: a revoked session or credential, a disabled principal, an ended or suspended profile, or a profile locked by its party's authentication policy answers `401` (`users.session_revoked`, `users.profile_unavailable` with the lock reason). The token holds no `email`, role or anonymity claim.

- **App audiences:** A token addressed to an app (its `aud` is the app's slug) is accepted only while that app is registered and not archived, and `aud` must be in `AUTH_ACCEPTED_AUDIENCES` (or, when that is empty, the default audience or a registered app's slug). The per-request liveness check treats an archived app (`entities.archived_at` set) as an unregistered audience, so archiving an app stops its tokens being accepted on the next request. When the session's app is `invite_only`, the acting profile must also be granted `login` on the app (`403 users.app_login_not_granted`).

- **Token algorithm:** Access tokens are signed EdDSA/Ed25519 only; HS256 and the `JWT_ALG` switch are removed. The verifier pins `EdDSA` and the configured public keys, so `alg: none`, HS256 and any other algorithm are rejected, closing the HMAC-with-public-key confusion attack; `kid`, issuer and expiry are always enforced. Audience tokens (`typ: aud+jwt`) for offline verifiers are never accepted as bearer tokens. See [jwt-eddsa.md](./jwt-eddsa.md) for configuration, key rotation, and verify-only deployment.

- **Anonymous actor (tokenless requests).** mod-users resolves one well-known, zero-authority `system_actor` entity (slug `anonymous`, seeded by mod-core; see [Data model](#data-model)) and exports an opt-in `resolveOrAnonymous` middleware that other modules can compose onto a `scope: public` route group to resolve a genuinely tokenless request (no `Authorization` header at all) to that actor before any authorization check runs. That opt-in describes route-level usage only: `provides.middleware` entries are unconditional reachability roots in mfgen, so `resolveOrAnonymous` and the `anonymousActor` service it depends on are constructed at boot in every application that composes mod-users, whether or not that application's routes reference the middleware — making mod-core's seeded anonymous actor a hard boot dependency for every mod-users consumer, not only those adopting tokenless requests. It is a wholly separate mechanism from the guest-user flow above — see [key use case 15](#15-create-a-guest-account-and-optionally-upgrade-it) — sharing one actor across every tokenless caller, owning nothing, and never holding a grant. The actor's ownership is guaranteed by mod-core's owner rules (a system actor never owns an entity); its zero-grants property is instead verified once, at process boot, and is not itself a continuous database constraint. A presented-but-invalid credential is never downgraded to anonymous — only a genuinely missing `Authorization` header falls through; an invalid, expired or not-live token still receives `401`. This middleware must never be composed with `requireAuth` or `requireVerifiedEmail` on the same route group (the latter fails closed with a `500`, since the anonymous branch places no `Identity`). Every anonymous request shares the same actor id, so any route that opts in must sit behind a per-IP/CIDR rate limiter at the edge or in the composition root (mod-users documents this precondition but does not ship the limiter) and should enforce a hard maximum page size on list results. Any cache or CDN in front of an anonymous-enabled route must key on the `Authorization` header (`Vary: Authorization`). Because `audit_log.actor_entity_id` collapses to one value for all anonymous traffic, attribution for anonymous traffic relies on the request id and HTTP access logs rather than the audit log, and no module may authorize a mutating operation for the anonymous actor. A consuming module must add its own public-visibility policy to both its decorating `Authorizer` and its list-scoping query — the generated `accessible_*_ids_for_actor` SQL function returns the empty set for the anonymous actor by design.

- **CORS:** Default-deny. Only origins listed in `CORS_ORIGINS` receive CORS headers, and credentials are allowed only for such an explicit allow-list. An unset `CORS_ORIGINS` emits no CORS headers, except in `DEPLOY_MODE=local`, which allows `*` without credentials. A `*` in `CORS_ORIGINS` is a startup error in every mode except `local` (`serverless`, `k8s`, and `container-host` all count as production-like). See [cors.md](./cors.md) for the behavior change affecting deployments that relied on the former implicit wildcard.

- **Password storage:** Passwords (at least 12 characters) are stored as argon2id encoded strings. Plaintext passwords are never persisted or logged.

- **OIDC ID token verification:** On callback, the module verifies the ID token signature, issuer, audience, and nonce before trusting any claims, and resolves identities by `(issuer, subject)`, never by email: an email that matches an existing account is only ever a refusal (`account_exists_sign_in_and_link`), so a person who controls a matching address at a provider cannot capture an existing account. Provider client secrets are stored in environment variables or the `oidc_providers` DB table (plaintext, matching the env-var model); the GUI never reads client secrets back — only a `has_client_secret` boolean is surfaced. OIDC issuer discovery and JWKS fetches verify TLS certificates; skipping verification requires the explicit, local-development-only `WithInsecureSkipTLSVerify` verifier option (see [consumer upgrade notes](./consumer-upgrade-notes.md#oidc-issuer-tls-verification) and [OIDC troubleshooting](./oidc-troubleshooting.md#self-signed-issuer-certificate)). A provider's asserted `acr` counts toward a session's assurance only when the provider is configured with `trust_acr`.

- **Handoff code binding:** The one-time code the OIDC callback puts in the redirect fragment (`#handoff=<code>`) is bound to the browser that started the flow, PKCE style (S256). The callback stores the challenge from the signed state on the `login_handoff` challenge; `POST /v1/auth/handoff` requires the matching `verifier`, hashes it and compares in constant time. A missing verifier is `400`; a wrong or malformed one is `401 users.invalid_credentials` and does not spend the code, so an intercepted code is useless without the verifier that never left the originating browser. The code lives 60 seconds and is single-use; no token or refresh token is ever placed in a URL.

- **Open-redirect prevention:** The `return` query parameter on `GET /v1/auth/oidc/{provider}/start` is restricted to site-relative paths (must begin with `/`). Absolute URLs and protocol-relative references are rejected with `400`.

- **CSRF / state validation:** The OIDC flow uses a signed state token stored in a scoped cookie (`oidc_state`, scoped to `/v1/auth/oidc/`). The callback validates the cookie before processing the authorization code. The cookie is anti-forgery state, not an authentication cookie. The two authenticated OIDC start calls that set it (`POST /v1/me/credentials/oidc/{provider}/start` and `POST /v1/session/step-up/oidc/{provider}/start`) are a documented exception to the frontend contract's `credentials: 'omit'` rule: a client sends `credentials: 'include'` on exactly those two requests so the browser keeps the cookie, and both still carry the Bearer token.

- **Anti-enumeration:** Email-code and password-reset request endpoints return `204` unconditionally to prevent account existence probing.

- **Request order and existence masking.** Authenticated routes are mounted behind `requireAuth` and answer in this order: 401 (no live session or credential), 400 (stateless shape: path UUIDs, strict JSON body, field checks), 403 (authorization), 404 (lookup), then 409 or 422 (state conflicts). A principal, party or profile the caller may not read is masked as 404, the same as an unknown one, before any 403; the 403 is for an operation the caller may see but not perform. On the self-service writes the step-up gate (`409 users.step_up_required`) sits after the 400 shape checks and before authorization; on principal disable and enable and on a profile status change it sits after authorization. Documented exceptions: public and credential routes (`/v1/auth/*`) have no 401 gate and report credential failures as `401 users.invalid_credentials`; the OIDC config write routes accept a body `setup_token` as a credential, so a caller with no admin session and no `X-Setup-Token` header and a malformed body gets 400 first; `POST /v1/bootstrap` checks its `X-Setup-Token` header before reading the body; and `requireOIDCConfirmed` (503 `users.oidc_not_confirmed`) answers before any handler 400, even ahead of the 401, because it depends only on service state. The email-code and password-reset requests keep their unconditional `204` (anti-enumeration). Body decoding is strict everywhere: unknown fields, trailing data and an empty body give 400 (`invalid_json_body`); a body over 1 MiB gives `body_too_large`. A state conflict is reported after authorization: a duplicate identifier or credential gives 409 (`users.identifier_taken`, `users.ssh_key_in_use`, `users.oidc_identity_in_use`). Public auth 500s carry a generic message; the cause is logged server-side.

- **Email one-time code limits:** A code allows at most 5 verification attempts, after which it is exhausted. A new code request is refused within a 60 s cooldown of the newest challenge of the same kind and subject in any state (live, exhausted, or consumed), enforced atomically by `create_challenge_with_cooldown`. A code is single-use: it is consumed on success, and at the exhaustion boundary the consume fails closed, so a correct guess that holds an earlier attempt is refused when a concurrent final-attempt claim consumes the code. The public email-code endpoints serve sign-in only; verification and step-up codes are issued and checked through the authenticated identifier and step-up routes. The code-request endpoint always answers `204` after a ~200 ms floor; the send runs in a detached background goroutine bounded by a concurrency limit (16; excess requests are dropped and logged), a 20 s timeout, and panic recovery. **Accepted residual risk (documented trade-off).** A code lives 5 minutes and the 60 s cooldown permits up to about 5 live codes per subject, each with its own 5-attempt budget. That gives roughly 5 guesses per minute per account against a 10^6 code space, with no per-IP or per-account lockout. Separately, an attacker who knows an email can exhaust the newest code with 5 wrong guesses, so the victim cannot obtain a fresh code for up to 60 seconds (the cooldown counts exhausted codes), and can repeat this each minute. Mitigations considered but out of scope: a per-subject failed-attempt window across all live codes, and per-IP throttling on the public verify endpoint.

- **Step-up and assurance:** Assurance belongs to the session. Each credential kind has a base assurance level (`acr` 1 to 3) and an `amr` value; a session's `acr` is the highest effective level of its unrevoked factors, raised to 2 when two distinct factor classes (for example knowledge and possession) are present, and its `auth_time` is when a factor was last proved. An operation can require a minimum `acr` and a maximum authentication age (the `impersonate` and `credentials` operations carry ten minutes), a party can require a `min_acr`, and the self-service writes require `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE`; a session that falls short is refused with the action-required `409 users.step_up_required`. Step-up adds or re-verifies a factor in the current session (password, emailed code or OIDC), recomputes `acr`, resets `auth_time` and returns a fresh access token; there is no step-up token. The emailed step-up code is subject to the same limits as the email one-time code above (5 attempts, 60 s cooldown, single-use consumption). Requests that read `acr` and `auth_time` always take them from the session row, never from the token, so a step-up or factor revocation applies immediately.

- **Owning-party gate:** A party's authentication policy (`credential_rule`, `min_acr`) is enforced per request on outside actors as well as on its own members. When the acting profile is outside the target's owning party (an outside collaborator reaching the party's content through actor groups or grants, or a personal profile reaching it through the SSH fallback), the authorization engine requires the session's factors (or the key's credential) to meet that party's `credential_rule` and `min_acr`, and refuses with `403` otherwise. A party record is its own owning party: when the target is itself a party, it is gated by its own policy as well as by its direct owner's, so an outside collaborator holding `update` on a restrictive party's record cannot change that party (for example its authentication policy) from a session that does not meet it. Nested ownership is one level only (the target, when it is a party, and its direct owner; never the owner's owner). Platform operator profiles are exempt from the owning-party gate: a customer party's policy never binds the operator, so the platform can always administer, support and impersonate; operation-level `min_acr` and step-up still apply, and non-operator outside collaborators stay gated. A profile confirmation (`profile_policy_confirmations`) exempts the `credential_rule` only, never `min_acr`. While impersonating, the impersonated actor's rules apply to every operation, so impersonation never skips a party's `credential_rule`: both the profile's own party gate and the owning-party gate are evaluated on the administrator's session (its factors for the `credential_rule`, its `acr` for `min_acr`), and the only exemption is an impersonation started through a platform operator profile, which skips a customer party's `credential_rule` (never `min_acr`) because an operator's credentials can never be authorized for a customer party. See [architecture.md](./architecture.md#api-layer).

- **Impersonation:** Impersonation is a separate child session of the administrator's own, never a switch of the administrator's profile. The impersonated actor's rules apply to every operation except stopping the impersonation; nested impersonation is disallowed by construction (a database invariant, `MF101`, in addition to the route refusing it); the impersonation token is never refreshed (no refresh token is issued) and is never replayed as the administrator; logging out of a tab that is impersonating revokes the administrator session (and with it the impersonation) rather than leaving the administrator signed in. An impersonating context is refused credential administration, managed-user creation, party policy and credential authorization writes, and ending a profile. A planned, not yet built, general mechanism would mark data `no-write`, `no-read` or `no-access` for impersonation and run the impersonation check only when a function touches marked data; until it exists, the explicit refusals above are the protection. See [Impersonation sessions](./architecture/sessions-and-tokens.md#impersonation-sessions).

- **SSH public-key authentication (git-over-SSH access):** A registered key is a standing, non-interactive `ssh_key` credential for the git-over-SSH surface a composing application (for example `app-mfgit`) mounts, resolved to its credential and principal via the exported `sshKeyResolver` service (`ResolveCredential`), with `AuthContextForOwner` deriving the acting profile from the repository's owner — see [docs/architecture.md's API layer](./architecture.md#api-layer) and [docs/architecture/ssh-keys.md](./architecture/ssh-keys.md).
  - **Algorithm policy.** Input is a single `authorized_keys`-format line. Accepted key types: Ed25519, the FIDO `sk-` variants, ECDSA P-256/384/521, and RSA of at least 2048 bits. Refused at registration with `400`: DSA (`ssh-dss`), an under-strength RSA key (under 2048 bits, `users.ssh_key_too_weak`), any certificate key type or other unsupported type (`users.ssh_key_type_unsupported`), and a line carrying `authorized_keys` options, more than one key, or otherwise malformed input (`users.ssh_key_invalid`). A `label` over 100 characters is refused with `users.ssh_key_label_too_long`. A supplied `label` containing a control character or any Unicode bidi-formatting (`Bidi_Control`) character is refused with `users.ssh_key_label_invalid`; the same characters are stripped, not rejected, from a comment-derived default label when no `label` is supplied. The policy is fixed in code, not configurable.
  - **Global uniqueness.** A key resolves to at most one active credential across the whole module, enforced by a partial unique index on the fingerprint, not a Go-level pre-check. A duplicate active registration — by the same principal or a different one — is rejected with `409` (`users.ssh_key_in_use`) without revealing who already holds the key.
  - **Immediate revocation.** The resolver performs a fresh, uncached, indexed lookup on every call and excludes revoked and expired keys and keys whose principal entity is archived or whose principal is disabled, so a revoked key — or a deleted or disabled principal's keys — stops authenticating on the caller's very next SSH connection. An already-open session is not terminated; that is transport behavior outside this module.
  - **Registration and assurance.** Registering and revoking a key are credential writes: self-service on `/v1/me/credentials` (principal is the target; `auth_time` within `AUTH_SELF_SERVICE_MAX_AUTH_AGE`) or administrator-side on `/v1/principals/{uuid}/credentials/ssh-keys` (the `credentials` operation, ten-minute `max_auth_age`, refused for the caller's own principal and while impersonating). Both answer a stale authentication with `409 users.step_up_required`; there is no `X-Step-Up-Token`. Listing is not gated by freshness on the self-service door.
  - **Profile derivation.** A key acts as the principal's active profile in the repository's owning party when it has one and the credential is available to it (party gate, confinement, `min_acr` against the key's base level; a rule that requires an OIDC provider always refuses a key), otherwise as the personal profile unless the key is confined to a party; a refusal is `ErrSSHKeyNotAuthorized` naming the party.
  - **Existence masking.** A credential that does not exist, belongs to another principal or is already revoked is the same `404` on the administrator routes, and a principal the caller may not read is indistinguishable from an unknown one.

- **Authorization:** The authorization engine's only subject is the acting profile (under impersonation, the impersonated profile). A request is admitted through a wildcard grant, an entity grant (a grant on a type's entity also confers the operation over every instance of exactly that type), an owned-scope grant on the target's owner, or the own arm, which admits a personal profile's holder to the records the holder owns for the operations marked `ownership_confers` (never `impersonate`, `manage` or `login`); a prelude first applies the operation-level assurance gate and the owning-party gate. Authorization follows the ModuleForge `Authorizer` pattern described in [mod-core's authorization design](./mf-standards/architecture/authorization-design.md). Creating a user or a profile within a party (`create` on the `user` or `profile` type within the owning party) is authorized with `AuthorizeTypeIn`: it requires a wildcard grant, a grant on the exact type's entity, an owned-scope grant on the party, or the own arm, and fails closed when the authorizer cannot answer type-level checks. A grant on a type's entity also authorizes the same operation on every existing and future instance of exactly that type, which makes such grants instance-wide; see the warning in [D13](./architecture.md#key-decisions). Listing principals is authorized by the generated list-access functions: a type-level `list` grant on the `user` type lists every user including each user's primary email address, so grant it only to principals trusted with every user's email; see the warning in [D12](./architecture.md#key-decisions).

- **System actors, unmarked contexts and list filtering:** The authorization engine's assurance and owning-party gates are bypassed only for the anonymous context and for an explicit system-actor context (`opctx.WithSystemActor`, for trusted system flows only, never a user's profile). A context with no session and no credential is not trusted by default: without the marker it is gated and fails closed, and mod-users' own unauthenticated flows (sign-up, password reset, OIDC just-in-time creation, bootstrap) build such unmarked contexts for real users and never call `Authorize`. The marker is not an operator signal; the operator exemption is keyed on the acting profile. List endpoints apply the same owning-party gate per row, through the SQL predicate `mod_users.access_row_permitted(p_actor_entity_id, p_entity_id, p_owner_id, p_session_id, p_credential_id, p_system_actor)`: a row owned by a party whose authentication policy the caller's session does not meet is omitted from the list, consistent with the single-row refusal. A host app installs the access functions through `localAuthz.AccessFuncGenerator()` and binds each list transaction with `setup.BindAuthInputs`; an unbound list fails closed. See [architecture.md](./architecture.md#api-layer).

- **Audit trail:** All mutating operations are audited with before/after state, the acting profile and principal, the session or credential, and timestamp. Impersonated actions record both the administrator (as the impersonator) and the impersonated profile.

## Non-goals

- **Single sign-on (SSO) provider / OIDC issuer.** This module consumes OIDC providers (Google, Microsoft); it does not implement an OIDC issuer itself. Access tokens are local to this module and are not issued as OIDC tokens.

- **Role-based access control beyond grants.** There is no role or admin flag on a user. Authority is the acting profile's grants (and the own arm); the module does not define or interpret application roles.

- **Email delivery.** The module generates codes and tokens for email-code login, verification, and password reset. It does not implement or bundle an email transport; the application provides the mail sender.

- **User interface routing and application shell.** The `gui/` component library provides components, including page components for the backend-fixed paths (`/verify-email`, `/oidc-config`, `/reset-password`) and a `USERS_GUI_ROUTES` path constant; it does not provide routing, route files, navigation state, or an app shell — the consuming app owns the routes and injects navigation (see [use case 14](#14-gui-component-rendering-and-demo-app)). The `app-mfdemo` Next.js project (at the aggregate level) shows one way to compose the components but is not production application code.

- **Multi-tenancy beyond parties and app access.** Parties (organizations and natural persons), their profiles and the `login` grant that gates invite-only apps are available, but the module does not enforce cross-tenant data isolation beyond the grants and the owning-party gate; that is an application composition concern.

- **Rate limiting and lockout.** Per-account lockout and per-IP throttling of the public sign-in and code endpoints are not provided; see the residual-risk note under the email one-time code limits.

## Pointers to deeper docs

- [docs/architecture.md](./architecture.md) — system design, sub-project relationships, OIDC flow internals, and key design decisions.
- [docs/architecture/sessions-and-tokens.md](./architecture/sessions-and-tokens.md) — sessions, the access-token carrier, per-request liveness, refresh lineages, step-up, impersonation and the setup-token bootstrap.
- [docs/project-structure.md](./project-structure.md) — directory layout and sub-project conventions.
- [docs/oidc-troubleshooting.md](./oidc-troubleshooting.md) — step-by-step checklist for OIDC login failures caused by IdP configuration mismatches.
- [AGENTS.md](../AGENTS.md) — build, test, and development commands for contributors and AI agents.
- [api/openapi.yaml](../api/openapi.yaml) — authoritative OpenAPI 3.0 definition for the HTTP API.
- [README.md](../README.md) — consumer-facing project introduction and installation.
