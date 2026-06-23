-- +goose Up

-- Allow anonymous user accounts (accounts without an email address).
ALTER TABLE user_accounts ALTER COLUMN email DROP NOT NULL;

-- anon_tokens: short-lived session tokens for anonymous (non-authenticated)
-- users. session_token stores the SHA-256 hex hash of the opaque bearer token
-- (consistent with the password_resets pattern in 0104_password_resets.sql).
-- The user_account_id FK cascades deletes so tokens are cleaned up automatically
-- if the parent user_account row is hard-deleted.
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

-- +goose Down

-- Tokens must be removed before rolling back; the NOT NULL restoration below
-- will fail if any NULL-email rows remain. Down migration is for development
-- rollbacks only — operator must delete anonymous rows first.
DROP TABLE IF EXISTS anon_tokens;

ALTER TABLE user_accounts ALTER COLUMN email SET NOT NULL;
