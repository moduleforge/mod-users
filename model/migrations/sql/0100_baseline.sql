-- +goose Up

-- mod-users baseline schema.
--
-- Collapsed on 2026-10-04 from the former migrations 0100 (tables), 0101
-- (system actors), 0102 (auth_jwt_secrets) and 0103 (mod_users.ssh_public_keys)
-- while the module was still pre-release. Existing development databases that
-- recorded those versions in goose_db_version_users must be dropped and
-- recreated (make clean.data).

-- ===========================================================================
-- Core tables
-- ===========================================================================

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

-- ---------------------------------------------------------------------------
-- anon_tokens
-- ---------------------------------------------------------------------------
-- anon_tokens: short-lived session tokens for anonymous (non-authenticated)
-- users. session_token stores the SHA-256 hex hash of the opaque bearer token
-- (consistent with the password_resets pattern above). The user_account_id FK
-- cascades deletes so tokens are cleaned up automatically if the parent
-- user_account row is hard-deleted. user_accounts.email is already nullable
-- above to support anonymous accounts.
CREATE TABLE anon_tokens (
  id              BIGSERIAL PRIMARY KEY,
  uuid            UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  device_id       TEXT NOT NULL,
  session_token   TEXT NOT NULL UNIQUE,
  user_account_id BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL
);

CREATE INDEX anon_tokens_device_id_idx       ON anon_tokens(device_id);
CREATE INDEX anon_tokens_session_token_idx   ON anon_tokens(session_token);
CREATE INDEX anon_tokens_user_account_id_idx ON anon_tokens(user_account_id);

-- ===========================================================================
-- System actors
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- (a) Seed the 'system_actor' type
-- ---------------------------------------------------------------------------
-- system_actor is a concrete type directly under the root 'entity' type, not
-- under 'legal_entity'. This is load-bearing in three ways (per the anonymous
-- actor architecture proposal):
--   * It never self-owns: entities_owner_default_self (mod-core's
--     0013_entity_ownership.sql) fires only for types descending from
--     natural_person or service_account, neither of which system_actor is,
--     so a system_actor entity keeps owner_id NULL.
--   * It cannot join an actor group: authz_actor_group_members' type-check
--     trigger (mod-authz's 0502_authz_actor_group_members.sql) admits only
--     authz_actor_group and legal_entity descendants.
--   * It stays invisible to every list path: system_actor is deliberately
--     never added to authzSlugs in api/cmd/server/main.go, so no
--     accessible_system_actor_ids_for_actor function is ever generated.
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM types WHERE slug = 'entity') THEN
    RAISE EXCEPTION 'seed for system_actor requires parent slug ''entity'' to exist';
  END IF;
END $$;
-- +goose StatementEnd

INSERT INTO types (slug, parent_id, concrete, name, description)
SELECT
  'system_actor',
  id,
  true,
  'System Actor',
  'A zero-authority, platform-seeded principal (e.g. the shared anonymous actor). Never owns entities, never holds grants, never appears in list results.'
FROM types WHERE slug = 'entity';

