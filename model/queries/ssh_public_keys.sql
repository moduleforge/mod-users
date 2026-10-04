-- name: InsertSSHPublicKey :one
INSERT INTO mod_users.ssh_public_keys
  (user_account_id, key_type, public_key, fingerprint_sha256, label)
VALUES
  ($1, $2, $3, $4, $5)
RETURNING id, uuid, user_account_id, key_type, public_key, fingerprint_sha256,
          label, created_at, archived_at;

-- name: ListActiveSSHPublicKeysByUserAccount :many
SELECT id, uuid, user_account_id, key_type, public_key, fingerprint_sha256,
       label, created_at, archived_at
FROM mod_users.ssh_public_keys
WHERE user_account_id = $1 AND archived_at IS NULL
ORDER BY created_at ASC, id ASC
LIMIT $2 OFFSET $3;

-- name: CountActiveSSHPublicKeysByUserAccount :one
SELECT count(*)
FROM mod_users.ssh_public_keys
WHERE user_account_id = $1 AND archived_at IS NULL;

-- name: GetActiveSSHPublicKeyByUUIDForUserAccount :one
SELECT id, uuid, user_account_id, key_type, public_key, fingerprint_sha256,
       label, created_at, archived_at
FROM mod_users.ssh_public_keys
WHERE uuid = $1 AND user_account_id = $2 AND archived_at IS NULL;

-- name: ArchiveSSHPublicKey :execrows
UPDATE mod_users.ssh_public_keys
SET archived_at = now()
WHERE uuid = $1 AND user_account_id = $2 AND archived_at IS NULL;

-- name: ResolveActiveSSHPublicKey :one
SELECT ua.account_holder
FROM mod_users.ssh_public_keys k
JOIN user_accounts ua ON ua.id = k.user_account_id
WHERE k.fingerprint_sha256 = $1 AND k.public_key = $2 AND k.archived_at IS NULL;
