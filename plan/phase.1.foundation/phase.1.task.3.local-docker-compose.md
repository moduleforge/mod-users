# Phase 1, Task 3 — Local docker-compose

## Context
Local dev runs against real Postgres and a real OIDC provider (Authelia) so dev-prod parity holds. Mock OIDC is forbidden — the ClaimMapper layer must be exercised end-to-end locally.

## Acceptance
`users-module/deploy/local/docker-compose.yml` defines:
- `postgres`: image `postgres:16-alpine`, persistent volume, healthcheck, exposes 5432, env from `.env` (POSTGRES_USER, POSTGRES_PASSWORD, POSTGRES_DB=users).
- `authelia`: latest stable image, configured with two seeded users (`admin@example.test` with admin role; `user@example.test`), OIDC client registered for `users-api` (client_id, client_secret, redirect URIs covering local API + GUI), JWT signing key, exposes 9091.
- `mailhog` (or `mailpit`): SMTP catcher for email-code flow testing, exposes 1025/8025.
- All services on a shared `users-module-net` network.

Companion files under `deploy/local/`:
- `authelia/configuration.yml` — minimal config with file-backed user database.
- `authelia/users_database.yml` — seeded users with bcrypt hashes documented.
- `.env.example` at `users-module/.env.example` documenting every var (DB_*, OIDC_*, SMTP_*).

## Out of scope
- Wiring api/gui to the compose stack at runtime (Task 1.4 + Phase 3 do this).
- Production/k8s manifests (Phase 8).

## How to verify
- `cd users-module/deploy/local && docker compose up -d` brings all services healthy within 30s.
- `psql postgresql://users:users@localhost:5432/users -c 'SELECT 1'` succeeds.
- `curl http://localhost:9091/.well-known/openid-configuration` returns valid OIDC discovery document.
- `curl http://localhost:8025` returns the mailhog UI.

## Reference
- Plan summary connection-pool note (serverless: 4, local: 20).
- Authelia docs for OIDC client config.
