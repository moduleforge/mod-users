# mod-users architecture

## Overview

`mod-users` is a ModuleForge module that provides user identity, account management, and authentication as a composable unit. A host application integrates it by mounting the Go model migrations, wiring the Go API services, and importing the React component library. The module ships three sub-projects — `model`, `api`, and `gui` — each independently consumable and built to the ModuleForge module contract described in [mod-core architecture](https://github.com/moduleforge/mod-core/blob/main/docs/architecture.md). A demo application (`app-mfdemo`) that wires the module end-to-end lives in a separate project at the aggregate level.

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
| `apps` | Application tenants — a host app may register one or more named apps |
| `user_accounts` | Core user entity; promoted to `Entity` status for authorization (see [entity-typing](https://github.com/moduleforge/mod-core/blob/main/docs/architecture/entity-typing.md)). `email` is nullable — NULL indicates an anonymous account |
| `anon_tokens` | Maps `device_id` + `session_token` (SHA-256 hashed) to a `user_account`; enables cross-session identity continuity for anonymous users. Rows are deleted when the account is upgraded to a named account |
| `apps_user_accounts` | Many-to-many join between apps and user accounts |
| `auth_local` | Email + argon2id password credentials for a user account |
| `email_codes` | Short-lived one-time codes used for email verification and passwordless login |
| `password_resets` | Pending password-reset tokens |
| `oidc_config` | Per-deployment OIDC configuration (enabled providers, default settings) |
| `oidc_providers` | Per-provider OIDC overrides (issuer URL, client ID/secret, stored in DB or env) |
| `auth_oidc_identities` | OIDC identity records linking an external subject (`sub`) to a `user_account` |

Internal IDs are integers (joins only, never sent in responses). External IDs are UUIDs. Cross-module schema dependencies (e.g., the `legal_entities` table from mod-core) are resolved by the host application's migration composition step, not by tight coupling.

## API layer

The HTTP API is defined in `api/openapi.yaml`. Handlers live in `api/internal/handlers/`; business logic in `api/internal/service/`. All endpoints except `register`, `login`, and `anonymous` require an `Authorization: Bearer <jwt>` header.

API surface by tag group:

| Tag | Endpoints | Purpose |
|---|---|---|
| **Health** | `GET /healthz`, `GET /readyz` | Liveness and readiness probes |
| **Auth** | `/v1/auth/register`, `/v1/auth/login`, `/v1/auth/anonymous`, `/v1/auth/email-code`, `/v1/auth/password-reset`, `/v1/auth/providers`, `/v1/auth/oidc/{provider}/start`, `/v1/auth/oidc/{provider}/callback` | All authentication flows |
| **Self** | `GET /v1/self`, `PUT /v1/self` | Authenticated user reads and updates their own profile |
| **Users** | CRUD on `/v1/users`, `/v1/users/{uuid}`, grant/assume sub-routes | Admin user management |
| **Apps** | CRUD on `/v1/apps`, member management | Admin application and tenancy management |
| **Audit** | `/v1/audit`, `/v1/audit/{resource_type}/{resource_uuid}`, `/v1/users/{uuid}/audit` | Audit log access |

## Authentication flow

Authentication is multi-channel: any given user account may have credentials from multiple sources, all resolving to the same identity.

**Anonymous.** `POST /v1/auth/anonymous` creates a `user_account` with a NULL email (no credentials required) and a corresponding `anon_tokens` row. The response includes a signed JWT (with `is_anonymous: true` claim) and a raw session token. The session token can be presented on a subsequent visit — keyed by `device_id` — to recover the same anonymous identity across sessions. When the user later provides an email address (via `PUT /v1/self`), the service upgrades the account: the `email` column is set, `is_anonymous` becomes false, and all `anon_tokens` rows for that account are deleted. The `login`, `email-code`, and `password-reset` handlers guard against anonymous accounts and return `400 anonymous_account` if called with an anonymous JWT.

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

`gui/` is a React component library built with tsup and exported as `@moduleforge/users-gui`. It provides ready-made components for registration, login (all channels), password reset, and profile management. Source layout: `src/components/` (UI components), `src/lib/` (API client, utilities). The `tsup` build emits JS and type declarations only — no CSS is bundled into `dist/`. The Tailwind v4 entry stylesheet lives at `gui/.ladle/styles.css` and is used only by the Ladle component workbench (`make preview`); consumers generate their own CSS via Tailwind v4 `@source` scanning of the library's built `dist/` output, as `app-mfdemo` does.

The library depends on `@moduleforge/core-gui` as a peer. For local development, this dependency is linked via yalc rather than a published registry. See [AGENTS.md](../AGENTS.md) for the yalc setup procedure.

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

**sqlc** generates typed Go query functions from SQL. Queries live in `model/queries/`; the generated output is in `model/db/`. The workflow is: edit SQL → run `make generate` (or `sqlc generate`) → commit generated code. For the rationale behind sqlc and goose, see [mod-core: database considerations](https://github.com/moduleforge/mod-core/blob/main/docs/architecture/db-considerations.md).

**yalc** is used to link the `@moduleforge/core-gui` peer dependency during local development. The `.yalc/` directory is gitignored and must be set up manually in fresh worktrees. This pattern is common across ModuleForge GUI modules; see [AGENTS.md](../AGENTS.md) for the concrete steps.

## Further reading

- [docs/mod-users-spec.md](./mod-users-spec.md) — feature specification and key use cases
- [docs/oidc-troubleshooting.md](./oidc-troubleshooting.md) — OIDC configuration troubleshooting
- [docs/project-structure.md](./project-structure.md) — full directory layout
- [AGENTS.md](../AGENTS.md) — build, test, and dev environment commands
- [mod-core architecture](https://github.com/moduleforge/mod-core/blob/main/docs/architecture.md) — module system design, composition model, authorization design
