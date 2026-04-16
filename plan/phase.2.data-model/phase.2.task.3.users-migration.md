# Phase 2, Task 3 — Users migration with leaf-kind trigger and UNIQUE auth index

## Context
`users` is a role extension over the entity hierarchy — a user must reference a leaf entity (natural_person, corporation, or service_account). Postgres CHECK constraints can't cross tables, so we enforce this with a trigger. The OIDC lookup index is correctness-critical (prevents duplicate identities) and performance-critical (every authenticated request hits it).

## Acceptance
`0006_users.sql`:
```sql
CREATE TABLE users (
  id                   BIGSERIAL PRIMARY KEY,
  uuid                 UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  entity_id            BIGINT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE RESTRICT,
  email                TEXT NOT NULL UNIQUE,
  email_verified_at    TIMESTAMPTZ,
  is_admin             BOOLEAN NOT NULL DEFAULT FALSE,
  default_app_id       BIGINT, -- FK added in apps migration
  auth_issuer          TEXT,
  auth_id              TEXT,
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_auth_idx
  ON users(auth_issuer, auth_id)
  WHERE auth_issuer IS NOT NULL AND auth_id IS NOT NULL;
CREATE INDEX users_email_lower_idx ON users(lower(email));

-- Trigger: enforce that entity_id is a leaf entity (natural_person, corporation, or service_account)
CREATE OR REPLACE FUNCTION users_enforce_leaf_entity() RETURNS TRIGGER AS $$
DECLARE
  v_entity_kind TEXT;
  v_legal_kind  TEXT;
BEGIN
  SELECT kind INTO v_entity_kind FROM entities WHERE id = NEW.entity_id;
  IF v_entity_kind IS NULL THEN
    RAISE EXCEPTION 'users.entity_id % does not exist', NEW.entity_id;
  END IF;
  IF v_entity_kind = 'service_account' THEN
    -- service_account is itself a leaf
    RETURN NEW;
  END IF;
  IF v_entity_kind = 'legal_entity' THEN
    SELECT kind INTO v_legal_kind FROM legal_entities WHERE entity_id = NEW.entity_id;
    IF v_legal_kind IN ('natural_person','corporation') THEN
      RETURN NEW;
    END IF;
    RAISE EXCEPTION 'users.entity_id % is a legal_entity but not a leaf kind (got %)', NEW.entity_id, v_legal_kind;
  END IF;
  RAISE EXCEPTION 'users.entity_id % has unknown kind %', NEW.entity_id, v_entity_kind;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_enforce_leaf_entity_trg
  BEFORE INSERT OR UPDATE OF entity_id ON users
  FOR EACH ROW EXECUTE FUNCTION users_enforce_leaf_entity();

CREATE TRIGGER users_set_updated_at
  BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

`model/scripts/relink_auth.sql`:
- Documented script taking `:user_uuid`, `:new_issuer`, `:new_auth_id`, optional `:old_issuer`. Updates `auth_issuer`/`auth_id` on the matched user; verifies no conflict via `users_auth_idx`. Wrapped in a transaction.

## How to verify
- Apply migration; `INSERT INTO users(entity_id, email) VALUES (<entity for natural_person>, 'a@b')` succeeds.
- `INSERT INTO users(entity_id, email) VALUES (<entity for legal_entity NOT in natural/corp>, 'x@y')` raises.
- Inserting two users with same `(auth_issuer, auth_id)` raises unique violation.

## Notes
- `default_app_id` FK is **deferred** — we'll add it in Task 2.5 after `apps` exists, via an `ALTER TABLE`.
