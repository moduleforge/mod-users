# Phase 2, Task 6 — Audit log migration + insert helper

## Context
Every mutating API call writes an audit row. Admins query audit by actor (per-user activity) or by target (per-object change history).

## Acceptance

`0012_audit_log.sql`:
```sql
CREATE TABLE audit_log (
  id                BIGSERIAL PRIMARY KEY,
  actor_user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
  assumed_user_id   BIGINT REFERENCES users(id) ON DELETE RESTRICT,
  target_entity_id  BIGINT REFERENCES entities(id) ON DELETE SET NULL,
  op                TEXT NOT NULL CHECK (op IN ('create','update','delete','assume','login','grant','revoke')),
  resource          TEXT NOT NULL,           -- e.g. 'users','apps','apps_users'
  before            JSONB,
  after             JSONB,
  at                TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_actor_at_idx  ON audit_log(actor_user_id, at DESC);
CREATE INDEX audit_log_target_at_idx ON audit_log(target_entity_id, at DESC) WHERE target_entity_id IS NOT NULL;
CREATE INDEX audit_log_op_at_idx     ON audit_log(op, at DESC);
```

## How to verify
- Migration applies.
- Insert with `op='login'` succeeds; `op='hack'` raises CHECK violation.
- Target lookup query plan uses `audit_log_target_at_idx`.

## Notes
- Append-only; no UPDATE/DELETE handlers will be written. Retention is a future concern.
- The Go-side audit writer (Phase 3 Task 3.5) is responsible for serializing `before`/`after` as JSONB and threading the actor/assumed user through context.