-- ---------------------------------------------------------------------------
-- (b) system_actors CTI table
-- ---------------------------------------------------------------------------
-- No updated_at / set_updated_at: system_actors rows are seed data and are
-- never updated after insert.
CREATE TABLE system_actors (
  entity_id  BIGINT PRIMARY KEY REFERENCES entities(id) ON DELETE RESTRICT,
  slug       TEXT UNIQUE NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Enforce that the referenced entity's fundamental type is exactly
-- 'system_actor' (or a descendant of it).
-- +goose StatementBegin
CREATE FUNCTION system_actors_check_type() RETURNS TRIGGER AS $$
DECLARE
  v_type_id BIGINT;
BEGIN
  SELECT fundamental_type_id INTO v_type_id FROM entities WHERE id = NEW.entity_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'system_actors: entity_id % does not reference a known entity', NEW.entity_id;
  END IF;
  IF NOT type_is_or_descends_from(v_type_id, 'system_actor') THEN
    RAISE EXCEPTION 'system_actors: entity % fundamental type is not system_actor', NEW.entity_id;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER system_actors_type_check
  BEFORE INSERT ON system_actors
  FOR EACH ROW EXECUTE FUNCTION system_actors_check_type();

-- ---------------------------------------------------------------------------
-- (c) Seed the one row: slug 'anonymous'
-- ---------------------------------------------------------------------------
-- Idempotent-safe: bails out entirely if the 'anonymous' system_actors row
-- already exists, so the entities insert and the system_actors insert are
-- guarded consistently -- no orphan entities row can be produced by a re-run.
-- Does not hardcode entities.id (BIGSERIAL, not deterministic across
-- environments); everything downstream resolves the actor by slug via the
-- GetSystemActorBySlug query.
-- +goose StatementBegin
DO $$
DECLARE
  v_type_id   BIGINT;
  v_entity_id BIGINT;
BEGIN
  IF EXISTS (SELECT 1 FROM system_actors WHERE slug = 'anonymous') THEN
    RETURN;
  END IF;

  SELECT id INTO v_type_id FROM types WHERE slug = 'system_actor';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'seed for the anonymous system actor requires type slug ''system_actor'' to exist';
  END IF;

  -- owner_id is left unset: entities_owner_default_self only defaults
  -- owner_id for natural_person/service_account descendants, so it never
  -- fires for system_actor -- this row's owner_id stays NULL.
  INSERT INTO entities (fundamental_type_id) VALUES (v_type_id) RETURNING id INTO v_entity_id;
  INSERT INTO system_actors (entity_id, slug) VALUES (v_entity_id, 'anonymous');
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- (d) entities_no_system_actor_owner ownership guard
-- ---------------------------------------------------------------------------
-- Deliberately BEFORE INSERT OR UPDATE (not BEFORE UPDATE OF owner_id), so it
-- fires on every entities UPDATE, not only ones that touch owner_id. The
-- cost is a primary-key lookup against a one-row table; that is the
-- proposal's accepted trade.
--
-- Postgres fires same-event row-level triggers in alphabetical trigger-name
-- order -- a convention mod-core's 0013_entity_ownership.sql already relies
-- on and documents. Two firing-order facts are load-bearing here:
--   * On INSERT, 'entities_no_system_actor_owner' sorts BEFORE
--     'entities_owner_self_default' ('...no...' < '...ow...'). That is
--     safe: the self-default only ever assigns NEW.id for types descending
--     from natural_person/service_account, neither of which a system_actor
--     is, so no defaulted value can evade this guard.
--   * On UPDATE, 'entities_no_system_actor_owner' sorts BEFORE
--     'entities_owner_immutable'. An UPDATE ... SET owner_id = <anon entity
--     id> therefore raises this trigger's "a system actor may not own an
--     entity" message, not the immutability message.
-- +goose StatementBegin
CREATE FUNCTION entities_check_no_system_actor_owner() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.owner_id IS NOT NULL
     AND EXISTS (SELECT 1 FROM system_actors WHERE entity_id = NEW.owner_id)
  THEN
    RAISE EXCEPTION 'entities: a system actor may not own an entity (owner_id=%)', NEW.owner_id;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER entities_no_system_actor_owner
  BEFORE INSERT OR UPDATE ON entities
  FOR EACH ROW EXECUTE FUNCTION entities_check_no_system_actor_owner();

-- ===========================================================================
-- auth_jwt_secrets
-- ===========================================================================

-- auth_jwt_secrets is a private, single-row table holding the HS256
-- JWT signing secret (see api/internal/auth.IssueLocalJWT et al.) when
-- JWT_SECRET is not supplied via the environment (see
-- api/internal/config.Load's JWT-secret bootstrap path,
-- bootstrapJWTSecretFromDB / fetchOrGeneratePersistedJWTSecret). The
-- id = 1 CHECK plus PRIMARY KEY caps this table at one row ever:
-- concurrent first-boot callers race on that constraint (see
-- InsertJWTSecretIfAbsent's ON CONFLICT (id) DO NOTHING below), so
-- every caller converges on the same secret rather than each
-- independently generating one and picking arbitrarily. The
-- char_length CHECK is a defense-in-depth guard against a corrupt or
-- truncated secret ever being persisted through this code path;
-- the Go bootstrap path independently validates length on every read
-- and fails loudly rather than regenerating if a persisted secret is
-- too short.
--
-- CREATE TABLE IF NOT EXISTS, deliberately, unlike a normal first-ever
-- migration: config.Load() runs before this module's own migrations
-- apply in every generated composition root (unlike mod-core's
-- analogous field_crypto_keys, whose cipher-init call runs after both
-- the pool and migrations), so Load()'s own bootstrap path may have
-- already created this table, idempotently, using this exact DDL,
-- before goose ever gets here. See plan/overview.md's "Key research
-- findings" for the full reasoning.
--
-- Deliberately not modeled as an entity (no FK to entities.id): this is
-- bootstrap/operational data, the same reasoning that keeps
-- goose_db_version_users a bare table.
CREATE TABLE IF NOT EXISTS auth_jwt_secrets (
  id         SMALLINT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  secret     TEXT NOT NULL CHECK (char_length(secret) >= 32),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- ===========================================================================
-- mod_users.ssh_public_keys
-- ===========================================================================

-- ---------------------------------------------------------------------------
-- mod_users.ssh_public_keys
-- ---------------------------------------------------------------------------
-- This is the first table in the ecosystem placed under a mod_* schema.
-- Every table above this line predates the schema ownership standard
-- (docs-mf-standards/architecture/schema-ownership-design.md,
-- docs-mf-standards/building-modules.md#data-ownership-and-schema-conventions)
-- and is deliberately UNQUALIFIED: mod-users is a foundation module, so those
-- tables (and goose_db_version_users) land in whatever schema is first on the
-- migrating connection's search_path (the default schema normally; the module
-- schema in a displaced layout). This table is new, so the standard applies:
-- it lives in mod_users (derived from `module: users`) and is
-- schema-qualified. Its foreign key to user_accounts is left UNQUALIFIED on
-- purpose, like every other reference to a foundation table, so it binds to
-- wherever user_accounts actually landed on the same search_path rather than
-- hardcoding one schema.
--
-- ssh_public_keys_active_fingerprint_uq is the global-uniqueness invariant:
-- a fingerprint must resolve to at most one active key, or the same key
-- could silently authenticate as two different users. It is enforced here,
-- in the schema, rather than with a Go pre-check, because a module is not
-- the sole writer of its own tables (building-modules.md#grants) and a Go
-- check alone would lose a concurrent-registration race. The same partial
-- index doubles as the resolver's lookup index for
-- ResolveActiveSSHPublicKey, since it is scoped to exactly the rows the
-- resolver reads (fingerprint_sha256 among active keys).
--
-- Revocation archives the row (archived_at) rather than deleting it: a key
-- row is a module-owned domain row another composing app may reference (for
-- example in push attribution or an audit view), so it follows
-- schema-ownership-design.md#deletion-module-owned-rows-are-archived-never-dropped
-- rather than mod-users' older hard-deleting credential tables, which
-- predate the standard. Every read and the resolver filter
-- archived_at IS NULL.
CREATE SCHEMA IF NOT EXISTS mod_users;

CREATE TABLE IF NOT EXISTS mod_users.ssh_public_keys (
  id                 BIGSERIAL PRIMARY KEY,
  uuid               UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  user_account_id    BIGINT NOT NULL REFERENCES user_accounts(id) ON DELETE CASCADE,
  key_type           TEXT NOT NULL CHECK (key_type IN (
                       'ssh-ed25519', 'sk-ssh-ed25519@openssh.com',
                       'ecdsa-sha2-nistp256', 'ecdsa-sha2-nistp384', 'ecdsa-sha2-nistp521',
                       'sk-ecdsa-sha2-nistp256@openssh.com', 'ssh-rsa')),
  public_key         TEXT NOT NULL,
  fingerprint_sha256 TEXT NOT NULL CHECK (fingerprint_sha256 LIKE 'SHA256:%'),
  label              TEXT NOT NULL DEFAULT '' CHECK (char_length(label) <= 100),
  created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at        TIMESTAMPTZ
);

CREATE UNIQUE INDEX IF NOT EXISTS ssh_public_keys_active_fingerprint_uq
  ON mod_users.ssh_public_keys (fingerprint_sha256)
  WHERE archived_at IS NULL;

CREATE INDEX IF NOT EXISTS ssh_public_keys_user_account_active_idx
  ON mod_users.ssh_public_keys (user_account_id)
  WHERE archived_at IS NULL;

-- +goose Down

-- Reverse order of the Up sections. user_accounts and the other base tables
-- (the former 0100) are intentionally not dropped -- base schema, no rollback.

-- mod_users.ssh_public_keys, then its schema if nothing else lives there.
DROP TABLE IF EXISTS mod_users.ssh_public_keys;
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
    WHERE n.nspname = 'mod_users'
  ) THEN
    DROP SCHEMA IF EXISTS mod_users;
  END IF;
END $$;
-- +goose StatementEnd

DROP TABLE IF EXISTS auth_jwt_secrets;

-- Reverse order: drop the entities_no_system_actor_owner trigger, then its
-- function, then the system_actors_type_check trigger and its function,
-- then delete the seeded system_actors and entities rows, then drop the
-- system_actors table. The seeded 'system_actor' type row is a documented
-- exception -- see the note at the end of this block.
DROP TRIGGER IF EXISTS entities_no_system_actor_owner ON entities;
DROP FUNCTION IF EXISTS entities_check_no_system_actor_owner();

DROP TRIGGER IF EXISTS system_actors_type_check ON system_actors;
DROP FUNCTION IF EXISTS system_actors_check_type();

-- Delete the seeded system_actors row first -- its entity_id FK is
-- ON DELETE RESTRICT, so the entities row cannot be deleted while a
-- system_actors row still references it -- then delete the entities row it
-- pointed at, in one statement via a writable CTE so the entity id is still
-- available to the second DELETE after the first has removed its row.
WITH removed AS (
  DELETE FROM system_actors WHERE slug = 'anonymous' RETURNING entity_id
)
DELETE FROM entities WHERE id IN (SELECT entity_id FROM removed);

DROP TABLE IF EXISTS system_actors;

-- Note: types rows are append-only (the types_reject_mutation trigger,
-- defined in mod-core's 0002_types.sql, unconditionally rejects every
-- DELETE FROM types). The 'system_actor' type row therefore cannot be
-- deleted and remains in place after a DOWN migration. This mirrors the
-- same, already-established limitation documented in mod-authz's
-- 0501_authz_actor_groups.sql ("types rows are append-only ... they cannot
-- be deleted ... a deprecated type causes no harm").
