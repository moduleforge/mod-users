-- +goose Up

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

-- +goose Down

DROP TABLE IF EXISTS auth_jwt_secrets;
