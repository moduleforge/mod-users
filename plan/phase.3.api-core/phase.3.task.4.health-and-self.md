# Phase 3, Task 4 — `/healthz`, `/readyz`, `/v1/self`

## Context
Health endpoints are required by Cloud Run/k8s. `/v1/self` is the first real authenticated endpoint and a smoke test for the entire auth chain.

## Acceptance

`api/internal/handlers/health.go`:
- `GET /healthz` → 200, body `{"status":"ok"}` (liveness; no DB check).
- `GET /readyz` → 200 if `pool.Ping(ctx)` succeeds, 503 otherwise. Body `{"status":"ready|not_ready","db":"ok|<error>"}`.

`api/internal/handlers/self.go`:
- `GET /v1/self` (auth required) → 200 with the caller's profile:
  ```json
  {
    "uuid": "...",
    "email": "...",
    "email_verified": true,
    "is_admin": true,
    "default_app": { "uuid": "...", "slug": "..." } | null,
    "entity": { "uuid": "...", "kind": "natural_person|corporation|service_account", "...": "..." }
  }
  ```
- `PUT /v1/self` (auth required) — accepts editable profile fields (`given_name`, `family_name`, `default_app_uuid`). Email changes go through a verification flow (Phase 4); reject email changes here.
- Both write to `audit_log` via the writer (Task 3.5) on the PUT path.

Mount points:
```
r.Get("/healthz", health.Live)
r.Get("/readyz", health.Ready(pool))

r.Route("/v1", func(r chi.Router) {
  r.Group(func(r chi.Router) {
    r.Use(auth.RequireAuth(verifier, mapper, resolver))
    r.Use(auth.WithAppContext(...))
    r.Get("/self", self.Get(...))
    r.Put("/self", self.Put(...))
  })
})
```

## How to verify
- `curl /healthz` → 200 OK without docker-compose db running (must NOT fail).
- `curl /readyz` → 503 with db down, 200 with db up.
- With Authelia running and a valid token, `GET /v1/self` returns the right user JSON; second request shows the user persisted (no duplicate row).
- `PUT /v1/self` with `{"given_name":"Foo"}` updates and writes an `audit_log` row with `op='update'`, `resource='users'`.

## Notes
- `/healthz` MUST be cheap and never fail even during DB outage; it's the liveness probe.
- The `entity` block in `GET /v1/self` requires joining users → entities → (legal_entities → natural_persons|corporations) | service_accounts. Do this join in the api layer, not via a SQL view.
