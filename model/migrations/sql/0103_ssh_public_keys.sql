-- +goose Up

-- ---------------------------------------------------------------------------
-- mod_users.ssh_public_keys
-- ---------------------------------------------------------------------------
-- This is the first table in the ecosystem placed under a mod_* schema
-- rather than public. Every table above this line predates the schema
-- ownership standard (docs-mf-standards/architecture/schema-ownership-design.md,
-- docs-mf-standards/building-modules.md#data-ownership-and-schema-conventions)
-- and stays in public; moving them is out of scope. This table is new, so
-- the standard applies: a module's tables live in mod_<module> — here
-- mod_users, derived from `module: users` — with every reference
-- schema-qualified (mod_users.ssh_public_keys, public.user_accounts) rather
-- than relying on search_path.
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
  user_account_id    BIGINT NOT NULL REFERENCES public.user_accounts(id) ON DELETE CASCADE,
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
-- intentionally omitted — forward-only per building-modules.md
