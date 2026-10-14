# Readyz Generic Body

> **Ephemeral plan document.** This file is ephemeral and will be removed after the implementation is complete. Project documents must be updated or created to capture the context and information contained herein. Link only from other plan documents; do not link to this document from non-plan documents.

## Purpose and scope

Backlog item FAVu. `Ready` in `api/internal/handlers/health.go` writes `err.Error()` from `pool.Ping` into the body of the unauthenticated `GET /readyz` 503 response, which can leak host, port, user, and database names. Return a fixed value and log the detail.

## Requirements

1. On ping failure, log with `slog.ErrorContext(r.Context(), "readyz: database ping failed", "error", err)` and respond 503 with `{"status":"not_ready","db":"unavailable"}`. The success body is unchanged.
2. `Ready` takes a `*pgxpool.Pool`, which is hard to fake. Either introduce a small unexported/exported interface (`Ping(ctx) error`) accepted by `Ready` while keeping the call site `handlers.Ready(pool)` in `api/cmd/server/main.go` compiling, or add a helper that takes a ping func. Public signature compatibility matters: `*pgxpool.Pool` must still be accepted.
3. Add a unit test: a failing pinger yields 503, body `db` equals `unavailable`, and the body does not contain the sentinel error text (for example a fake error `dial tcp 10.0.0.5:5432 user=secret db=prod`); a passing pinger yields 200 with `db: ok`.
4. Update `api/openapi.yaml` `/readyz` 503 description to say the `db` field is the fixed value `unavailable`, and `docs/mod-users-spec.md` line for `GET /readyz` if it implies detail.

## Validation

- `cd api && go test ./internal/handlers/...` passes with the new test.
- `grep -n "err.Error()" api/internal/handlers/health.go` returns nothing.
- `cd api && go build ./...` and `go vet ./...` pass.

## Metadata

architectural_impact: false

## Status

succeeded, 2026-10-14. Validation: go test ./internal/handlers/... passed; grep for err.Error() in health.go empty; go build and go vet passed. Files: api/internal/handlers/health.go, api/internal/handlers/health_test.go, api/openapi.yaml, docs/mod-users-spec.md.
