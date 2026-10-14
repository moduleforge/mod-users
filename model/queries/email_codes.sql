-- name: CreateEmailCode :one
INSERT INTO email_codes (user_account_id, code_hash, purpose, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id, user_account_id, code_hash, purpose, expires_at, consumed_at, created_at, failed_attempts;

-- name: GetActiveEmailCode :one
SELECT id, user_account_id, code_hash, purpose, expires_at, consumed_at, created_at, failed_attempts
FROM email_codes
WHERE user_account_id = $1
  AND purpose = $2
  AND consumed_at IS NULL
  AND expires_at > now()
ORDER BY created_at DESC
LIMIT 1;

-- name: ConsumeEmailCode :execrows
UPDATE email_codes
SET consumed_at = now()
WHERE id = $1 AND consumed_at IS NULL;

-- name: ClaimEmailCodeAttempt :one
-- Atomically claims one verification attempt (call before comparing the code).
-- $2 is the max attempts. The code is consumed when the bound is reached.
-- No row is returned when the code is already consumed or exhausted.
UPDATE email_codes
SET failed_attempts = failed_attempts + 1,
    consumed_at = CASE WHEN failed_attempts + 1 >= sqlc.arg(max_attempts)::int THEN now() ELSE consumed_at END
WHERE id = $1
  AND consumed_at IS NULL
  AND failed_attempts < sqlc.arg(max_attempts)::int
RETURNING id, user_account_id, code_hash, purpose, expires_at, consumed_at, created_at, failed_attempts;

-- name: CreateEmailCodeWithCooldown :one
-- Atomically mints a code unless a code of ANY state (unconsumed, exhausted,
-- or consumed by success) was created for the same (user, purpose) within the
-- last cooldown_secs seconds. Concurrent callers are serialized by an advisory
-- lock inside the create_email_code_with_cooldown function, so at most one
-- code is created per window. No row is returned (pgx.ErrNoRows) when the
-- cooldown applies; callers must then send no email.
SELECT id, user_account_id, code_hash, purpose, expires_at, consumed_at, created_at, failed_attempts
FROM create_email_code_with_cooldown(
  sqlc.arg(user_account_id)::bigint,
  sqlc.arg(code_hash)::text,
  sqlc.arg(purpose)::text,
  sqlc.arg(expires_at)::timestamptz,
  sqlc.arg(cooldown_secs)::int
);
