# AGENTS.md — mod-users

This file is the canonical reference for contributors and AI agents working on this codebase. It covers environment setup, build and test commands, project conventions, and known rough edges. Claude Code-specific guidance is in [`.claude/CLAUDE.md`](./.claude/CLAUDE.md), which references this file.

## Project overview

`@moduleforge/mod-users` is a ModuleForge monorepo module providing user identity, account management, and authentication. It ships three sub-projects: `model/` (Go/Postgres/sqlc), `api/` (Go HTTP), and `gui/` (TypeScript/React). A demo application (`app-mfdemo`) lives in a separate project at the aggregate level. See [docs/architecture.md](./docs/architecture.md) for the full system design and [docs/mod-users-spec.md](./docs/mod-users-spec.md) for the feature specification.

## Prerequisites

| Tool | Min version | Purpose |
|---|---|---|
| Go | 1.21+ | `model/` and `api/` sub-projects |
| Bun | 1.0+ | `gui/`, root workspace |
| Docker + Compose | any recent | Local dev stack (Postgres, Authelia, Mailpit) |
| GNU make | 3.81+ | Build orchestration (use `gmake` on macOS if needed) |
| sqlc | latest | Go query code generation (`model/`) |
| goose | latest | Database migrations (`model/`) |

> macOS ships BSD make. Install GNU make with `brew install make` and invoke as `gmake`, or ensure `/usr/local/bin` is before `/usr/bin` in `PATH`.

## First-time setup

1. **Clone and install JS dependencies:**
   ```sh
   git clone git@github.com:moduleforge/mod-users.git
   cd mod-users
   bun install          # installs root workspace deps (includes gui/)
   ```

2. **Copy the env file:**
   ```sh
   cp .env.example .env
   # Edit .env if needed — defaults work for local dev
   ```

3. **Add Authelia to /etc/hosts** (required for OIDC login in local dev):
   ```sh
   echo "127.0.0.1  authelia" | sudo tee -a /etc/hosts
   ```

4. **Build `mod-core/gui`, mod-users' bun-workspace sibling** (required for `gui/` builds):

   `gui/` declares `@moduleforge/core-gui` as an optional peer dependency, resolved via a bun workspace rather than a registry package: this repo's root `package.json` declares `"workspaces": ["gui", "../mod-core/gui"]`, so `bun install` at the repo root links `mod-core/gui` in directly (requires the sibling repo `mod-core` checked out alongside `mod-users`, e.g. `../mod-core` relative to this repo's root). `mod-core/gui` must be built once before `gui/`'s own build/typecheck can resolve `@moduleforge/core-gui`:
   ```sh
   # From the mod-core repo (sibling of this repo):
   cd ../mod-core/gui && bun run build   # not bare `tsup` -- dist/index.css needs the build:css step
   # Then from mod-users root:
   bun install --frozen-lockfile
   ```
   If you don't need to work on `gui/`, skip this step — `make build.api` and `make test` work without it.

5. **Start the dev stack:**
   ```sh
   make dev.start
   ```
   This starts Postgres (5432), Authelia (9091), Mailpit (8025), and the API server (8080). For local component preview, use `make preview` (Ladle); `app-mfdemo` is the integration testbed.

## Build commands

```sh
make build           # build all sub-projects (default target)
make build.model     # model/ only
make build.api       # api/ only
make build.gui       # gui/ only (requires mod-core/gui built first — see First-time setup)
make preflight       # verify tool versions and fix stale deps across all sub-projects
```

## Test commands

```sh
make test            # unit tests across all sub-projects (alias: make test.unit)
make test.unit       # same as above
make test.integration # integration tests (requires running Postgres)
make test.all        # unit + integration
make lint            # lint all sub-projects (read-only)
make lint-fix        # apply lint fixes
```

Sub-project tests can be run directly:
```sh
cd model && go test ./...
cd api   && go test ./...
cd gui   && bun test
```

### Integration tests against a throwaway Postgres

On a shared Docker host, run the integration suites (`api/internal/authz`, `api/internal/config`, `api/internal/integration`) against a throwaway container rather than `users-module-postgres`:

```sh
name="mod-users-integ-$(openssl rand -hex 4)"
docker run -d --rm --name "$name" -e POSTGRES_USER=users -e POSTGRES_PASSWORD=users \
  -e POSTGRES_DB=postgres -p 127.0.0.1::5432 postgres:16
port="$(docker port "$name" 5432/tcp | head -1 | sed 's/.*://')"
until docker exec "$name" pg_isready -U users; do sleep 1; done
make -C model compose
(cd api && AUTHZ_DEV_PG_HOST=127.0.0.1 AUTHZ_DEV_PG_PORT="$port" \
   go test -tags=integration -p 1 -count=1 ./internal/authz/... ./internal/config/... ./internal/integration/...)
docker rm -f "$name"
```

