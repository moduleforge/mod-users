# users-module Specification

## Purpose and scope

This document is the canonical functional specification for `@moduleforge/users-module`. It describes what the module does and is required to do — the behavioral contracts an implementation must satisfy. It is written for developers and AI agents implementing, modifying, or integrating the module.

This spec assumes familiarity with the ModuleForge module model described in [core-module's architecture overview](https://github.com/moduleforge/core-module/blob/main/docs/architecture.md). It does not repeat cross-cutting design rationale (authorization, state management, entity typing, database conventions) that is documented there.

The spec covers three sub-packages — `model`, `api`, and `gui`. A demo application (`app-mfdemo`) that exercises the module end-to-end is a separate project at the aggregate level. For design choices and the *how* of each sub-package, see [docs/architecture.md](./architecture.md). For directory layout and sub-project conventions, see [docs/project-structure.md](./project-structure.md). For build, test, and development commands, see [AGENTS.md](../AGENTS.md).

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

**Action:** Submits email address, password, given name, and family name to `POST /v1/auth/register`.

**Outcome:** A new `user_accounts` row is created, linked to a new `legal_entities` row (natural person), with an `auth_local` credential record storing an argon2id password hash. The response carries a signed JWT. If the email is already in use, the request is rejected with `409`.

---

### 2. Log in with email and password

**Actor:** Registered end user with a local credential.

**Action:** Submits email and password to `POST /v1/auth/login`.

**Outcome:** Credentials are verified against the stored argon2id hash. On success, a signed JWT is returned. On failure, `401` is returned. The response does not distinguish wrong email from wrong password (no account enumeration).

---

### 3. Log in via email one-time code

**Actor:** Registered end user.

**Action:** Requests a code with `POST /v1/auth/email-code` (providing email), then submits the code via `POST /v1/auth/email-code/verify`.

**Outcome:** The code request always returns `202` regardless of whether the email matches a known account (prevents enumeration). A hashed, time-limited code is generated and sent to the email address. On successful verification, the code is marked consumed and a JWT is returned. Expired or already-consumed codes are rejected with `401`.

---

### 4. Reset a forgotten password

**Actor:** Registered end user with a local credential.

**Action:** Requests a reset email with `POST /v1/auth/password-reset` (providing email), then submits the reset token and new password to `POST /v1/auth/password-reset/confirm`.

**Outcome:** The reset request always returns `202` (prevents enumeration). A hashed, time-limited reset token is generated and sent to the email address. On successful confirmation, the `auth_local` password hash is updated, the token is marked consumed, and `200` is returned. Invalid or expired tokens are rejected with `401`.

---

### 5. Log in via OIDC provider (OAuth 2.0 authorization-code flow)

**Actor:** End user with an account at a configured OIDC provider (e.g. Google, Microsoft Entra ID).

**Action:** Browser is directed to `GET /v1/auth/oidc/{provider}/start`, which generates a signed state token, sets a scoped cookie, and redirects to the provider's authorization endpoint. After the user authenticates at the provider, the browser is redirected back to `GET /v1/auth/oidc/{provider}/callback`.

**Outcome:** The callback handler validates the state cookie, exchanges the authorization code for an ID token, verifies the token (signature, issuer, audience), normalizes claims, and resolves or creates the user account. On success, the browser is redirected to the configured return URL with the JWT and return path in the URL fragment. On error, the redirect carries an error code in the query string. The `return` query parameter on the start endpoint restricts to site-relative paths to prevent open-redirect attacks.

---

### 6. Link an OIDC identity to an existing account

**Actor:** Authenticated end user or the OIDC callback handler.

**Action:** On OIDC callback, if the ID token's email matches an existing `user_accounts` row that has no `auth_oidc_identities` row for this `(issuer, subject)` pair, the identity is linked to the existing account.

**Outcome:** A new `auth_oidc_identities` row is created associating the `(issuer, subject)` from the OIDC provider with the existing user account. Subsequent OIDC logins with the same provider resolve to the same account. A single user account may hold multiple OIDC identities (one per provider).

---

### 7. View and update own profile

**Actor:** Authenticated end user.

**Action:** Calls `GET /v1/self` to retrieve profile, or `PUT /v1/self` to update given name, family name, or default application preference.

**Outcome:** On `GET`, the caller's full profile is returned including entity kind, display name, and default app. On `PUT`, the provided fields are updated and the updated profile is returned.

---

### 8. Admin: manage user accounts

**Actor:** Authenticated admin user.

**Action:** Calls any of: `GET /v1/users` (list/search), `POST /v1/users` (create), `GET /v1/users/{uuid}` (retrieve), `PUT /v1/users/{uuid}` (update), `DELETE /v1/users/{uuid}` (archive, soft-delete), or `POST /v1/users/{uuid}/grant` (grant or revoke admin privileges).

**Outcome:** User list supports search by email or name with pagination. Create accepts optional password and admin flag. Archive sets `archived_at` on the underlying record (soft-delete; the record is preserved). Admin grants/revocations are reflected immediately on the `user_accounts` record. All mutating operations are audited.

---

### 9. Admin: assume a user's identity

**Actor:** Authenticated admin user.

**Action:** Calls `POST /v1/users/{uuid}/assume`.

**Outcome:** A JWT scoped to the target user is returned. The token records the admin's original identity so that actions taken under the assumed identity are audited as "admin acting as user". The admin's own session is unaffected.

---

### 10. Admin: manage applications

**Actor:** Authenticated admin user.

**Action:** Calls any of the `/v1/apps` endpoints: list, create, retrieve, update, archive (soft-delete), plus membership operations (`GET /v1/apps/{uuid}/members`, `POST /v1/apps/{uuid}/members`, `DELETE /v1/apps/{uuid}/members/{user_uuid}`).

**Outcome:** Applications are tenant-like groupings that users can be assigned to with roles. An archived application is soft-deleted (`archived_at` set). Members can be added or removed; each membership record carries a `roles` array.

---

### 11. Admin: configure OIDC providers

**Actor:** Authenticated admin user (or operator using the setup token flow when no admin account yet exists).

**Action:** Uses the OIDC configuration UI (served by `app-mfdemo`) to add, edit, or disable OIDC providers. Changes are persisted to `oidc_providers`. The "Test configuration" flow exercises a full round-trip against the provider without affecting the admin's session and without creating user records.

**Outcome:** The effective provider configuration is the merged result of environment variables and any `oidc_providers` DB overrides — DB rows take precedence. The `oidc_config` singleton tracks whether OIDC is opted out entirely, and holds the setup-token hash used to authorize the initial confirm step before any admin account exists.

---

### 12. View audit log

**Actor:** Authenticated admin user.

**Action:** Calls `GET /v1/audit` (all recent entries), `GET /v1/users/{uuid}/audit` (entries by actor), or `GET /v1/audit/{resource_type}/{resource_uuid}` (entries by target resource). All endpoints support pagination and optional `resource_type` filtering.

**Outcome:** Paginated audit entries are returned. Each entry records action, resource type, resource UUID, actor UUID, before/after state snapshots, and timestamp.

---

### 13. GUI component rendering and demo app

**Actor:** Developer or AI agent composing a ModuleForge-based application.

**Action:** Imports components from `@moduleforge/users-gui` (via npm, Bun workspace, or yalc local link) and composes them into an application shell.

**Outcome:** Components render the auth, profile, admin, and OIDC-config surfaces. The `app-mfdemo` Next.js project (at the aggregate level) demonstrates all components in a working context; it is the component showcase (the role Storybook plays in other projects). The demo app requires the API running locally.

---

### 14. Create an anonymous account and optionally upgrade it

**Actor:** Unauthenticated end user (typically a client application acting on behalf of a new visitor with no credentials).

**Action:** Submits a `device_id` (stable device fingerprint) to `POST /v1/auth/anonymous`.

**Outcome:** A new `user_accounts` row is created with a NULL email address. A corresponding `anon_tokens` row is created, associating the `device_id` with the new account via a SHA-256-hashed session token. The response carries a signed JWT (with `is_anonymous: true` claim) and the raw session token. The session token can be supplied by the client on a later visit to recover the same anonymous identity across sessions, keyed by `device_id`.

When the anonymous user later provides an email address (via `PUT /v1/self`), the account is upgraded: the `email` column is set to the provided value, `is_anonymous` becomes `false`, and all `anon_tokens` rows for that account are deleted. Subsequent auth flows (login, email-code, password-reset) that require an email address guard against anonymous accounts and return `400` with code `anonymous_account` if called with an anonymous JWT.

## General features

- **All authenticated endpoints require a valid bearer JWT.** The token is passed as `Authorization: Bearer <token>`. Endpoints explicitly marked `security: []` in the OpenAPI spec (health probes, auth endpoints, provider listing) are the only public endpoints.

- **All errors return a structured payload** with machine-readable `code` and human-readable `message` fields.

- **All responses use JSON.** Request bodies where required are JSON. Content-type negotiation is not supported.

- **All list endpoints are paginated** using `limit` (max 200, default 20) and `offset` query parameters. Responses include a `total` count.

- **All destructive admin operations are soft-deletes.** Users and applications are archived via `archived_at` timestamp; no hard deletes are exposed through the API.

- **All write operations are audited.** Mutating API operations produce audit log entries recording before/after state, the actor UUID, and the affected resource.

- **Anti-enumeration responses.** Email-code request and password-reset request endpoints always return `202` regardless of whether the submitted email matches a known account, preventing account existence probing.

- **Admin identity is preserved on assume.** Tokens issued by the assume endpoint embed the admin's original UUID so audit log entries under the assumed identity trace back to the acting admin.

- **OIDC provider configuration merges env and DB.** Environment variables (prefixed `AUTH_PROVIDER_{ID}_*`) supply defaults; `oidc_providers` DB rows are overlaid at runtime. Any column that is NULL in the DB row means "no override — use env value." Deleting a DB row fully reverts to env.

- **The data model is normalized per ModuleForge conventions.** Internal identifiers are integers (never exposed in API responses); external identifiers are UUIDs. `user_accounts` references `legal_entities` (a `core-module` concept) to enforce that only natural persons and corporations — not service accounts — may hold user accounts.

## Data model

The module owns nine Postgres tables. All are managed by goose migrations under `model/migrations/`; Go query code is generated by sqlc.

| Table | Purpose |
|---|---|
| `apps` | Application tenants (name, slug, soft-delete). |
| `user_accounts` | One row per user. References `legal_entities(entity_id)` (core-module); holds the canonical email (nullable — NULL for anonymous accounts), email-verified timestamp, and default-app FK. The derived boolean `is_anonymous` (email IS NULL) is exposed in Go types and API responses. |
| `anon_tokens` | Device continuity tokens for anonymous users. Maps `device_id` + SHA-256-hashed `session_token` → `user_account`. Rows are cascade-deleted when the parent `user_account` is hard-deleted, and are explicitly deleted by the service when an anonymous account is upgraded (email patched from NULL to a real value). |
| `auth_local` | Local credential (argon2id hash) for email+password login. One row per user account, optional. |
| `email_codes` | Time-limited, single-use codes for email-code login and email verification. |
| `password_resets` | Time-limited, single-use tokens for password reset. |
| `apps_user_accounts` | Membership join table: app ↔ user account, with a `roles` text array. |
| `oidc_config` | Singleton operator-level OIDC configuration (opt-out flag, setup-token hash). |
| `oidc_providers` | Per-provider DB overrides (display name, issuer URL, client ID/secret, scopes, enabled flag). |
| `auth_oidc_identities` | OIDC identity links: `(issuer, subject)` pairs keyed to a user account. Many per account (one per provider). |

The `legal_entities` table referenced by `user_accounts.account_holder` is defined in `core-module/model`; it is composed into the migration set at the application level, not duplicated here.

## API definition

The HTTP API is versioned under `/v1/`. The full OpenAPI 3.0 definition is at [`api/openapi.yaml`](../api/openapi.yaml); the capability checklist below is the normative summary.

### Health

- [ ] `GET /healthz` — liveness probe (no auth, always `200`).
- [ ] `GET /readyz` — readiness probe (no auth, `200` when DB is reachable, `503` otherwise).

### Authentication (no auth required)

- [ ] `POST /v1/auth/register` — create account with email, password, given name, family name; returns JWT.
- [ ] `POST /v1/auth/login` — authenticate with email and password; returns JWT.
- [ ] `POST /v1/auth/anonymous` — create an anonymous account (no credentials required); returns JWT with `is_anonymous: true` and a raw session token for cross-session continuity.
- [ ] `POST /v1/auth/email-code` — request a one-time login code (always `202`).
- [ ] `POST /v1/auth/email-code/verify` — verify code; returns JWT.
- [ ] `POST /v1/auth/password-reset` — request password-reset email (always `202`).
- [ ] `POST /v1/auth/password-reset/confirm` — set new password with reset token.
- [ ] `GET /v1/auth/providers` — list enabled OIDC providers (public metadata only; never returns client secrets).
- [ ] `GET /v1/auth/oidc/{provider}/start` — begin OIDC authorization-code flow; redirects to provider.
- [ ] `GET /v1/auth/oidc/{provider}/callback` — complete OIDC flow; resolves/creates user; redirects to frontend with JWT fragment.

### Self (authenticated)

- [ ] `GET /v1/self` — retrieve authenticated user's profile.
- [ ] `PUT /v1/self` — update own given name, family name, or default application.

### Admin: users (admin auth required)

- [ ] `GET /v1/users` — list/search users with pagination.
- [ ] `POST /v1/users` — create user; optional password and admin flag.
- [ ] `GET /v1/users/{uuid}` — retrieve user detail.
- [ ] `PUT /v1/users/{uuid}` — update user fields.
- [ ] `DELETE /v1/users/{uuid}` — archive user (soft-delete).
- [ ] `POST /v1/users/{uuid}/grant` — grant or revoke admin privileges.
- [ ] `POST /v1/users/{uuid}/assume` — obtain a JWT scoped to the user; original admin identity embedded.

### Admin: applications (admin auth required)

- [ ] `GET /v1/apps` — list applications.
- [ ] `POST /v1/apps` — create application (name, slug).
- [ ] `GET /v1/apps/{uuid}` — retrieve application detail.
- [ ] `PUT /v1/apps/{uuid}` — update application name.
- [ ] `DELETE /v1/apps/{uuid}` — archive application (soft-delete).
- [ ] `GET /v1/apps/{uuid}/members` — list application members.
- [ ] `POST /v1/apps/{uuid}/members` — add a member with optional roles.
- [ ] `DELETE /v1/apps/{uuid}/members/{user_uuid}` — remove a member.

### Admin: audit log (admin auth required)

- [ ] `GET /v1/audit` — list recent audit entries with pagination and optional `resource_type` filter.
- [ ] `GET /v1/users/{uuid}/audit` — audit entries where the user is the actor.
- [ ] `GET /v1/audit/{resource_type}/{resource_uuid}` — audit entries for a specific resource.

## Security requirements

- **Authentication:** All non-public endpoints require a valid signed JWT passed as a bearer token. JWTs are issued by this module and verified on every request. JWTs issued for anonymous accounts carry an `is_anonymous: true` claim; email-dependent auth operations (`login`, `email-code`, `password-reset`) reject these tokens with `400 anonymous_account`.

- **Password storage:** Passwords are stored as argon2id encoded strings. Plaintext passwords are never persisted or logged.

- **OIDC ID token verification:** On callback, the module verifies the ID token signature, issuer, audience, and nonce before trusting any claims. Provider client secrets are stored in environment variables or the `oidc_providers` DB table (plaintext, matching the env-var model); the GUI never reads client secrets back — only a `has_client_secret` boolean is surfaced.

- **Open-redirect prevention:** The `return` query parameter on `GET /v1/auth/oidc/{provider}/start` is restricted to site-relative paths (must begin with `/`). Absolute URLs and protocol-relative references are rejected with `400`.

- **CSRF / state validation:** The OIDC flow uses a signed state token stored in a scoped cookie (`oidc_state`, scoped to `/v1/auth/oidc/`). The callback validates the cookie before processing the authorization code.

- **Anti-enumeration:** Email-code and password-reset request endpoints return `202` unconditionally to prevent account existence probing.

- **Authorization:** Admin-only endpoints enforce admin privilege on the authenticated principal. The assume endpoint additionally enforces that the acting user is an admin. Authorization follows the ModuleForge `Authorizer` pattern described in [core-module's authorization design](https://github.com/moduleforge/core-module/blob/main/docs/architecture/authorization-design.md).

- **Audit trail:** All mutating operations are audited with before/after state, actor UUID, and timestamp. Assumed-identity actions record both the admin actor and the assumed user.

## Non-goals

- **Single sign-on (SSO) provider / OIDC issuer.** This module consumes OIDC providers (Google, Microsoft); it does not implement an OIDC issuer itself. Session JWTs are local to this module and are not issued as OIDC tokens.

- **Role-based access control beyond admin/non-admin.** The `apps_user_accounts.roles` array is a data field applications may use. This module does not define, enforce, or interpret role semantics beyond the binary `is_admin` flag.

- **Email delivery.** The module generates codes and tokens for email-code login, verification, and password reset. It does not implement or bundle an email transport; the application provides the mail sender.

- **User interface routing and application shell.** The `gui/` component library provides components; it does not provide routing, navigation state, or an app shell. The `app-mfdemo` Next.js project (at the aggregate level) shows one way to compose the components but is not production application code.

- **Multi-tenancy beyond app membership.** Application-level tenancy (the `apps` / `apps_user_accounts` model) is available, but the module does not enforce cross-tenant data isolation; that is an application composition concern.

## Pointers to deeper docs

- [docs/architecture.md](./architecture.md) — system design, sub-project relationships, OIDC flow internals, and key design decisions.
- [docs/project-structure.md](./project-structure.md) — directory layout and sub-project conventions.
- [docs/oidc-troubleshooting.md](./oidc-troubleshooting.md) — step-by-step checklist for OIDC login failures caused by IdP configuration mismatches.
- [AGENTS.md](../AGENTS.md) — build, test, and development commands for contributors and AI agents.
- [api/openapi.yaml](../api/openapi.yaml) — authoritative OpenAPI 3.0 definition for the HTTP API.
- [README.md](../README.md) — consumer-facing project introduction and installation.
