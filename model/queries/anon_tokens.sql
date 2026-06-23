-- name: CreateAnonToken :one
INSERT INTO anon_tokens (device_id, session_token, user_account_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id, uuid, device_id, session_token, user_account_id, created_at, expires_at;

-- name: GetAnonTokenBySessionToken :one
SELECT id, uuid, device_id, session_token, user_account_id, created_at, expires_at
FROM anon_tokens
WHERE session_token = $1
  AND expires_at > now();

-- name: GetAnonTokensByDeviceID :many
SELECT id, uuid, device_id, session_token, user_account_id, created_at, expires_at
FROM anon_tokens
WHERE device_id = $1
  AND expires_at > now()
ORDER BY created_at DESC;

-- name: DeleteAnonTokensByUserAccountID :exec
DELETE FROM anon_tokens
WHERE user_account_id = $1;
