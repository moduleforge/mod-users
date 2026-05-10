-- name: CreateUserAccount :one
INSERT INTO user_accounts (account_holder, email, email_verified_at)
VALUES ($1, $2, $3)
RETURNING id, uuid, account_holder, email, email_verified_at, default_app_id,
          created_at, updated_at;

-- name: GetUserAccountByID :one
SELECT id, uuid, account_holder, email, email_verified_at, default_app_id,
       created_at, updated_at
FROM user_accounts
WHERE id = $1;

-- name: GetUserAccountByUUID :one
SELECT id, uuid, account_holder, email, email_verified_at, default_app_id,
       created_at, updated_at
FROM user_accounts
WHERE uuid = $1;

-- name: GetUserAccountByEmail :one
SELECT id, uuid, account_holder, email, email_verified_at, default_app_id,
       created_at, updated_at
FROM user_accounts
WHERE lower(email) = lower($1);

-- name: UpdateUserAccount :exec
UPDATE user_accounts
SET email = $2,
    email_verified_at = $3
WHERE id = $1;

-- name: SetDefaultApp :exec
UPDATE user_accounts
SET default_app_id = $2
WHERE id = $1;

-- name: GetUserAccountByAccountHolder :one
SELECT id, uuid, account_holder, email, email_verified_at, default_app_id,
       created_at, updated_at
FROM user_accounts
WHERE account_holder = $1;

-- name: SearchUserAccounts :many
SELECT id, uuid, account_holder, email, email_verified_at, default_app_id,
       created_at, updated_at
FROM user_accounts
WHERE ($1::text IS NULL OR lower(email) LIKE '%' || lower($1::text) || '%')
ORDER BY created_at DESC
LIMIT $2 OFFSET $3;
