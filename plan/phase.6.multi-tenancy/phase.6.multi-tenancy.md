# Phase 6 — Multi-tenancy

## Goal
Apps are first-class. Admins can create apps, assign users, and grant per-app roles. Every authenticated request runs in an app context (default or `X-App` override). Authorization scopes by app context: regular users see only their own data within their app; admins are global.

## Endpoints (under `/v1`, all require auth; admin-only flagged)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/apps` | admin | Create app (`{slug, name}`) |
| GET | `/apps` | admin | List apps |
| GET | `/apps/:uuid` | admin | App detail |
| PUT | `/apps/:uuid` | admin | Update app |
| DELETE | `/apps/:uuid` | admin | Archive app |
| GET | `/apps/:uuid/users` | admin | List app's user assignments |
| POST | `/apps/:uuid/users` | admin | Assign user `{user_uuid, roles[]}` |
| PUT | `/apps/:uuid/users/:user_uuid` | admin | Update roles |
| DELETE | `/apps/:uuid/users/:user_uuid` | admin | Remove assignment |
| GET | `/self/apps` | any | List apps the caller belongs to |

## Hard rules
- `WithAppContext` middleware (Phase 3 Task 3.3) reads `X-App: <uuid>` header → resolves to `app.id` → loads the caller's `apps_users` row → fills `UserContext.AppID` and `UserContext.AppRoles`.
- If header missing: use `users.default_app_id`. If still null AND the caller is not admin AND the endpoint is app-scoped: 400 `{"error":{"code":"app_required"}}`.
- Admins bypass the requirement; admin-only endpoints don't need an app context.
- Removing a user from their own `default_app` clears `default_app_id` (audit captured).
- Archiving an app sets `apps.archived_at`; subsequent requests with `X-App` pointing to it return 410 Gone.

## Tasks
- 6.1 Apps CRUD
- 6.2 Apps_users assignment
- 6.3 Per-request app context middleware
- 6.4 Authorization scoping (admin global; user limited to own data)

## Notes
- "Authorization scoping" in v1 is narrow: the only user-data endpoints exist within users-module itself, and `GET /v1/users/:uuid` already enforces self-or-admin. The bigger payoff comes when downstream services consume `UserContext.AppID + AppRoles` to scope their own data. Document this contract clearly so consumers know what to read off context.
- App-scoped audit query (`/v1/apps/:uuid/audit`) is OUT of scope for v1 (audit_log doesn't carry app_id). Note as v2.
