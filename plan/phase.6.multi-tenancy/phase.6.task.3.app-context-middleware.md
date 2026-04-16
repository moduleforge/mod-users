# Phase 6, Task 3 — Per-request app context middleware

## Context
Already stubbed in Phase 3 (`auth.WithAppContext`). This task delivers the real implementation now that apps and apps_users exist.

## Acceptance

`api/internal/auth/appcontext.go`:
- `WithAppContext(pool *pgxpool.Pool) func(http.Handler) http.Handler`.
- Steps:
  1. Read `X-App: <uuid>` header. If present, look up app by uuid. 410 if archived. 404 if unknown.
  2. If header missing, use `users.default_app_id`. If null and the route is app-scoped (default in v1: ALL non-admin, non-`/self/*`, non-`/auth/*` routes), 400 `app_required`.
  3. Load `apps_users` row for `(app_id, user_id)`. If absent AND the caller is not admin, 403 `not_a_member`.
  4. Set `UserContext.AppID` and `UserContext.AppRoles`.

Mount strategy:
- Apply `WithAppContext` AFTER `RequireAuth` on the `/v1` subrouter, but allow handlers to opt out via a tag in route metadata (or simply mount unscoped routes — `/self/*`, `/auth/*`, `/users/*` admin endpoints — in a sibling group that does NOT use `WithAppContext`).

## How to verify
- Request with `X-App: <archived uuid>` → 410.
- Non-admin without header and without default_app → 400.
- Non-admin not assigned to the app → 403.
- Admin without app context can hit admin routes (e.g., `/v1/apps`) without `X-App` header.

## Notes
- For v1, app-scoping inside users-module's own endpoints is mostly a contract for downstream consumers. Inside this module, `/v1/users/:uuid` keeps its self-or-admin rule. `/v1/users` (search) becomes app-scoped: non-admins can't hit it; admins see global.
- Document the route-grouping decision in `api/internal/server/router.go`.
