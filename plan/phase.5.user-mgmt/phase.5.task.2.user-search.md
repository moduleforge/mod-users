# Phase 5, Task 2 — User search

## Context
Admins need to find users by email or by free-text on display fields. v1 keeps it simple — `ILIKE` on lowercased columns plus exact email match.

## Acceptance

`GET /v1/users` (admin):
- Query params:
  - `email` — exact (case-insensitive) match.
  - `q` — substring match (case-insensitive) on `email` OR natural_person `given_name`/`family_name` OR corporation `legal_name` OR service_account `label`.
  - `app` — UUID; restricts to users with an `apps_users` row for that app.
  - `is_admin` — `true|false` filter.
  - `archived` — `true|false|all`, default `false`.
  - `limit` (default 50, max 200), `offset`.
- 200 `{ "items": [...], "total": <count>, "limit": ..., "offset": ... }`.
- Each item is a compact user summary (uuid, email, display_name, is_admin, kind, archived, default_app).

`SearchUsers` sqlc query:
- Joins users → entities → legal_entities → natural_persons / corporations / service_accounts.
- Builds the WHERE dynamically — preferred approach is to write the query with COALESCE/optional params and pass NULL for unused filters; sqlc handles this fine.

## How to verify
- Search by email returns single match.
- Substring `q='ali'` matches `alice@…` and `Alice Smith`.
- `?app=<uuid>` excludes users not assigned to that app.
- Pagination total reflects unfiltered count for the given query.

## Notes
- Don't return entity_id, only uuids.
- Keep total query separate from items query; both use the same WHERE.
- Indexing on `lower(email)` already exists (Phase 2 Task 3). Add a `pg_trgm`-free substring strategy — for v1 sequential scan is fine given low row counts; document that we'll revisit if rows > 10k.
