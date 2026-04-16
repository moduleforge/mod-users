# Phase 2, Task 2 — Entity hierarchy migrations

## Context
The CTI (class-table inheritance) hierarchy is the foundation of the data model. Every authorization subject and target descends from `entities`.

## Acceptance
Five migration files, dependency-ordered:

### `0001_entities.sql`
```sql
CREATE TABLE entities (
  id           BIGSERIAL PRIMARY KEY,
  uuid         UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  kind         TEXT NOT NULL CHECK (kind IN ('legal_entity','service_account')),
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at  TIMESTAMPTZ
);
CREATE INDEX entities_kind_idx ON entities(kind);
CREATE INDEX entities_archived_at_idx ON entities(archived_at) WHERE archived_at IS NOT NULL;
```
- Requires `pgcrypto` extension; create it in this migration. Document this single Postgres dep in `model/README.md`.

### `0002_legal_entities.sql`
```sql
CREATE TABLE legal_entities (
  id          BIGSERIAL PRIMARY KEY,
  entity_id   BIGINT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE RESTRICT,
  kind        TEXT NOT NULL CHECK (kind IN ('natural_person','corporation')),
  display_name TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX legal_entities_kind_idx ON legal_entities(kind);
```

### `0003_natural_persons.sql`
```sql
CREATE TABLE natural_persons (
  id              BIGSERIAL PRIMARY KEY,
  legal_entity_id BIGINT NOT NULL UNIQUE REFERENCES legal_entities(id) ON DELETE RESTRICT,
  given_name      TEXT,
  family_name     TEXT,
  -- legal_id intentionally omitted in v1 (PII)
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### `0004_corporations.sql`
```sql
CREATE TABLE corporations (
  id              BIGSERIAL PRIMARY KEY,
  legal_entity_id BIGINT NOT NULL UNIQUE REFERENCES legal_entities(id) ON DELETE RESTRICT,
  legal_name      TEXT NOT NULL,
  jurisdiction    TEXT,
  -- legal_id intentionally omitted in v1
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

### `0005_service_accounts.sql`
```sql
CREATE TABLE service_accounts (
  id          BIGSERIAL PRIMARY KEY,
  entity_id   BIGINT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE RESTRICT,
  label       TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## How to verify
- `make model.migrate.up` against an empty database succeeds.
- `make model.verify` (atlas validate) reports clean.
- Manual: `INSERT INTO entities(kind) VALUES ('legal_entity') RETURNING id, uuid;` returns a row.

## Notes
- All FKs upward only.
- No views.
- Add `updated_at` trigger function in a small `0000_helpers.sql` (run before `0001`) — `set_updated_at()` setting `NEW.updated_at = now()`. Apply via per-table `BEFORE UPDATE` trigger in each migration above.
