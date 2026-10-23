# model

Postgres 16 schema, goose versioned migrations, and sqlc-generated Go queries
for mod-users.

See [../docs/mf-standards/architecture/db-considerations.md](../docs/mf-standards/architecture/db-considerations.md)
for the rationale behind the Postgres + goose choices.

## Layout

- `migrations/sql/` — goose versioned migration files (`.sql`); currently the single
  `0100_baseline.sql`, which creates every mod-users table, trigger and function in the
  `mod_users` schema (see the file's header comment for the schema rule and the `MF101`–`MF103`
  SQLSTATE contract)
- `migrations/migrate.go` — embeds `migrations/sql/*.sql` and exposes `Migrate`
- `queries/` — sqlc query files (`.sql`), one per concept; they reference `mod_users` relations only
  (sqlc compiles against `migrations/sql/` alone), and call `mod_users` functions for anything
  that needs mod-core tables:
  - `users.sql` — `mod_users.users` (create, `GetUserByEntityID`, defaults, guest upgrade, managed-user lists)
  - `identifiers.sql` — `user_identifiers` (create, lookup by kind/value, primary switch, verify, delete)
  - `credentials.sql` — `credentials` and the `cred_password`/`cred_email_otp`/`cred_oidc`/`cred_ssh_key`/`cred_api_key`/`cred_anon`
    subtables (active lookups by identifier, issuer/subject, fingerprint, client id, token hash; revoke;
    revoke sessions holding a credential)
  - `challenges.sql` — `auth_challenges` (`create_challenge_with_cooldown`, consume, attempt counter)
  - `sessions.sql` — sessions, factors, refresh lineages (rotation, reuse lookup) and the
    `session_liveness`, `credential_liveness`, `profile_availability`,
    `credential_profile_availability` and `session_acr` wrappers
  - `party_policies.sql` — `party_authn_policies` and `credential_party_authorizations`
  - `oidc_config.sql`, `oidc_providers.sql` — the OIDC onboarding singleton and provider overrides
    (providers carry `party_id`, `jit_mode`, `trust_acr`)
  - `signing_keys.sql` — the persisted Ed25519 signing key
- `db/` — sqlc-generated Go code (package `usersdb`; do not edit — run `make gen`, and delete the
  `*.sql.go` of a removed query file, since sqlc does not)
- `scripts/shadow-db-lint.sh` — ephemeral-Postgres lint runner
- `sqlc.yaml` — sqlc v2 configuration; its `rename` overrides keep the Go names of the
  `mod_users` tables unprefixed (`User`, not `ModUsersUser`)

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
