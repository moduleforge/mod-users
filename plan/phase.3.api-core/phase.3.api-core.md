# Phase 3 — API core

## Goal
Bring the Go service to a state where authenticated requests work end-to-end against Authelia (or any OIDC provider), `/v1/self` returns the caller's profile, and audits are written for mutations.

## Outputs
- `api/cmd/server/main.go` finalized (loads config, opens pgx pool, builds router, starts/stops cleanly)
- `api/internal/db/pool.go` — pgx v5 pool with mode-aware sizing
- `api/internal/auth/{claims,middleware,principal,jwt}.go` — ClaimMapper + middleware
- `api/internal/handlers/{health,self}.go`
- `api/internal/audit/audit.go` — context-threaded writer

## Hard rules
- pgx v5 only; no ORM.
- All handlers return `application/json`; errors as `{ "error": { "code": "...", "message": "...", "details": ... } }`.
- Every mutating handler MUST call `audit.Write(ctx, op, resource, before, after)` before returning success.
- Pool sizing per `Config.MaxConns` (default 4 serverless, 20 otherwise).
- Graceful shutdown wired in main: SIGTERM/SIGINT → `server.Shutdown` → `pool.Close()` → `otel.Shutdown` within 25s.

## Tasks
- 3.1 Service skeleton (chi, pgx pool, slog, OTel, graceful shutdown)
- 3.2 ClaimMapper interface + provider implementations
- 3.3 OIDC middleware, Principal-on-context, role mapping
- 3.4 `/healthz`, `/readyz`, `/v1/self` GET/PUT
- 3.5 Audit-log writer hooked into mutation handlers

## Notes
- `Principal` struct: `{ Subject, Issuer, Email, Roles []string }` — what mappers produce.
- After middleware resolves Principal, a separate step (`resolveUser`) loads or upserts the `users` row keyed by `(auth_issuer, auth_id)` and stores `*UserContext` on context: `{ User, IsAdmin, AssumedUser, AppID }`. Handlers depend on `UserContext`, not raw Principal.
- For local auth (Phase 4), the JWT issuer is the api itself (`OIDC_LOCAL_ISSUER`); local-auth middleware path bypasses the OIDC verifier but produces the same `UserContext`.
