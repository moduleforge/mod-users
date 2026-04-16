# Phase 5, Task 5 — Audit endpoints

## Context
CLAUDE.md: "admins can see what changes a user performed" and "admins can see what changes were made to an object". Plus a recent-activity firehose.

## Acceptance

`GET /v1/users/:uuid/audit` (admin):
- Returns audit rows where `actor_user_id = user.id` OR `assumed_user_id = user.id` (so assumed-as-Bob actions show in Bob's history too — important for accountability).
- Query params: `op` (filter), `from`, `to` (RFC3339), `limit` (default 50, max 200), `offset`.
- 200 `{ items: [...], total }`.

`GET /v1/audit/:object_uuid` (admin):
- `:object_uuid` is the entity uuid of any tracked object (a user's entity uuid, an app uuid will require a lookup adapter).
- Returns audit rows where `target_entity_id` resolves to that uuid.
- Same query params as above.

`GET /v1/audit` (admin):
- Recent global audit (paginated). Filterable by `op`, `actor_uuid`, `from`, `to`.
- 200 `{ items, total }`.

Audit row shape in responses:
```json
{
  "at": "2026-...",
  "op": "update",
  "resource": "users",
  "actor": { "uuid": "...", "email": "..." },
  "assumed": { "uuid": "...", "email": "..." } | null,
  "target": { "uuid": "...", "kind": "natural_person|..." } | null,
  "before": { ... } | null,
  "after":  { ... } | null
}
```

## How to verify
- Trigger a few mutations as different actors; query each endpoint; counts and ordering (DESC by `at`) correct.
- Assumed-action shows up under both admin's audit and assumed user's audit.
- Filtering by `op=login` returns only login rows.

## Notes
- For now, `audit/:object_uuid` only supports user/entity uuids. Apps audit will piggyback once we treat apps as entities (out of scope v1 — track in Phase 6 if it falls out naturally; otherwise add a small `target_kind` column in v2).
