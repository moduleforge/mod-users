-- +goose Up
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
