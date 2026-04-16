# Phase 1, Task 4 — Shared config + OpenTelemetry bootstrap

## Context
The api needs a single config loader (env → typed struct), structured logging via `log/slog`, and OpenTelemetry tracing/metrics exported via OTLP. These are infra concerns the rest of the API will lean on.

## Acceptance
`api/internal/config/config.go`:
- Exports `type Config struct { ... }` covering DB (URL, MaxConns, MaxConnLifetime, MaxConnIdleTime), OIDC (IssuerURL, ClientID, ClientSecret, ClaimStyle, AdminRole), Server (Addr, ShutdownTimeout), Local Auth (JWTSecret, EmailCodeTTL, PasswordResetTTL), SMTP (Host, Port, From, User, Pass), Otel (ExporterEndpoint, ServiceName), DeployMode (`local|serverless|k8s`).
- `Load() (*Config, error)` reads env, applies per-mode defaults: `MaxConns` defaults to 4 in `serverless`, 20 otherwise; `ShutdownTimeout` defaults to 25s.
- Validates required fields (returns descriptive error listing all missing).

`api/internal/observability/otel.go`:
- `Init(ctx, cfg) (shutdown func(context.Context) error, err error)`.
- Configures tracer + meter providers with OTLP HTTP exporter.
- Gracefully no-ops if `OTEL_EXPORTER_OTLP_ENDPOINT` is empty (local dev convenience).

`api/cmd/server/main.go`:
- Loads config, inits OTel, sets up `slog` JSON handler, prints "users-api up on :PORT" via slog.
- Catches SIGTERM/SIGINT, calls shutdown chain (server.Shutdown → pgx pool close — pool added in Phase 3 — → otel shutdown), 25s timeout, exits 0 on clean shutdown.

## Out of scope
- pgx pool itself (Phase 3 Task 3.1; config struct just holds settings now).
- HTTP routes (Phase 3).

## How to verify
- `cd users-module/api && go test ./internal/config/...` passes (write a basic table-driven test for Load).
- `make dev.start` brings up compose, then `go run ./cmd/server` logs structured "users-api up" lines and shuts down cleanly on Ctrl-C.

## Reference
- Plan summary "Architectural pillars" — pool sizing and shutdown specs.
