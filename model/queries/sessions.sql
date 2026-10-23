-- Queries over mod_users.sessions, session_factors and session_refresh_tokens,
-- plus thin wrappers over the mod_users availability and liveness functions.

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------

-- name: CreateSession :one
-- impersonator_session_id, impersonated_profile_id and impersonation_reason are
-- all NULL or all set (sessions_impersonation_complete); MF101 on nesting or an
-- unheld profile.
INSERT INTO mod_users.sessions (
    principal_id, app_id, acr, amr, auth_time, expires_at,
    impersonator_session_id, impersonated_profile_id, impersonation_reason
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9
)
RETURNING *;

-- name: GetSessionByUUID :one
SELECT * FROM mod_users.sessions WHERE uuid = $1;

-- name: GetSessionByID :one
SELECT * FROM mod_users.sessions WHERE id = $1;

-- name: ListLiveSessionsByPrincipal :many
SELECT * FROM mod_users.sessions
WHERE principal_id = $1
  AND revoked_at IS NULL
  AND expires_at > now()
ORDER BY last_seen_at DESC, id DESC;

-- name: ListImpersonationChildren :many
SELECT * FROM mod_users.sessions
WHERE impersonator_session_id = $1 AND revoked_at IS NULL;

-- name: TouchSession :exec
UPDATE mod_users.sessions SET last_seen_at = now() WHERE id = $1;

-- name: UpdateSessionAssurance :one
-- After a step-up: the recomputed acr/amr and a fresh auth_time.
UPDATE mod_users.sessions
SET acr = $2,
    amr = $3,
    auth_time = $4
WHERE id = $1
RETURNING *;

-- name: SetSessionExpiry :one
-- Impersonation renew.
UPDATE mod_users.sessions SET expires_at = $2 WHERE id = $1 RETURNING *;

-- name: RevokeSession :one
-- Only an unrevoked session is revoked (no row otherwise).
UPDATE mod_users.sessions
SET revoked_at = now(),
    revoke_reason = $2
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeSessionsByPrincipal :many
-- Disable and "log out everywhere"; returns the revoked sessions.
UPDATE mod_users.sessions
SET revoked_at = now(),
    revoke_reason = $2
WHERE principal_id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeImpersonationChildren :many
-- Ends the impersonation sessions started from a parent session.
UPDATE mod_users.sessions
SET revoked_at = now(),
    revoke_reason = $2
WHERE impersonator_session_id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeSessionsImpersonatingProfile :many
-- A profile that ended: sessions impersonating it end with 'profile_ended'.
UPDATE mod_users.sessions
SET revoked_at = now(),
    revoke_reason = 'profile_ended'
WHERE impersonated_profile_id = $1 AND revoked_at IS NULL
RETURNING *;

-- ---------------------------------------------------------------------------
-- Session factors
-- ---------------------------------------------------------------------------

-- name: UpsertSessionFactor :one
-- Add a factor, or re-verify it (step-up). MF101 when the credential belongs to
-- another principal.
INSERT INTO mod_users.session_factors (session_id, credential_id, asserted_acr)
VALUES ($1, $2, $3)
ON CONFLICT (session_id, credential_id) DO UPDATE
SET verified_at = now(),
    asserted_acr = EXCLUDED.asserted_acr
RETURNING *;

-- name: ListSessionFactors :many
-- The factors with their credential and kind, for acr/amr derivation and the
-- session view.
SELECT sqlc.embed(f), c.kind, c.uuid AS credential_uuid, k.amr, k.factor, k.base_acr
FROM mod_users.session_factors f
JOIN mod_users.credentials c ON c.id = f.credential_id
JOIN mod_users.credential_kinds k ON k.kind = c.kind
WHERE f.session_id = $1
ORDER BY f.verified_at, f.credential_id;

-- name: ListSessionAMR :many
-- Distinct amr values of the session's unrevoked factors.
SELECT DISTINCT k.amr
FROM mod_users.session_factors f
JOIN mod_users.credentials c ON c.id = f.credential_id
JOIN mod_users.credential_kinds k ON k.kind = c.kind
WHERE f.session_id = $1 AND c.revoked_at IS NULL
ORDER BY k.amr;

-- ---------------------------------------------------------------------------
-- Refresh lineages
-- ---------------------------------------------------------------------------

-- name: CreateRefreshToken :one
-- A new token; lineage_uuid is supplied by the caller (a fresh uuid for a new
-- lineage or fork, the existing one for rotation).
INSERT INTO mod_users.session_refresh_tokens (session_id, lineage_uuid, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetRefreshTokenByHash :one
-- Reuse lookup: a row with used_at set presented again is reuse.
SELECT * FROM mod_users.session_refresh_tokens WHERE token_hash = $1;

-- name: UseRefreshToken :one
-- Atomic rotation step: marks a live, unused token used. No row means the
-- token is already used (reuse), expired, or unknown; the caller then looks it
-- up with GetRefreshTokenByHash to tell the cases apart.
UPDATE mod_users.session_refresh_tokens
SET used_at = now()
WHERE token_hash = $1
  AND used_at IS NULL
  AND expires_at > now()
RETURNING *;

-- name: SetRefreshTokenReplacedBy :exec
UPDATE mod_users.session_refresh_tokens
SET replaced_by_id = $2
WHERE id = $1 AND used_at IS NOT NULL;

-- name: ListRefreshTokensByLineage :many
SELECT * FROM mod_users.session_refresh_tokens
WHERE lineage_uuid = $1
ORDER BY id;

-- name: ExpireRefreshTokensBySession :execrows
-- Session revocation: the session's live tokens stop rotating.
UPDATE mod_users.session_refresh_tokens
SET expires_at = now()
WHERE session_id = $1 AND used_at IS NULL AND expires_at > now();

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM mod_users.session_refresh_tokens WHERE expires_at < $1;

-- ---------------------------------------------------------------------------
-- Availability, liveness and acr wrappers
-- ---------------------------------------------------------------------------

-- name: ProfileAvailability :one
-- 'available' or a lock reason (not_held, inactive, principal_disabled,
-- confined, credential_not_authorized, provider_required, acr_too_low).
SELECT mod_users.profile_availability(@session_id::bigint, @profile_id::bigint)::text AS availability;

-- name: CredentialProfileAvailability :one
SELECT mod_users.credential_profile_availability(@credential_id::bigint, @profile_id::bigint)::text AS availability;

-- name: SessionLiveness :one
-- The per-request check for an interactive token; always one row.
SELECT session_id, principal_id, profile_id, party_id, impersonator_principal_id,
       app_id, acr, amr::text[] AS amr, auth_time, status
FROM mod_users.session_liveness(
    @session_uuid::uuid, @principal_uuid::uuid, @profile_uuid::uuid
);

-- name: CredentialLiveness :one
-- The per-request check for a client-credentials token; always one row.
SELECT * FROM mod_users.credential_liveness(
    @credential_uuid::uuid, @principal_uuid::uuid, @profile_uuid::uuid
);

-- name: SessionACR :one
-- NULL when the session has no unrevoked factor (an impersonation session
-- carries its parent's acr).
SELECT mod_users.session_acr(@session_id::bigint)::smallint AS acr;
