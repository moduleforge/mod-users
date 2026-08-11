-- name: GetJWTSecret :one
SELECT secret FROM auth_jwt_secrets WHERE id = 1;

-- name: InsertJWTSecretIfAbsent :one
INSERT INTO auth_jwt_secrets (id, secret)
VALUES (1, $1)
ON CONFLICT (id) DO NOTHING
RETURNING secret;
