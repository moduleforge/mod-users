# Project structure — mod-users

This document maps every significant directory and key root-level file. After reading it, a contributor or agent should be able to locate any part of the codebase without exploring the tree.

## Root-level files

| File | Purpose |
|---|---|
| `Makefile` | Root build orchestrator. Delegates to sub-project Makefiles via dot-namespaced targets (`build.api`, `test.gui`, etc.). Run `make help` or scan the file for all targets. |
| `package.json` | Bun workspace root. Declares `gui/` as a workspace member and pins `"engines": { "bun": ">=1.0" }`. |
| `bun.lock` | Workspace lockfile for root + `gui/`. Committed; do not delete. |
| `.env.example` | Template for all environment variables consumed by Docker Compose, the API server, and the GUI. Copy to `.env` before running the dev stack. |
| `.gitignore` | Repo-wide ignore rules. Notable entries: `node_modules/`, `dist/`, `worktree/`, `.env`, `CLAUDE.md` (root-level, not `.claude/CLAUDE.md`). |
| `.ko.yaml` | Ko (Go container image builder) configuration for the API server. |
| `.mcp.json` | MCP server configuration for Claude Code (gitignored; generated per session). |

## Sub-project directories

### `model/` — Go data model

Owns the Postgres schema, migrations, and generated query code.

```
model/
  go.mod / go.sum       # independent Go module
  Makefile              # model-specific build targets
  sqlc.yaml             # sqlc configuration
  schema/               # Postgres table definitions (source of truth)
  migrations/           # goose-managed migration files (numbered, in order)
  queries/              # SQL queries that sqlc compiles to Go
  db/                   # sqlc-generated Go code (committed; do not edit)
  internal/             # shared model utilities
  scripts/              # helper scripts (migration tooling, etc.)
```

### `api/` — Go HTTP API

Owns the HTTP handlers, business-logic services, and authentication middleware.

```
api/
  go.mod / go.sum       # independent Go module
  Makefile
  openapi.yaml          # authoritative REST API specification
  Dockerfile            # production container image
  entrypoint.sh         # container entrypoint
  cmd/server/           # main package — wires deps and starts the HTTP server
  internal/
    auth/               # authentication middleware and OIDC integration
    authz/              # authorization helpers
    config/             # env-based configuration loading
    db/                 # database connection and transaction helpers
    email/              # email dispatch (SMTP / Mailpit)
    handlers/           # HTTP handlers (thin — one service call each)
    observability/      # logging, metrics, tracing setup
    service/            # business-logic services
    server/             # HTTP server setup and route registration
```

### `gui/` — TypeScript/React component library

Exports `@moduleforge/users-gui`: React components, an API client, and base styles.

```
gui/
  package.json          # declares the npm package name and build scripts
  Makefile
  tsconfig.json
  tsup.config.ts        # library bundler config (outputs CJS + ESM + .d.ts)
  Dockerfile.dev        # dev container for the GUI server
  dist/                 # build output (gitignored; `make build.gui` populates it)
  node_modules/         # managed by bun workspace (gitignored)
  src/
    index.ts            # package entry point — re-exports all public components
    components/         # React UI components (auth flows, profile, admin views)
    lib/                # API client, hooks, shared utilities
    stories/            # Storybook story files (exploratory; not the primary showcase)
    styles.css          # base component styles
```

> `gui/` depends on `@moduleforge/core-gui` via a `file:.yalc/` link. The `.yalc/` directory is gitignored and must be set up manually in fresh checkouts. See [AGENTS.md](../AGENTS.md#first-time-setup).

### `deploy/` — deployment configuration

```
deploy/
  local/                # Docker Compose stack for local development
    docker-compose.yml  # brings up Postgres, Authelia, Mailpit, API, GUI dev server
    authelia/           # Authelia config and certs for local OIDC
```

## Documentation directories

```
docs/
  mod-users-spec.md       # feature specification and key use cases
  architecture.md         # system design, sub-project relationships, auth flow
  project-structure.md    # this file
  oidc-troubleshooting.md # OIDC configuration troubleshooting checklist
```

## Workflow and tooling directories

```
plan/
  next-steps.yaml         # tracked follow-up items (open issues, blockers)
  summary-bun-migration.md # completed bun migration session summary

.flow/
  binding.md              # Flow workflow skill-binding manifest (auto-generated)

.claude/
  CLAUDE.md               # Claude Code project configuration (committed)
  settings.json           # Claude Code permissions and hooks

worktree/                 # git worktree roots for plan branches (gitignored)
```
