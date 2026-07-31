# mod-users architecture

## Overview

`mod-users` is a ModuleForge module that provides user identity, account management, and authentication as a composable unit. A host application integrates it by mounting the Go model migrations, wiring the Go API services, and importing the React component library. The module ships three sub-projects — `model`, `api`, and `gui` — each independently consumable and built to the ModuleForge module contract described in [mod-core architecture](./mf-standards/architecture.md). A demo application (`app-mfdemo`) that wires the module end-to-end lives in a separate project at the aggregate level.

### Runtime service dependencies

Beyond the cross-module *schema* composition described under [Data model](#data-model) below, `mod-users` also declares runtime *service*-level dependencies in `moduleforge.module.yaml`'s `requires.services` — Go service instances that a composing host application must supply (typically from `mod-core` and `authz-module`) before `mod-users`' own `provides.services` can be constructed. As of this writing, `requires.services` lists:

| Service | Source module | Used by | Purpose |
|---|---|---|---|
| `naturalPersonService` | mod-core | `userAccountService` | `coreservice.NaturalPersonServicer` — creates/manages the natural-person entity record backing a user account |
| `typeResolver` | mod-core | `userAccountService` | `*types.Resolver` — resolves and validates entity-type metadata for accounts |
| `coreServices` | mod-core | `selfHandler` | `*coreservice.Services` — the composite core-services aggregate; `selfHandler` calls its `Entity.GetSelf` to read the given/family name half of `/v1/self`'s composite identity response |
| `grantService` | authz-module | `firstUserGrant` hook | `GrantServicer` — issues the wildcard `manage` grant bootstrapped for the first user account created in a fresh database |
| `entityResolver` | mod-core | `appsHandler` | `*entity.Resolver` — resolves an `apps/{uuid}` path param to the app's internal id, since `mod-users` no longer owns an `apps` table of its own (see [Data model](#data-model)) |

`requires.infra` (`pool`, `opReg`, `smtp`, `cfg`) covers infrastructure singletons rather than services and is not enumerated here; see `moduleforge.module.yaml` directly for the current list. This table reflects the manifest at the time of writing — treat `moduleforge.module.yaml`'s `requires:` block as the source of truth if the two ever disagree.

## Sub-project layout

| Sub-project | Language | What it owns | What it exposes |
|---|---|---|---|
| `model/` | Go | Postgres schema, goose migrations, sqlc query code | Go package with typed query functions and model types |
| `api/` | Go | HTTP handlers, business-logic services, auth middleware | Mountable HTTP routes; Go service constructors |
| `gui/` | TypeScript / React | UI components for auth flows and user management | `@moduleforge/users-gui` npm package |
The sub-projects have a layered dependency: `api` imports `model`; `gui` is independent of the Go code. The demo application (`app-mfdemo`, a separate project at the aggregate level) depends on both the running `api` and the `gui` library.

## Data model

The Postgres schema lives in `model/migrations/` (managed with goose) and `model/schema/`. The sqlc-generated Go query code is in `model/db/`.

| Table | Purpose |
|---|---|
| `user_accounts` | Core user entity; promoted to `Entity` status for authorization (see [entity-typing](./mf-standards/architecture/entity-typing.md)). `email` is nullable — NULL indicates an anonymous account |
| `anon_tokens` | Maps `device_id` + `session_token` (SHA-256 hashed) to a `user_account`; enables cross-session identity continuity for anonymous users. Rows are deleted when the account is upgraded to a named account |
| `apps_user_accounts` | Many-to-many join between mod-core's `apps` and this module's `user_accounts` — the membership record; carries a `roles` text array. `apps` itself is not a mod-users table (see below) |
| `auth_local` | Email + argon2id password credentials for a user account |
| `email_codes` | Short-lived one-time codes used for email verification and passwordless login |
| `password_resets` | Pending password-reset tokens |
| `oidc_config` | Per-deployment OIDC configuration (enabled providers, default settings) |
| `oidc_providers` | Per-provider OIDC overrides (issuer URL, client ID/secret, stored in DB or env) |
| `auth_oidc_identities` | OIDC identity records linking an external subject (`sub`) to a `user_account` |

Internal IDs are integers (joins only, never sent in responses). External IDs are UUIDs. Cross-module schema dependencies (e.g., the `legal_entities` table from mod-core, referenced by `user_accounts.account_holder`) are resolved by the host application's migration composition step, not by tight coupling. `apps` is a second instance of this same pattern: it was previously a table this module owned outright, but application tenancy now lives in mod-core as an entity subtype, so `apps` is defined and CRUD-managed there. `user_accounts.default_app_id` and `apps_user_accounts.app_id` remain as tolerated cross-module FKs against mod-core's `apps` table — this module's manifest declares `after: [core]` (see `moduleforge.module.yaml`) so mod-core's migrations, including `apps`, run before this module's, satisfying those FKs at composition time. mod-users no longer retains any local `apps` table or query; it keeps only the `apps_user_accounts` membership join, whose handler resolves the `{uuid}` path param via mod-core's shared `entityResolver` service rather than a local lookup.

## API layer

The HTTP API is defined in `api/openapi.yaml`. Handlers live in `api/internal/handlers/`; business logic in `api/internal/service/`. All endpoints except `register`, `login`, and `anonymous` require an `Authorization: Bearer <jwt>` header.

API surface by tag group:

| Tag | Endpoints | Purpose |
|---|---|---|
| **Health** | `GET /healthz`, `GET /readyz` | Liveness and readiness probes |
| **Auth** | `/v1/auth/register`, `/v1/auth/login`, `/v1/auth/anonymous`, `/v1/auth/email-code`, `/v1/auth/password-reset`, `/v1/auth/providers`, `/v1/auth/oidc/{provider}/start`, `/v1/auth/oidc/{provider}/callback` | All authentication flows |
| **Self** | `GET /v1/self`, `PUT /v1/self` | Authenticated user reads and updates their own profile. `GET` is reachable to accounts with an unverified email (so the GUI can render the "verify your email" page); `PUT` requires a verified email |
| **Identities / Credentials** | `GET /v1/self/identities`, `POST /v1/self/identities/oidc/{provider}/start`, `DELETE /v1/self/identities/{identity_uuid}`, `POST`/`DELETE /v1/self/credential/password`, `POST /v1/self/credential/step-up`, `POST /v1/self/credential/step-up/verify` | List own identities (local password-credential status plus OIDC identities), link/unlink an OIDC identity, and set/remove a local password. `GET` (List) is reachable to accounts with an unverified email, matching the `/self` `GET` rationale; the six mutating endpoints additionally require a verified email. When `AUTH_REQUIRE_STEP_UP` (`cfg.Auth.RequireStepUpForCredentialChange`) is enabled, the credential-mutating endpoints (link start, unlink, set/remove password) additionally require a recent step-up challenge, presented via the `X-Step-Up-Token` header |
| **Users** | CRUD on `/v1/users`, `/v1/users/{uuid}`, grant/assume sub-routes | Admin user management |
| **Apps** | Membership only: `POST`/`GET /v1/apps/{uuid}/user-accounts` (assign/list members), `DELETE /v1/apps/{uuid}/user-accounts/{user_account_uuid}` (remove), `PUT .../roles` (update roles) | Admin app-membership management. App CRUD (`/v1/apps`, `/v1/apps/{uuid}`) is no longer served by this module — it is served by mod-core when composed into a host application |
| **Audit** | `/v1/audit`, `/v1/audit/{resource_type}/{resource_uuid}`, `/v1/users/{uuid}/audit` | Audit log access |

Routes are wired into the generated server via `moduleforge.module.yaml`'s `provides.routes` entries (consumed by the ModuleForge mfgen codegen). Per-route middleware differentiation is expressed by giving the affected route its own manifest entry with its own `middleware:` list — as seen in the account-routes entry, the two self-routes entries (`GET`/`PUT` split), and the two identities-routes entries (read/mutating split above) — rather than by any register-time conditional. The same manifest also wires internal service construction: `provides.services` entries build handler and cache instances rather than relying on app-level startup hooks — for example, the `stepUpConsumed` service's constructor (`auth.NewStepUpConsumedCache`) starts the background janitor that prunes expired step-up-token JTIs as a side effect of construction, so every composing host app gets the janitor running automatically without declaring its own `startupHooks:` entry.

## Authentication flow

Authentication is multi-channel: any given user account may have credentials from multiple sources, all resolving to the same identity.

**Anonymous.** `POST /v1/auth/anonymous` creates a `user_account` with a NULL email (no credentials required) and a corresponding `anon_tokens` row. The response includes a signed JWT (with `is_anonymous: true` claim) and a raw session token. The session token can be presented on a subsequent visit — keyed by `device_id` — to recover the same anonymous identity across sessions. When the user later provides an email address (via `PUT /v1/self`), the service upgrades the account: the `email` column is set, `is_anonymous` becomes false, and all `anon_tokens` rows for that account are deleted.

**Local (email + password).** `POST /v1/auth/register` creates a `user_account` + `auth_local` row. `POST /v1/auth/login` verifies the argon2id hash and returns a signed JWT.

**Email one-time code.** `POST /v1/auth/email-code` dispatches a short-lived code; `POST /v1/auth/email-code/verify` exchanges the code for a JWT. Used for passwordless login and email verification.

**OIDC (Google, Microsoft, Authelia, and others).** The OIDC flow is:
1. Client calls `GET /v1/auth/oidc/{provider}/start` → API returns an authorization URL.
2. User authenticates at the IdP. IdP redirects to `GET /v1/auth/oidc/{provider}/callback`.
3. API exchanges the code for an id_token, validates it with `go-oidc`, and extracts `sub` + `email`.
4. API looks up `auth_oidc_identities` by `(provider, sub)`.
   - Match found → issue JWT for the linked `user_account`.
   - No match, email matches an existing account → link the identity (merge); issue JWT.
   - No match, no email match → create new `user_account` + `auth_oidc_identities` row; issue JWT.

Provider configuration is loaded from environment variables (`AUTH_PROVIDER_<PROVIDER>_CLIENT_ID` / `_CLIENT_SECRET`) and can be overridden by rows in the `oidc_providers` table. In local dev, Authelia acts as the default IdP and is wired automatically by Docker Compose.

For OIDC configuration issues and troubleshooting, see [docs/oidc-troubleshooting.md](./oidc-troubleshooting.md).

## Multi-channel account model

`user_accounts` is the canonical identity record. `email` is nullable — a NULL value indicates an anonymous account. The derived boolean `is_anonymous` (email IS NULL) is exposed on Go service structs and in API responses. Anonymous accounts are a valid `user_account` subtype: they participate in authorization and auditing the same as named accounts, but they cannot use email-dependent auth flows.

Multiple authentication methods can be associated with the same named account:

- `auth_local` — one per account (email + password).
- `auth_oidc_identities` — one per provider per account (OIDC sub claim).
- `anon_tokens` — one or more per anonymous account (device continuity tokens, deleted on upgrade).

When a new OIDC login arrives for an email that matches an existing account, the OIDC identity is linked automatically (merged). This allows a user who originally registered with email/password to later log in via Google without creating a duplicate account, and vice versa.

## GUI component library

`gui/` is a React component library built with tsup and exported as `@moduleforge/users-gui`. It provides ready-made auth UI: `LoginForm` (email/password login, plus OIDC-provider sign-in buttons when the API reports configured providers) and `RegisterForm`, both self-contained components that call `useAuth()`/the API client directly, and five full-page compositions — `AuthPage` (composes both forms behind an in-page login/register mode toggle), `OidcCallbackPage` (handles the OIDC provider's redirect-back leg), `ForgotPasswordPage` (requests a password-reset link, then shows an in-place confirmation view), `ResetPasswordPage` (sets a new password given a reset token passed in as a required prop — it never reads the URL or a router itself), and `EmailCodePage` (passwordless sign-in via an emailed one-time code, toggling between an internal request/verify step) — which report outcomes via router-agnostic callback props rather than performing navigation themselves. `ForgotPasswordPage` and `ResetPasswordPage` call the API client directly (unlike `LoginForm`/`RegisterForm`, which call `useAuth()`'s `login()`/`register()`); `EmailCodePage` additionally calls `useAuth()` to establish the session once a code is verified, like `LoginForm`'s own use of `useAuth()`. Profile management is not yet part of this surface. Source layout: `src/components/` (UI components), `src/lib/` (API client, utilities). The `tsup` build emits JS and type declarations only — no CSS is bundled into `dist/`. The Tailwind v4 entry stylesheet lives at `.ladle/styles.css` and is used only by the Ladle component workbench (`make preview`); consumers generate their own CSS via Tailwind v4 `@source` scanning of the library's built `dist/` output, as `app-mfdemo` does.

The library depends on `@moduleforge/core-gui` as a peer. This dependency resolves through a bun workspace rather than a published registry: this repo's root `package.json` declares `"workspaces": ["gui", "../mod-core/gui"]`, so `bun install` at the repo root links the sibling repo's `mod-core/gui` in directly (requires `mod-core` checked out alongside this repo). `mod-core/gui` must be built once before `gui/`'s own build/typecheck resolves it. See [AGENTS.md](../AGENTS.md) for the setup procedure.

The `app-mfdemo` project (a separate Next.js app at the aggregate level — not part of this Bun workspace) wires together the running API and the GUI components to demonstrate a complete integration. It serves as the component showcase in lieu of a dedicated story tool.

## Local development stack

`make dev.start` brings up the full stack via Docker Compose (`deploy/local/docker-compose.yml`):

| Service | Port | Purpose |
|---|---|---|
| Postgres | 5432 | Primary datastore |
| Authelia | 9091 | Local OIDC identity provider |
| Mailpit | 1025/8025 | SMTP trap for email-code testing |
| API server | 8080 | Go HTTP API (built from source) |

`make dev.start` does not run a GUI container. For local component preview, run `make preview` (Ladle, port 61002); `app-mfdemo` (run separately, at the aggregate level) is the integration testbed against the running API.

Copy `.env.example` to `.env` and add `127.0.0.1 authelia` to `/etc/hosts` before first run. See `deploy/local/README.md` for the full first-time setup walkthrough.

## Build system

Make orchestrates the polyglot build. The root `Makefile` delegates to sub-project Makefiles via dot-namespaced targets:

```
make build          # build all sub-projects
make test           # run unit tests across all sub-projects
make lint           # lint all sub-projects
make dev.start      # start full local dev stack
make preflight      # verify tools and fix stale deps
make build.gui      # build gui/ only
```

JavaScript/TypeScript sub-projects use Bun. `gui/` is a member of the root Bun workspace (`bun install` at the repo root installs its deps). Go sub-projects (`model/`, `api/`) have their own `go.mod` files. The demo app (`app-mfdemo`) is a separate project at the aggregate level.

## Cross-cutting patterns

**sqlc** generates typed Go query functions from SQL. Queries live in `model/queries/`; the generated output is in `model/db/`. The workflow is: edit SQL → run `make generate` (or `sqlc generate`) → commit generated code. For the rationale behind sqlc and goose, see [mod-core: database considerations](./mf-standards/architecture/db-considerations.md).

A **bun workspace** resolves the `@moduleforge/core-gui` peer dependency: the root `package.json`'s `workspaces` array includes `../mod-core/gui` alongside this repo's own `gui` member, so `bun install` at the repo root links it in directly (no `yalc`, no registry lookup). This requires `mod-core` checked out as a sibling of this repo; see [AGENTS.md](../AGENTS.md) for the concrete steps.

## Further reading

- [docs/mod-users-spec.md](./mod-users-spec.md) — feature specification and key use cases
- [docs/oidc-troubleshooting.md](./oidc-troubleshooting.md) — OIDC configuration troubleshooting
- [docs/project-structure.md](./project-structure.md) — full directory layout
- [AGENTS.md](../AGENTS.md) — build, test, and dev environment commands
- [mod-core architecture](./mf-standards/architecture.md) — module system design, composition model, authorization design
