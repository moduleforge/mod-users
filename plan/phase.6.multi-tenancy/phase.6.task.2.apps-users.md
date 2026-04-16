# Phase 6, Task 2 — Apps_users assignment

## Acceptance

`api/internal/handlers/apps/membership.go`:

`GET /v1/apps/:uuid/users` (admin):
- Query: `q`, `role`, `limit`, `offset`.
- 200 `{ items: [{ user: {uuid, email, display_name}, roles, assigned_at }], total }`.

`POST /v1/apps/:uuid/users` (admin):
- Body: `{ user_uuid, roles: [string] }`. Roles trimmed/lowercased; max 32 per user.
- 409 if assignment already exists.
- 201 with assignment JSON.
- Audit `op='grant'`, `resource='apps_users'`, `before=null`, `after={app, user, roles}`.

`PUT /v1/apps/:uuid/users/:user_uuid` (admin):
- Body: `{ roles: [string] }` — replaces the array (not merge).
- 200.
- Audit `op='update'` with before/after roles.

`DELETE /v1/apps/:uuid/users/:user_uuid` (admin):
- Removes the assignment.
- If this was the user's `default_app`, clear `default_app_id`.
- 204.
- Audit `op='revoke'`.

`GET /v1/self/apps`:
- Lists apps the caller belongs to (excluding archived).
- 200 `{ items: [{ app: {uuid, slug, name}, roles, is_default }] }`.

## How to verify
- Assign then list → user appears with given roles.
- Duplicate assign → 409.
- Update roles to `["editor"]`; audit shows role swap.
- Delete a user's only app — that user's `default_app_id` cleared.
- Self listing only shows non-archived apps.

## Notes
- Per-app role semantics are owned by downstream consumers; users-module does not interpret role names beyond storing/returning them.
