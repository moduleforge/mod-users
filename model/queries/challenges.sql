-- Queries over mod_users.auth_challenges: one-time codes and tokens. Only the
-- hash of a code is stored. Challenges are not credentials.

-- name: CreateChallengeWithCooldown :one
-- Mint at most one challenge per subject (kind, principal, identifier,
-- session) per cooldown window. No row (sql.ErrNoRows / pgx.ErrNoRows) means a
-- challenge was created within p_cooldown_secs: the caller answers "too soon".
SELECT * FROM mod_users.create_challenge_with_cooldown(
    @kind::text,
    sqlc.narg(principal_id)::bigint,
    sqlc.narg(identifier_id)::bigint,
    sqlc.narg(session_id)::bigint,
    @code_hash::text,
    @expires_at::timestamptz,
    @cooldown_secs::int
);

-- name: CreateChallenge :one
-- Direct insert for challenges without a cooldown (login_handoff, step_up
-- bound to a credential).
INSERT INTO mod_users.auth_challenges (
    kind, principal_id, identifier_id, credential_id, session_id, code_hash, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetChallengeByUUID :one
SELECT * FROM mod_users.auth_challenges WHERE uuid = $1;

-- name: GetLatestOpenChallenge :one
-- The newest unconsumed, unexpired challenge of a subject, for code
-- verification. Subject columns match with NULLs equal.
SELECT * FROM mod_users.auth_challenges
WHERE kind = @kind
  AND principal_id IS NOT DISTINCT FROM sqlc.narg(principal_id)::bigint
  AND identifier_id IS NOT DISTINCT FROM sqlc.narg(identifier_id)::bigint
  AND session_id IS NOT DISTINCT FROM sqlc.narg(session_id)::bigint
  AND consumed_at IS NULL
  AND expires_at > now()
ORDER BY created_at DESC, id DESC
LIMIT 1;

-- name: IncrementChallengeFailedAttempts :one
-- The attempt counter; the caller consumes the challenge once the budget is spent.
UPDATE mod_users.auth_challenges
SET failed_attempts = failed_attempts + 1
WHERE id = $1
RETURNING failed_attempts;

-- name: ConsumeChallenge :one
-- Single-use: only an unconsumed, unexpired challenge is consumed (no row
-- otherwise), so a replay or a race loses.
UPDATE mod_users.auth_challenges
SET consumed_at = now()
WHERE id = $1
  AND consumed_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: ConsumeChallengeByCodeHash :one
-- Exchanges that look a challenge up by its code alone (login_handoff,
-- password_reset): atomic find-and-consume.
UPDATE mod_users.auth_challenges
SET consumed_at = now()
WHERE id = (
    SELECT ac.id FROM mod_users.auth_challenges ac
    WHERE ac.kind = $1
      AND ac.code_hash = $2
      AND ac.consumed_at IS NULL
      AND ac.expires_at > now()
    ORDER BY ac.created_at DESC
    LIMIT 1
    FOR UPDATE
)
RETURNING *;

-- name: ConsumeOpenChallengesBySubject :execrows
-- Supersede earlier open challenges of a subject (for example the password
-- reset challenges of a principal once one is used).
UPDATE mod_users.auth_challenges
SET consumed_at = now()
WHERE kind = @kind
  AND principal_id IS NOT DISTINCT FROM sqlc.narg(principal_id)::bigint
  AND identifier_id IS NOT DISTINCT FROM sqlc.narg(identifier_id)::bigint
  AND consumed_at IS NULL;

-- name: DeleteExpiredChallenges :execrows
-- Housekeeping: drops challenges expired before the cutoff.
DELETE FROM mod_users.auth_challenges WHERE expires_at < $1;
