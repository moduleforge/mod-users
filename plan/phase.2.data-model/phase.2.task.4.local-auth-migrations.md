# Phase 2, Task 4 — Local auth migrations

## Context
Local auth (password + email-code) lives in tables separate from `users` so that OIDC-only users carry no empty credential rows.

## Acceptance

`0007_auth_local.sql`:
```sql
CREATE TABLE auth_local (
  user_id              BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  password_hash        TEXT NOT NULL,           -- argon2id encoded string
  password_updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  created_at           TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

`0008_email_codes.sql`:
```sql
CREATE TABLE email_codes (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash   TEXT NOT NULL,                    -- bcrypt or sha256(salt+code)
  purpose     TEXT NOT NULL CHECK (purpose IN ('login','verify_email')),
  expires_at  TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX email_codes_user_purpose_idx ON email_codes(user_id, purpose) WHERE consumed_at IS NULL;
```

`0009_password_resets.sql`:
```sql
CREATE TABLE password_resets (
  id          BIGSERIAL PRIMARY KEY,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash  TEXT NOT NULL UNIQUE,             -- sha256 of opaque token
  expires_at  TIMESTAMPTZ NOT NULL,
  consumed_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

## How to verify
- Migrations apply cleanly.
- Inserting an `auth_local` row for a non-existent `user_id` fails (FK).
- Inserting an `email_codes` row with `purpose='reset'` fails (CHECK).

## Notes
- TTLs are enforced in app code (5 min for login codes; 30 min suggested for password resets — confirm in Phase 4).
- Cleanup of expired rows is a future cron concern; not part of v1.
