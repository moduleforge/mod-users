-- +goose Up

-- ---------------------------------------------------------------------------
-- apps
-- ---------------------------------------------------------------------------
CREATE TABLE apps (
  id          BIGSERIAL PRIMARY KEY,
  uuid        UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  slug        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ
);

CREATE TRIGGER apps_set_updated_at
  BEFORE UPDATE ON apps
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- user_accounts
-- ---------------------------------------------------------------------------
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
  email             TEXT UNIQUE,
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

-- ---------------------------------------------------------------------------
-- auth_local
-- ---------------------------------------------------------------------------
CREATE TABLE auth_local (
  user_account_id     BIGINT PRIMARY KEY REFERENCES user_accounts(id) ON DELETE CASCADE,
  password_hash       TEXT NOT NULL,            -- argon2id encoded string
  password_updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at          TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- email_codes
-- ---------------------------------------------------------------------------
CREATE TABLE email_codes (
  id              BIGSERIAL PRIMARY KEY,
  user_account_id BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  code_hash       TEXT NOT NULL,                     -- sha256(salt+code) or bcrypt
  purpose         TEXT NOT NULL CHECK (purpose IN ('login', 'verify_email')),
  expires_at      TIMESTAMPTZ NOT NULL,
  consumed_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Index for looking up active (unconsumed) codes per user account and purpose.
CREATE INDEX email_codes_user_account_purpose_idx
  ON email_codes(user_account_id, purpose)
  WHERE consumed_at IS NULL;

-- ---------------------------------------------------------------------------
-- password_resets
-- ---------------------------------------------------------------------------
CREATE TABLE password_resets (
  id              BIGSERIAL PRIMARY KEY,
  user_account_id BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  token_hash      TEXT NOT NULL UNIQUE,              -- sha256 of opaque token
  expires_at      TIMESTAMPTZ NOT NULL,
  consumed_at     TIMESTAMPTZ,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------------
-- apps_user_accounts
-- ---------------------------------------------------------------------------
CREATE TABLE apps_user_accounts (
  app_id          BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  user_account_id BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  roles           TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
  assigned_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (app_id, user_account_id)
);

-- Index for "list all apps for a given user account" queries.
CREATE INDEX apps_user_accounts_user_account_idx ON apps_user_accounts(user_account_id);

-- ---------------------------------------------------------------------------
-- oidc_config
-- ---------------------------------------------------------------------------
-- oidc_config holds the singleton row that captures the operator's
-- confirmed choices for the OIDC onboarding flow. Only one row ever
-- exists (id = 1, enforced by CHECK); the design choice is deliberate —
-- "current configuration" is a singleton, and keeping the table shape
-- trivial simplifies the upsert + query layer. Per-provider on/off state
-- is owned by oidc_providers.enabled, not this table.
--
-- Columns:
--   opt_out          : persists a "local-auth only" choice made through
--                      the confirm UI. Equivalent in effect to the
--                      NO_OIDC_ACCOUNTS env flag but survives env-var
--                      changes across restarts.
--   setup_token_hash : sha256 hex of the active one-time setup token used
--                      to authorize the /v1/oidc-config/confirm endpoint
--                      when no admin session exists yet. NULL when no
--                      token is active (i.e., state is confirmed).
--   setup_token_created_at : emission timestamp for debugging / auditing.
--   saved_at         : last-saved wall-clock timestamp; powers the GUI
--                      "last saved" label and the revert button.
CREATE TABLE oidc_config (
    id                     INTEGER PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    opt_out                BOOLEAN NOT NULL DEFAULT FALSE,
    setup_token_hash       TEXT,
    setup_token_created_at TIMESTAMPTZ,
    saved_at               TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the singleton row so downstream queries can always UPDATE rather
-- than UPSERT. The id = 1 CHECK guarantees subsequent inserts fail loudly.
INSERT INTO oidc_config (id) VALUES (1);

-- ---------------------------------------------------------------------------
-- oidc_providers
-- ---------------------------------------------------------------------------
-- oidc_providers stores per-provider DB overrides for the OIDC provider
-- registry (phase 9.11a). A row here means "the operator edited this
-- provider via the admin GUI"; any NULL column means "no override — use
-- env value if set, otherwise well-known default". The merge layer in
-- api/internal/config/provider_merge.go reads env + this table and
-- produces the effective Provider that the OAuth orchestrator sees.
--
-- Design notes:
--   * id is a slug (lowercase letters / digits / dashes, 2-32 chars,
--     no leading/trailing dash). Matches the env-var naming convention
--     (AUTH_PROVIDER_{ID}_*) once lowercased.
--   * scopes is TEXT[] rather than JSONB because pgx maps it cleanly to
--     []string and we never need partial-path queries into it.
--   * enabled is NOT NULL — a DB row always has an explicit on/off
--     opinion. To "remove the override" the revert endpoint deletes the
--     row entirely, falling back to the pre-existing confirm-flow
--     provider_enabled JSONB in oidc_config.
--   * client_secret is stored plaintext; this matches the env model
--     (AUTH_PROVIDER_*_CLIENT_SECRET is plaintext in .env). The GUI
--     never reads this back — a has_client_secret boolean is surfaced
--     instead.
--   * updated_at is maintained by the shared set_updated_at() trigger
--     defined in 0001_helpers.sql.
CREATE TABLE oidc_providers (
    id TEXT PRIMARY KEY
        CHECK (id ~ '^[a-z][a-z0-9-]{0,30}[a-z0-9]$'),
    display_name   TEXT,
    issuer_url     TEXT,
    client_id      TEXT,
    client_secret  TEXT,
    claim_style    TEXT,
    scopes         TEXT[],
    enabled        BOOLEAN     NOT NULL DEFAULT TRUE,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TRIGGER oidc_providers_set_updated_at
BEFORE UPDATE ON oidc_providers
FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- ---------------------------------------------------------------------------
-- auth_oidc_identities
-- ---------------------------------------------------------------------------
CREATE TABLE auth_oidc_identities (
  id                    BIGSERIAL PRIMARY KEY,
  uuid                  UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  user_account_id       BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  issuer                TEXT NOT NULL,
  subject               TEXT NOT NULL,
  email                 TEXT,                  -- snapshot at link time, informational
  email_verified_at_idp TIMESTAMPTZ,           -- informational; the account-level flag is canonical
  linked_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (issuer, subject)
);
CREATE INDEX auth_oidc_identities_user_account_idx ON auth_oidc_identities(user_account_id);

-- +goose Down
-- intentionally omitted — base schema, no rollback
