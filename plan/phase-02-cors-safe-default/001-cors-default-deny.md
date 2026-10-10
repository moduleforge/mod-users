# 001 CORS Default Deny And Startup Guard

## Purpose and scope
Fix `api/internal/server/server.go`, which sets `AllowCredentials: true` unconditionally so an unset `CORS_ORIGINS` yields `Access-Control-Allow-Origin: *` plus credentials in every DEPLOY_MODE.

## Requirements
1. Unset/empty `CORS_ORIGINS` → no CORS headers (no cross-origin access).
2. `AllowCredentials` only when an explicit origin allow-list is configured.
3. In production-like DEPLOY_MODEs, fail startup on `*` combined with credentials (identify the mode enum in config; document which modes count).
4. Local/dev mode keeps a documented permissive convenience only if it never combines `*` with credentials.
5. Docs: CORS section in README/spec, note behaviour change for existing deployments relying on the implicit wildcard (flow-accounts adoption note).

## Validation
- Tests: unset → no ACAO; allow-list match → ACAO echoes origin + credentials; mismatch → no ACAO; `*`+credentials in prod mode → startup error.
- `cd api && go test ./...`.

## Status
- Outcome: succeeded (2026-10-10).
- Validation: `cd api && go test ./...` passed; new tests cover unset/allow-list/mismatch/local-wildcard and the config startup guard.
- Files: `api/internal/server/server.go`, `api/internal/config/config.go`, `api/internal/server/cors_test.go`, `api/internal/config/config_test.go`, `docs/cors.md`, `README.md`, `docs/mod-users-spec.md`, `.env.example`.
- Decisions: production-like = every DEPLOY_MODE except `local`; local keeps `*` without credentials; guard lives in `config.Load` validation, and `server.New` additionally fails closed on `*` outside local.
