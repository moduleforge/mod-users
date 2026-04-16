# Phase 6, Task 1 — Apps CRUD

## Acceptance

`api/internal/handlers/apps/apps.go`:

`POST /v1/apps` (admin):
- Body: `{ slug, name }`. Slug normalized to lowercase, `[a-z0-9-]`, length 2–64.
- 409 on slug collision.
- 201 with full app JSON.
- Audit `op='create'`, `resource='apps'`, `target_entity_id=null`.

`GET /v1/apps` (admin):
- Query: `archived` (`true|false|all`, default `false`), `limit`, `offset`, `q` (substring on slug/name).
- 200 `{ items, total }`.

`GET /v1/apps/:uuid` (admin):
- 200 with app JSON + `member_count`.

`PUT /v1/apps/:uuid` (admin):
- Editable: `name`, `slug` (with collision check).
- 200.
- Audit `op='update'` with before/after.

`DELETE /v1/apps/:uuid` (admin):
- Sets `archived_at = now()`.
- All `apps_users` rows remain (membership history preserved).
- For users where this was their `default_app_id`, clear it (in same transaction); audit each clearance as a separate `op='update'` row.
- 204.

## How to verify
- Create then get → 200 with member_count=0.
- Create with duplicate slug → 409.
- Archive an app that's the default for two users → both users' `default_app_id` becomes NULL; two audit rows written.
- Archived apps are excluded by default from list; included with `?archived=all`.

## Notes
- We do NOT model apps as entities in v1 (no row in `entities` for an app). If we ever want app-targeted audit lookups, add `target_kind` to audit_log in v2.