`AUTHZ_DEV_PG_HOST` (explicit host, skips the `users-module-postgres` check) and `AUTHZ_DEV_PG_PORT` (default 5432) apply to all three suites. Each drops and recreates its own database (`authz_integ_users`, `signing_key_integ_users`, `users_integ_http`), so an explicit host with the default port 5432 is refused unless `AUTHZ_DEV_PG_CONFIRM_SHARED=1` is set to confirm use of a shared Postgres.

`api/internal/integration` composes the real services, handlers, authentication middleware and token carrier over that database and drives them through `httptest` (sessions, refresh and fork, step-up, credentials, OIDC with the identity provider replaced by `OAuth.ExchangeFn`, client credentials, the audit vocabulary and the route error order). When the mod-audit sibling is linked (`MODULEFORGE_SIBLINGS_DIR/mod-audit`, or `../mod-audit`), its `audit_log` migration is applied and its real observer runs in the observer group, so an op or principal outside mod-audit's contract fails the operation. The suite compares the database clock with the machine clock and refuses to run when they differ by more than ten seconds (a Docker VM whose clock drifted after the host slept); restart or resync the container then.

See the header of `api/internal/authz/authz_integration_test.go` and of `api/internal/integration/harness_integration_test.go` for detail.

### First run of a fresh database

The server prints a setup token at boot while the deployment has no operator party or OIDC is unconfirmed. Use it for `POST /v1/bootstrap` (header `X-Setup-Token`; creates the first user, the operator party and its administrator, see README "First run") and for the OIDC configuration at `/oidc-config`; it is cleared once both are done. The OIDC configuration routes accept it only while OIDC is unconfirmed: once OIDC is confirmed they need an operator administrator session even while the bootstrap is pending, and `POST /v1/bootstrap` alone keeps the token live then. There is no first-user hook: `core_claim_operator_party` decides the single bootstrap winner. Integration tests of the bootstrap (`api/internal/integration/bootstrap_integration_test.go`) run each case on a fresh copy of the migrated schema (`CREATE DATABASE ... TEMPLATE users_integ_boot_tpl`), because the operator party is set once per database.

## Dev stack commands

```sh
make dev.start       # start full Docker Compose stack (Ctrl-C to stop)
make dev.restart     # restart the stack (useful after .env changes)
make dev.stop        # stop all containers
make dev.db-connect  # open a psql shell to the local Postgres instance
make preview         # run the Ladle component workbench for gui/ (http://localhost:61002)
make clean           # remove build artifacts, DB data, and locally-built images
```

## Database migrations

Migrations are managed with goose. The `.sql` migration files live in `model/migrations/sql/`;
`model/migrations/migrate.go` (one level up) embeds them and exposes the `Migrate` function the
host app's generated `main.go` invokes at startup against this module's own dedicated
`goose_db_version_users` tracking table. The two are split into separate directories because
`goose validate`'s CLI globs every `*.go`/`*.sql` file in the directory it is pointed at and
hard-fails on any non-migration `.go` helper file it finds there — `sql/` keeps that directory
migration-only. To run or roll back manually:
```sh
cd model
goose -dir migrations/sql postgres "$DB_URL" up
goose -dir migrations/sql postgres "$DB_URL" down
```

Each module owns a fixed migration number range declared in its manifest (`migrations.range`; mod-users uses 100–199), isolated in its own `goose_db_version_<module>` table (`goose_db_version_users` here). mod-users has a single migration, `0100_baseline.sql`, rewritten in place (pre-release) for the identity model, with the former `0101`–`0104` email-code migrations folded in; any existing dev database created from an earlier baseline must be dropped and recreated (`make clean.data`).

Every mod-users table, function and trigger function lives in the `mod_users` schema and is referenced schema-qualified; objects owned by mod-core and mod-authz are referenced `public.`-qualified. Every function is `SECURITY INVOKER` and pinned to `SET search_path = pg_catalog, pg_temp`, so neither a caller's `search_path` (including mod-core's `SECURITY DEFINER` functions that write `public.profiles`) nor a session-temporary relation can redirect a name. The one exception is the `public.accessible_user_ids_for_actor` stub, which keeps the shape of mod-core's access-function stubs because `setup.ApplyFuncs` replaces it at startup. The baseline also owns two triggers on mod-core's `public.profiles` (`profiles_users_holder_rules`, `profiles_users_personal_profile`) that enforce the user-holder rules. Database invariants raise mod-users' own SQLSTATE codes: `MF101` (session), `MF102` (credential), `MF103` (user); the header comment of `0100_baseline.sql` lists them with the named unique indexes the service layer maps to `409`.

