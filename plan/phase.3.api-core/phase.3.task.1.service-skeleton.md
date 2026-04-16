# Phase 3, Task 1 — Service skeleton

## Context
The api needs a real, production-grade entrypoint: chi router, pgx pool with sane sizing, slog, OTel hooks from Phase 1, and a graceful shutdown chain that won't leak connections under Cloud Run/k8s rolling deploys.

## Acceptance
- `api/internal/db/pool.go`: `New(ctx, cfg) (*pgxpool.Pool, error)` building the pool with `MaxConns`, `MaxConnLifetime`, `MaxConnIdleTime` from `Config`. `Ping()` on creation. Wraps errors with context.
- `api/internal/server/server.go`: `New(cfg, pool, logger) *http.Server` builds a chi `Router`, mounts middleware in order: request ID → recoverer → slog access log → OTel tracing → CORS (allowlist from `Config.CORSOrigins`).
- `api/cmd/server/main.go` finalized: load config → init OTel → open pool → build server → `ListenAndServe` in a goroutine → block on signal → orchestrated shutdown.
- Shutdown sequence (with shared 25s context deadline): `srv.Shutdown(ctx)` → `pool.Close()` → `otelShutdown(ctx)`. Log each step. Exit non-zero only if any step errors.
- Add `/v1` subrouter as the canonical mount point; handlers in later tasks attach to it.

## How to verify
- `cd users-module && make dev.start` → `curl localhost:8080/healthz` returns 200 (placeholder; real handler in Task 3.4).
- Send SIGTERM; logs show "shutting down server", "closing pool", "flushing otel"; process exits 0.
- Under high load, killing the process mid-request drains in-flight connections rather than dropping them.

## Notes
- Use `chi/v5`. No gorilla/mux.
- Use `pgxpool.Config` directly — do NOT use `database/sql`.
- Request log fields: method, path, status, duration_ms, user_uuid (set later by auth middleware).
