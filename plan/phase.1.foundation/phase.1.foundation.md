# Phase 1 — Foundation

## Goal
Stand up the empty `users-module/` monorepo with a working `make dev.start` that brings up Postgres + Authelia and a no-op API/GUI process. This is the scaffolding everything else builds on.

## Outputs
- `users-module/` populated with `model/`, `api/`, `gui/`, `deploy/local/`
- Root `Makefile` with canonical targets (`build`, `test`, `lint`, `lint-fix`, `dev.start`, `dev.stop`, `clean`)
- `go.work` + `pnpm-workspace.yaml`
- `deploy/local/docker-compose.yml` (Postgres 16, Authelia, optional MailHog)
- `.env.example` documenting every required env var
- `api/internal/config/` loader and `api/internal/observability/` OTel bootstrap

## Dependencies
None.

## Notes
- Make targets MUST follow `feedback_make_conventions` memory (e.g., `dev.start`, `test.unit`).
- Add the GNU make version guard at the top of root `Makefile`; fail with install instructions on BSD make.
- Pin Go to 1.23+, Node to 20 LTS, pnpm to latest stable, Postgres to 16.
- Authelia is the local default OIDC provider; configure with two demo users (admin, user).
- Do NOT use Postgres-only types in any code path; Authelia and pgx are runtime-only choices, not schema-leaking.

## Tasks
- 1.1 Monorepo skeleton + workspaces
- 1.2 Make orchestration + GNU make guard
- 1.3 Local docker-compose (Postgres + Authelia)
- 1.4 Shared config + OpenTelemetry bootstrap
