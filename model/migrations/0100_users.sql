CREATE TABLE users (
  id                BIGSERIAL PRIMARY KEY,
  uuid              UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  entity_id         BIGINT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE RESTRICT,
  email             TEXT NOT NULL UNIQUE,
  email_verified_at TIMESTAMPTZ,
  is_admin          BOOLEAN NOT NULL DEFAULT FALSE,
  default_app_id    BIGINT, -- FK added in 0104_apps.sql after apps table exists
  auth_issuer       TEXT,
  auth_id           TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Compound partial unique index for OIDC identity lookup.
-- Partial: only enforced when both columns are non-null (local-only users have NULL).
CREATE UNIQUE INDEX users_auth_idx
  ON users(auth_issuer, auth_id)
  WHERE auth_issuer IS NOT NULL AND auth_id IS NOT NULL;

-- Case-insensitive email lookup for login and search.
CREATE INDEX users_email_lower_idx ON users(lower(email));

-- Note: the concrete-leaf invariant is enforced at the core layer via the
-- trigger on entities.fundamental_type_id, which rejects non-concrete types
-- at insert time. The FK users.entity_id → entities(id) guarantees the row
-- exists; the core trigger guarantees its fundamental type is concrete.
-- Therefore every users.entity_id inherently points at a concrete leaf entity,
-- and a redundant users-level trigger is not needed here.

CREATE TRIGGER users_set_updated_at
  BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
