-- Queries over mod_users.user_identifiers. value_canon is canonicalized by the
-- caller (an email: trimmed and lowercased); the table checks it.

-- name: CreateIdentifier :one
INSERT INTO mod_users.user_identifiers (
    user_id, kind, value_canon, is_primary, verified_at
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetIdentifierByUUID :one
SELECT * FROM mod_users.user_identifiers WHERE uuid = $1;

-- name: GetIdentifierByID :one
SELECT * FROM mod_users.user_identifiers WHERE id = $1;

-- name: LockIdentifierForUpdate :one
-- Row-locks the identifier so a concurrent password create or reset that binds
-- it (whose foreign-key check takes a shared lock on this row) serializes
-- behind a removal in progress.
SELECT * FROM mod_users.user_identifiers WHERE id = $1 AND user_id = $2 FOR UPDATE;

-- name: GetIdentifierByKindValue :one
SELECT * FROM mod_users.user_identifiers WHERE kind = $1 AND value_canon = $2;

-- name: ListIdentifiersByUser :many
SELECT * FROM mod_users.user_identifiers
WHERE user_id = $1
ORDER BY is_primary DESC, id;

-- name: ListIdentifiersByUsers :many
-- Batch form for directory lookups.
SELECT * FROM mod_users.user_identifiers
WHERE user_id = ANY(@user_ids::bigint[])
ORDER BY user_id, is_primary DESC, id;

-- name: GetPrimaryEmailByUser :one
SELECT * FROM mod_users.user_identifiers
WHERE user_id = $1 AND kind = 'email' AND is_primary;

-- name: MarkIdentifierVerified :one
-- Idempotent: keeps the first verification time.
UPDATE mod_users.user_identifiers
SET verified_at = COALESCE(verified_at, now())
WHERE id = $1
RETURNING *;

-- name: ClearPrimaryEmail :exec
-- First half of a primary switch; run SetIdentifierPrimary in the same
-- transaction (user_identifiers_one_primary_email_uq allows one at a time).
UPDATE mod_users.user_identifiers
SET is_primary = false
WHERE user_id = $1 AND kind = 'email' AND is_primary;

-- name: SetIdentifierPrimary :one
UPDATE mod_users.user_identifiers
SET is_primary = true
WHERE id = $1 AND user_id = $2 AND kind = 'email'
RETURNING *;

-- name: DeleteIdentifier :execrows
-- Fails with a foreign-key violation while a credential or challenge still
-- binds the identifier; the caller revokes those first.
DELETE FROM mod_users.user_identifiers WHERE id = $1 AND user_id = $2;

-- name: UserHasVerifiedEmail :one
SELECT EXISTS (
    SELECT 1 FROM mod_users.user_identifiers
    WHERE user_id = $1 AND kind = 'email' AND verified_at IS NOT NULL
) AS has_verified_email;
