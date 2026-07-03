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

4. **Set up yalc for gui/ peer dependency** (required for `gui/` builds):

   `gui/` depends on `@moduleforge/core-gui` via a `file:.yalc/` link. The `.yalc/` directory is gitignored and must be populated manually in fresh checkouts or worktrees:
   ```sh
   # From the core-gui package directory (sibling repo):
   yalc publish
   # Then from mod-users root:
   cd gui && yalc add @moduleforge/core-gui && cd ..
   bun install
   ```
   If you don't need to work on `gui/`, skip this step — `make build.api` and `make test` work without it.

5. **Start the dev stack:**
   ```sh
   make dev.start
   ```
   This starts Postgres (5432), Authelia (9091), Mailpit (8025), and the API server (8080). For local component preview, use `make preview` (Ladle); `app-mfdemo` is the full integration testbed.

## Build commands

```sh
make build           # build all sub-projects (default target)
make build.model     # model/ only
make build.api       # api/ only
make build.gui       # gui/ only (requires yalc setup)
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

## Dev stack commands

```sh
make dev.start       # start full Docker Compose stack (Ctrl-C to stop)
make dev.restart     # restart the stack (useful after .env changes)
make dev.stop        # stop all containers
make dev.db-connect  # open a psql shell to the local Postgres instance
make clean           # remove build artifacts, DB data, and locally-built images
```

## Database migrations

Migrations are managed with goose in `model/migrations/`. They run automatically on API server start. To run or roll back manually:
```sh
cd model
goose -dir migrations postgres "$DB_URL" up
goose -dir migrations postgres "$DB_URL" down
```

## Code generation (sqlc)

`model/db/` is generated from SQL in `model/queries/`. After editing a query file:
```sh
cd model && sqlc generate
```
The generated files are committed to the repo. `make clean.build` removes `model/db/` — restore with `git checkout HEAD -- model/db/` if needed.

## Working in worktrees

This repo uses git worktrees for isolated plan branches. When working in a worktree:
- Run `bun install` at the worktree root (the lockfile is checked in but `node_modules` is gitignored).
- Copy `.yalc/` from the main checkout into the worktree before building `gui/`.
- Copy `.env` from the main checkout (it is gitignored).

## Key files and directories

| Path | Purpose |
|---|---|
| `Makefile` | Root orchestrator — all top-level build, test, and dev targets |
| `package.json` | Bun workspace root — declares `gui/` as a workspace member |
| `bun.lock` | Workspace lockfile (committed) |
| `.env.example` | Template for required environment variables |
| `api/openapi.yaml` | Authoritative REST API specification |
| `model/schema/` | Postgres schema definitions (source of truth for table structure) |
| `model/migrations/` | goose migration files (numbered, in order) |
| `model/queries/` | SQL queries consumed by sqlc |
| `model/db/` | sqlc-generated Go query code (committed; do not edit by hand) |
| `gui/src/components/` | React UI components |
| `gui/src/lib/` | API client and shared utilities |
| `deploy/local/` | Docker Compose files and Authelia config for local dev |
| `docs/` | Project documentation |
| `plan/` | Historical plan summaries and follow-up items |
| `.flow/` | Flow workflow tooling binding manifest |
| `.claude/` | Claude Code project config |

## Known issues and follow-up items

- **CI workflow still uses pnpm** (`next-steps id: r86L`): `.github/workflows/ci.yml` installs pnpm and runs `pnpm install --frozen-lockfile`. CI will fail until the workflow is updated to use bun. This is a **blocker** for CI-dependent merges.

## Conventions

- **Internal IDs are never exposed in HTTP responses** — always use the `uuid` field.
- **Handlers are thin** — parse input, call one service method, shape response. No business logic in handlers.
- **Authorization is checked first** in every service method, before any data access.
- **Generated code (`model/db/`) is committed** and should not be edited by hand. Re-run `sqlc generate` after any query change.
- **Cross-module schema deps** are resolved by the host application's migration composition step, not by importing mod-core schema directly.
