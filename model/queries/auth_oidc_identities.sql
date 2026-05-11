-- name: GetOIDCIdentityByIssuerSubject :one
SELECT id, uuid, user_account_id, issuer, subject, email, email_verified_at_idp, linked_at, last_seen_at
FROM auth_oidc_identities
WHERE issuer = $1 AND subject = $2;

-- name: ListOIDCIdentitiesByUserAccount :many
SELECT id, uuid, user_account_id, issuer, subject, email, email_verified_at_idp, linked_at, last_seen_at
FROM auth_oidc_identities
WHERE user_account_id = $1
ORDER BY linked_at ASC;

-- name: InsertOIDCIdentity :one
INSERT INTO auth_oidc_identities
  (user_account_id, issuer, subject, email, email_verified_at_idp)
VALUES
  ($1, $2, $3, $4, $5)
RETURNING id, uuid, user_account_id, issuer, subject, email, email_verified_at_idp, linked_at, last_seen_at;

-- name: TouchOIDCIdentityLastSeen :exec
UPDATE auth_oidc_identities
SET last_seen_at = now()
WHERE id = $1;

-- name: DeleteOIDCIdentityByUUID :execrows
DELETE FROM auth_oidc_identities
WHERE uuid = $1 AND user_account_id = $2;

-- name: CountOIDCIdentitiesByUserAccount :one
SELECT count(*) FROM auth_oidc_identities WHERE user_account_id = $1;
