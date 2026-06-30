# model

Postgres 16 schema, goose versioned migrations, and sqlc-generated Go queries
for mod-users.

See [../../mod-core/docs/architecture/db-considerations.md](../../mod-core/docs/architecture/db-considerations.md)
for the rationale behind the Postgres + goose choices.

## Layout

- `migrations/` — goose versioned migration files (`.sql`)
- `queries/` — sqlc query files (`.sql`), one per concept
- `internal/db/` — sqlc-generated Go code (do not edit)
- `scripts/shadow-db-lint.sh` — ephemeral-Postgres lint runner
- `scripts/` — operational SQL scripts (e.g., `relink_auth.sql`)
- `sqlc.yaml` — sqlc v2 configuration

## Prerequisites

- [goose](https://github.com/pressly/goose) — `go install github.com/pressly/goose/v3/cmd/goose@latest`
- [sqlc](https://docs.sqlc.dev) — `go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.28.0`
- Docker (for `make lint`'s ephemeral shadow Postgres)
- Running Postgres instance (local: `docker compose up -d` from `deploy/local/`)

## Postgres dependency

The schema requires the `pgcrypto` extension for `gen_random_uuid()` and uses
several Postgres features (partial indexes, `JSONB`, `TIMESTAMPTZ`, native
`UUID` type) that are deliberately load-bearing. See the architecture note
linked above for the rationale.

## Make targets

```
make build            # alias for gen
make gen              # generate Go from sqlc queries
make verify           # goose validate + sqlc compile
make migrate.new NAME=foo  # create a new migration file
make migrate.up       # apply pending migrations
make migrate.status   # show migration status
make test.integration # apply migrations against DATABASE_URL
make lint             # apply all migrations to an ephemeral Postgres container
make clean            # remove generated Go code
```

All targets default `DATABASE_URL` to `postgresql://users:users@localhost:5432/users?sslmode=disable`.
From the ai-sandbox environment, use `host.docker.internal` instead of `localhost`.
