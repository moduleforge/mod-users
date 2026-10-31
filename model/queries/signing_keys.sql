-- Queries over mod_users.auth_signing_keys, the single persisted Ed25519 key
-- (PKCS#8 DER) used when JWT_SIGNING_KEY is unset.

-- name: GetSigningKey :one
SELECT * FROM mod_users.auth_signing_keys WHERE id = 1;

-- name: InsertSigningKeyIfAbsent :execrows
-- Concurrent first-boot callers race on the id = 1 primary key and all
-- converge on the winner's key: read it back with GetSigningKey. Zero rows
-- affected means another caller won.
INSERT INTO mod_users.auth_signing_keys (id, private_key)
VALUES (1, $1)
ON CONFLICT (id) DO NOTHING;