Cross-module ordering (when one module's migrations depend on another's schema) is carried by the manifest's `migrations.after` field, not by range allocation. `mod-users` declares `after: [core]` because its migrations reference mod-core's `principals`, `profiles`, `legal_entities`, `natural_persons`, `apps` and `profile_policy_confirmations` tables and its `type_is_or_descends_from` and `set_updated_at` functions. See `docs-mf-standards/manifest-spec.md` §5 for the full convention.

## Code generation (sqlc)

`model/db/` is generated from SQL in `model/queries/`. After editing a query file:
```sh
cd model && sqlc generate
```
The generated files are committed to the repo. `make clean.build` removes `model/db/` — restore with `git checkout HEAD -- model/db/` if needed.

sqlc prefixes the generated Go struct name of a table in a non-`public` schema with the schema (`mod_users.users` → `ModUsersUser`). Every mod-users table is in `mod_users`, so `model/sqlc.yaml` carries a `rename` override per table that keeps the Go names unprefixed (`User`, `Credential`, `Session`, ...). The override key is the schema-prefixed, singularized table name sqlc derives (`mod_users_user`); add one for every new table.

## Working in worktrees

This repo uses git worktrees for isolated plan branches. When working in a worktree:
- Run `bun install` at the worktree root (the lockfile is checked in but `node_modules` is gitignored).
- Copy `.env` from the main checkout (it is gitignored).
- `api/go.mod`'s `replace` directives (`../../mod-core/{api,model}`, `../../mod-audit/*`,
  `../../mod-authz/*`) resolve fine from the main checkout but break for any worktree nested deeper
  under `mod-users/worktrees/`, since the extra path depth throws off the relative paths. `make
  preflight` (a prerequisite of `build`/`test`) runs `scripts/link-siblings.sh`, which resolves this
  automatically at any worktree nesting depth by planting compatibility symlinks in the worktree's
  parent directory — no manual `go work` step needed. See
  [Building inside a task worktree](./docs/mf-standards/building-common.md#building-inside-a-task-worktree)
  for the mechanism.
- `gui/`'s `@moduleforge/core-gui` peer resolves through the root `package.json`'s bun workspace
  (`"workspaces": ["gui", "../mod-core/gui"]`), which composes with `link-siblings.sh`'s
  compatibility symlinks the same way the Go `replace` paths do — verified for the workspace
  member itself. **Known gap:** building `mod-core/gui` *through* the worktree's `../mod-core`
  compatibility symlink can leave `mod-core/gui`'s own devDependencies (e.g. `style-dictionary`,
  needed only for its `build:tokens` step) unresolvable — bun computes their hoisted-store
  symlinks relative to the symlink's apparent nesting depth under `mod-users/worktrees/`, which
  does not match the real, shallower depth of `mod-core`'s actual checkout the symlink points at.
  Workaround: build `mod-core/gui` once from `mod-core`'s own real checkout (`cd
  <aggregate-root>/mod-core/gui && bun install && bun run build`, not through the worktree's
  `../mod-core` path) before building/linting `gui/` from inside a task worktree. CI is
  unaffected — `checkout-deps.sh` materializes `mod-core` as a real, flatly-nested sibling
  directory there, never through this compatibility-symlink layer.

## Key files and directories

| Path | Purpose |
|---|---|
| `Makefile` | Root orchestrator — all top-level build, test, and dev targets |
| `package.json` | Bun workspace root — declares `gui/` as a workspace member |
| `bun.lock` | Workspace lockfile (committed) |
| `.env.example` | Template for required environment variables |
| `api/openapi.yaml` | Authoritative REST API specification |
| `api/internal/service/profile_service.go`, `profile_admit.go`, `profile_admin.go`, `profile_holder.go`, `profile_last_admin.go` | `ProfileService`: the party profile lifecycle (`InviteInTx`, `AdmitInTx` (trusted admission for mod-billing: no grant check; reuses an active profile, activates an invited one, refuses a suspended one with `ErrProfileSuspended`, else creates an active one), `EndInTx` with departure cleanup, `AddLifecycleObserver`, managed-user provisioning) and the HTTP-facing methods behind the party and `/v1/me/profiles/{uuid}/...` routes. `checkInvitePolicyMismatch` refuses an invitation whose holder cannot meet the party policy (409 `users.policy_mismatch_confirmation_required`) unless `ConfirmPolicyMismatch` is set (which needs `update` on the party, else 403), then `InviteInTx` records the `profile_policy_confirmations` row after creating the profile. `requireNotLastOperatorAdmin` (`profile_last_admin.go`, called from `transitionLocked` so `EndInTx`, the admin status update and the holder leave all inherit it) refuses to end or suspend the last active operator administrator (409 `users.last_operator_admin`), after taking the `platform_settings` row lock so concurrent transitions serialize (the full lock order is in [Lock order](docs/architecture.md#lock-order)). It reads the mod-authz-owned `public.grants` and `public.authz_operations` tables and `platform_settings` with raw SQL and hardcodes the Bootstrap grant shape (direct entity-scope wildcard `manage`, target NULL); it deliberately ignores a `manage` grant inherited through an actor group. That approximation is an explicit, recorded decision. |
| `api/internal/service/party_policy.go` | `PartyPolicyService`: the party authn policy (`party_authn_policies`), credential authorizations (`credential_party_authorizations`) and confinements (`credentials.confined_to_party_id`) behind `/v1/parties/{party_uuid}/authn-policy`, `/credential-authorizations` and `/confinements`. Writers: a party administrator (`update` on the party entity) or the credential holder through a session that satisfies the party gate through another factor (`holderSatisfiesGate`, which calls `mod_users.profile_availability_excluding` so a credential cannot authorize itself); every write needs a fresh authentication, and the credential's holder must hold an active profile in the party. The administrator door of confine reaches only party-controlled credentials (`partyControlled`: managed-user credentials and OIDC credentials of a party-bound provider); the holder door of unconfine is refused for those. Audit: `party_authn_policy` `create`/`update`, `credential_party_authorization` `create`/`delete`, confinement as `update` `credential`. |
| `api/internal/service/collaborator_gate.go`, `party_policy_lock.go`, `policy_mismatch.go` | `CollaboratorGate` (`usersservice.CollaboratorGate`, `CheckOutsideCollaborator(ctx, tx, owningPartyID, memberProfileID, confirmed)`): the hook mod-authz consults inside its write transaction for a profile outside the owning party (personal profiles count as outside) added to a party-owned actor group or named as a grant actor. It runs the party-policy mismatch check (`mod_users.holder_policy_mismatch`, shared with the invitation check, next to `profile_availability_for`), refuses an unconfirmed mismatch with 409 `users.policy_mismatch_confirmation_required`, and records `profile_policy_confirmations` idempotently when confirmed; a confirmation exempts the party's `credential_rule` only, never `min_acr`. At invite time the `authorized` rule counts as met by any usable interactive credential (no credential can be authorized before the profile exists), so the member stays locked (`credential_not_authorized`) until a credential is authorized after acceptance; `PutAuthnPolicy` deletes the party's confirmations when `credential_rule` or the required OIDC provider changes. The gate takes the shared per-party policy lock (`lockPartyPolicyShared`, in `party_policy_lock.go`) before reading the policy; `PutAuthnPolicy` takes the exclusive one (`lockPartyPolicyExclusive`) before its read and its confirmation purge, so the two serialize. Wired as the trailing argument of `authzservice.NewFromArgs` in `api/cmd/server/main.go` and provided as `collaboratorGate` in `moduleforge.module.yaml`; `api/usersservice/collaborator_gate_test.go` asserts it satisfies `authzservice.CollaboratorGate` (mod-authz must not import mod-users). |
| `api/internal/service/bootstrap_service.go`, `api/internal/handlers/bootstrap.go`, `operator_admin.go` | `BootstrapService` (`Bootstrap`: the setup-token first-run bootstrap in one transaction, serialized on the `platform_settings` row with `core_claim_operator_party` as the arbiter; `IsOperatorAdmin`: an operator profile session holding wildcard `manage`), `BootstrapHandler` (`RegisterBootstrapRoutes`: public `GET /v1/bootstrap/status`, `POST /v1/bootstrap`) and `NewOperatorAdminChecker`, the OIDC config `AdminChecker`. The setup-token lifecycle (active while the bootstrap or the OIDC confirmation is pending) lives in `OIDCConfigHandler` (`api/internal/handlers/oidc_config.go`, `Platform` dependency). |
| `api/internal/handlers/parties/` | `PartiesHandler`: `RegisterPartyRoutes` mounts `/v1/parties/{party_uuid}/profiles` (list, invite) and `/managed-users`, and also mounts the party authentication-policy, credential-authorization and confinement routes (served by `PartyPolicyService`); `RegisterProfileRoutes` mounts `PATCH /v1/profiles/{uuid}`. The holder's `accept`, `decline` and `leave` are on `MeHandler`. |
| `api/handlers/handlers.go` | Public facade re-exporting handler types/constructors/route-registration functions from `api/internal/handlers/` — the only handlers package external modules (host apps) can import. See Conventions below. |
| `model/schema/` | Postgres schema definitions (source of truth for table structure) |
| `model/migrations/sql/` | goose migration files (numbered, in order) |
| `model/migrations/migrate.go` | Embeds `model/migrations/sql/*.sql` and exposes `Migrate` |
| `model/queries/` | SQL queries consumed by sqlc |
| `model/db/` | sqlc-generated Go query code (committed; do not edit by hand) |
| `gui/README.md` | Consumer integration guide for `@moduleforge/users-gui` (runtime configuration, 401 handling, routes, standard pages, examples) — update it with any change to the exported GUI surface |
| `gui/src/components/` | React UI components, including the standard screens (`VerifyEmailPage`, `OidcConfigPage`, `OidcSetupGate`) |
| `gui/src/lib/` | API client, runtime configuration (`config.ts`: `configureUsersApi`, `isSafeReturnPath`), `return-path.ts`, `routes.ts` (`USERS_GUI_ROUTES`), and shared utilities |
| `deploy/local/` | Docker Compose files and Authelia config for local dev |
| `docs/` | Project documentation |
| `plan/` | Historical plan summaries and follow-up items |
| `.flow/` | Flow workflow tooling binding manifest |
| `.claude/` | Claude Code project config |

## Conventions

- **Internal IDs are never exposed in HTTP responses** — always use the `uuid` field.
- **Handlers are thin** — parse input, call one service method, shape response. No business logic in handlers.
- **Error precedence follows [http-error-precedence](./docs/mf-standards/architecture/http-error-precedence.md):** 401, 400, 403, 404, then 409/422. Stateless request-shape checks (`server.Decode`/`Validator`, `apiresp.ParseUUIDParam`) run in the handler before authorization. User-account lookups are masked: a missing account is 403 for every caller. Registrars of authenticated routes mount `auth.NewRequireAuth` (bearer access token, per-request session or credential liveness; it installs `auth.Identity` and `opctx.AuthContext`); public and credential routes (`/v1/auth/*`, OIDC config credentials) never do. No route reads an authentication cookie. Service methods still authorize before any data access that depends on state.
- **SSH profile derivation for mod-repos:** `usersservice.SSHKeyResolver.AuthContextForOwner(ctx, cred, ownerID)` derives the acting profile from the repository owner: a party the key's principal holds an active profile in gives that party profile when `mod_users.credential_profile_availability` is `available`; otherwise (no owner, a non-party owner, no active profile in the party, or the principal's own user or person) it gives the personal profile, unless the key is confined to a party. It is read-only, uncached and logs no key material. A refusal satisfies `errors.Is(err, usersservice.ErrSSHKeyNotAuthorized)` ("key not authorized for X"); `usersservice.SSHKeyNotAuthorizedParty(err)` returns the party uuid X. An SSH key is never an OIDC factor, so a party whose rule is `provider` always refuses it, as does a party `min_acr` above the key kind's `base_acr`.
- **Generated code (`model/db/`) is committed** and should not be edited by hand. Re-run `sqlc generate` after any query change.
- **Cross-module schema deps** are resolved by the host application's migration composition step, not by importing mod-core schema directly.
- **Any handler/constructor/route-registration function referenced by `moduleforge.module.yaml`'s `constructor:` or `register:` keys must be re-exported through the public facade `api/handlers/handlers.go`, not left only in `api/internal/handlers/`.** External modules — including every `mfgen`-generated host app's composition root (e.g. `app-mftodo/cmd/server/main.go`) — import `github.com/moduleforge/mod-users/api/handlers`, which cannot see unexported-package (`internal/`) symbols. `handlers.go` re-exports each handler type as a type alias (`type X = inner.X`) plus a thin wrapper function per constructor/register call. When adding a new manifest entry, add the matching alias/wrapper to `handlers.go` in the same change — otherwise the manifest addition compiles fine in this repo (tests here only exercise `internal/`) but breaks `mfgen generate` output in every consuming app, and the failure surfaces there, not here. This exact gap shipped in the `self-route-manifest` plan (2026-07-15): `RegisterSelfGetRoute`/`RegisterSelfPutRoute` were added to `internal/handlers` and to the module manifest, but not re-exported, so `app-mftodo`'s generated `main.go` referenced undefined symbols until a follow-up fix added the facade wrappers.
