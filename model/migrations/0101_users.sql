-- +goose Up

-- user_accounts: an interactive login identity tied to a Legal Entity.
-- account_holder references legal_entities(entity_id), NOT entities(id), because
-- only legal entities (natural_person, corporation) can hold user accounts.
-- Service accounts (machines) cannot hold user accounts — the FK enforces this.
--
-- OIDC identity is stored in auth_oidc_identities (many per account); there is no
-- single-slot auth_issuer/auth_id here.
CREATE TABLE user_accounts (
  id                BIGSERIAL PRIMARY KEY,
  uuid              UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  account_holder    BIGINT NOT NULL UNIQUE REFERENCES legal_entities(entity_id) ON DELETE RESTRICT,
  email             TEXT NOT NULL UNIQUE,
  email_verified_at TIMESTAMPTZ,
  default_app_id    BIGINT CONSTRAINT user_accounts_default_app_fk REFERENCES apps(id) ON DELETE SET NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Case-insensitive email lookup for login and search.
CREATE INDEX user_accounts_email_lower_idx ON user_accounts(lower(email));

-- The FK user_accounts.account_holder → legal_entities(entity_id) narrows valid
-- holders to concrete legal entity subtypes (natural_person, corporation).
-- The entities.fundamental_type_id trigger guarantees the type is concrete;
-- the legal_entities FK guarantees the holder is a legal entity, excluding
-- service_accounts at the database level.

CREATE TRIGGER user_accounts_set_updated_at
  BEFORE UPDATE ON user_accounts
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
