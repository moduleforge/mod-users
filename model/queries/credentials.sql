-- Queries over mod_users.credentials and the per-kind cred_* subtables.
-- Credentials are revoked, never deleted. The `active` (and cred_password's
-- principal_id) mirror columns are maintained by trigger and never written here.

-- ---------------------------------------------------------------------------
-- Common credential row
-- ---------------------------------------------------------------------------

-- name: CreateCredential :one
-- Insert the common row; insert the matching cred_<kind> row in the same
-- transaction. Invariants (kind allowed for the principal, default profile
-- held) raise MF102.
INSERT INTO mod_users.credentials (
    principal_id, kind, label, confined_to_party_id, default_profile_id,
    created_by_profile_id, expires_at
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetCredentialByUUID :one
SELECT * FROM mod_users.credentials WHERE uuid = $1;

-- name: GetCredentialByID :one
SELECT * FROM mod_users.credentials WHERE id = $1;

-- name: ListCredentialsByPrincipal :many
-- Every credential of the principal, revoked ones included, newest first.
SELECT * FROM mod_users.credentials
WHERE principal_id = $1
ORDER BY created_at DESC, id DESC;

-- name: ListActiveCredentialsByPrincipal :many
SELECT * FROM mod_users.credentials
WHERE principal_id = $1
  AND revoked_at IS NULL
  AND (expires_at IS NULL OR expires_at > now())
ORDER BY created_at DESC, id DESC;

-- name: ListCredentialKinds :many
SELECT * FROM mod_users.credential_kinds ORDER BY kind;

-- name: GetCredentialKind :one
SELECT * FROM mod_users.credential_kinds WHERE kind = $1;

-- name: CountActiveInteractiveCredentials :one
-- For the "last interactive credential" guard (409 users.last_credential).
SELECT count(*) AS active_interactive
FROM mod_users.credentials c
JOIN mod_users.credential_kinds k ON k.kind = c.kind
WHERE c.principal_id = $1
  AND k.class = 'interactive'
  AND c.revoked_at IS NULL
  AND (c.expires_at IS NULL OR c.expires_at > now());

-- name: UpdateCredentialLabelAndDefault :one
-- A NULL default_profile_id clears the landing profile.
UPDATE mod_users.credentials
SET label = @label,
    default_profile_id = @default_profile_id
WHERE id = @id
RETURNING *;

-- name: SetCredentialConfinement :one
-- A NULL party clears the confinement.
UPDATE mod_users.credentials
SET confined_to_party_id = $2
WHERE id = $1
RETURNING *;

-- name: TouchCredentialLastUsed :exec
UPDATE mod_users.credentials SET last_used_at = now() WHERE id = $1;

-- name: RevokeCredential :one
-- Idempotent guard: only an unrevoked credential is revoked (no row otherwise).
UPDATE mod_users.credentials
SET revoked_at = now(),
    revoke_reason = $2
WHERE id = $1 AND revoked_at IS NULL
RETURNING *;

-- name: RevokeSessionsHoldingCredential :many
-- Revoke every unrevoked session holding the credential as a factor
-- (revoke_reason 'credential_revoked'). Impersonation children end through
-- session_liveness (parent_session_ended). Returns the revoked sessions.
UPDATE mod_users.sessions
SET revoked_at = now(),
    revoke_reason = 'credential_revoked'
WHERE revoked_at IS NULL
  AND id IN (
      SELECT f.session_id
      FROM mod_users.session_factors f
      WHERE f.credential_id = $1
  )
RETURNING *;

-- ---------------------------------------------------------------------------
-- password
-- ---------------------------------------------------------------------------

-- name: CreatePasswordCredential :one
-- identifier_id NULL: an unbound password (at most one active per principal).
INSERT INTO mod_users.cred_password (credential_id, identifier_id, hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetPasswordByCredentialID :one
SELECT * FROM mod_users.cred_password WHERE credential_id = $1;

-- name: GetActivePasswordLogin :one
-- Login lookup: the active password credential bound to an identifier, with
-- its user. Revoked or expired credentials never match.
SELECT sqlc.embed(c), p.hash, p.identifier_id
FROM mod_users.user_identifiers i
JOIN mod_users.cred_password p ON p.identifier_id = i.id AND p.active
JOIN mod_users.credentials c ON c.id = p.credential_id
WHERE i.kind = $1
  AND i.value_canon = $2
  AND c.revoked_at IS NULL
  AND (c.expires_at IS NULL OR c.expires_at > now());

-- name: GetActiveUnboundPassword :one
SELECT sqlc.embed(c), p.hash
FROM mod_users.cred_password p
JOIN mod_users.credentials c ON c.id = p.credential_id
WHERE p.principal_id = $1
  AND p.identifier_id IS NULL
  AND p.active;

-- name: ListActivePasswordsByPrincipal :many
SELECT sqlc.embed(c), p.hash, p.identifier_id
FROM mod_users.cred_password p
JOIN mod_users.credentials c ON c.id = p.credential_id
WHERE p.principal_id = $1 AND p.active
ORDER BY c.id;

-- name: UpdatePasswordHash :exec
UPDATE mod_users.cred_password SET hash = $2 WHERE credential_id = $1;

-- ---------------------------------------------------------------------------
-- email_otp
-- ---------------------------------------------------------------------------

-- name: CreateEmailOTPCredential :one
INSERT INTO mod_users.cred_email_otp (credential_id, identifier_id)
VALUES ($1, $2)
RETURNING *;

-- name: GetActiveEmailOTPByIdentifier :one
SELECT sqlc.embed(c), o.identifier_id
FROM mod_users.cred_email_otp o
JOIN mod_users.credentials c ON c.id = o.credential_id
WHERE o.identifier_id = $1
  AND o.active
  AND (c.expires_at IS NULL OR c.expires_at > now());

-- ---------------------------------------------------------------------------
-- oidc
-- ---------------------------------------------------------------------------

-- name: CreateOIDCCredential :one
-- A second active (issuer, subject) fails on cred_oidc_active_issuer_subject_uq
-- (23505, mapped to 409 users.oidc_identity_in_use).
INSERT INTO mod_users.cred_oidc (credential_id, provider_id, issuer, subject, email_at_link)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetActiveOIDCByIssuerSubject :one
-- The known-identity login path; it never matches by email.
SELECT sqlc.embed(c), o.provider_id, o.issuer, o.subject, o.email_at_link
FROM mod_users.cred_oidc o
JOIN mod_users.credentials c ON c.id = o.credential_id
WHERE o.issuer = $1
  AND o.subject = $2
  AND o.active
  AND (c.expires_at IS NULL OR c.expires_at > now());

-- name: GetOIDCByCredentialID :one
SELECT * FROM mod_users.cred_oidc WHERE credential_id = $1;

-- name: ListActiveOIDCByPrincipal :many
SELECT sqlc.embed(c), o.provider_id, o.issuer, o.subject, o.email_at_link
FROM mod_users.cred_oidc o
JOIN mod_users.credentials c ON c.id = o.credential_id
WHERE c.principal_id = $1 AND o.active
ORDER BY c.id;

-- ---------------------------------------------------------------------------
-- ssh_key
-- ---------------------------------------------------------------------------

-- name: CreateSSHKeyCredential :one
-- A second active fingerprint fails on cred_ssh_key_active_fingerprint_uq
-- (23505, mapped to 409 users.ssh_key_in_use).
INSERT INTO mod_users.cred_ssh_key (credential_id, key_type, public_key, fingerprint_sha256)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetActiveSSHKeyByFingerprint :one
-- The resolver lookup (read-only; the active fingerprint index serves it).
SELECT sqlc.embed(c), k.key_type, k.public_key, k.fingerprint_sha256
FROM mod_users.cred_ssh_key k
JOIN mod_users.credentials c ON c.id = k.credential_id
WHERE k.fingerprint_sha256 = $1
  AND k.active
  AND (c.expires_at IS NULL OR c.expires_at > now());

-- name: ListSSHKeysByPrincipal :many
-- Active keys only; revoked keys stay in ListCredentialsByPrincipal.
SELECT sqlc.embed(c), k.key_type, k.public_key, k.fingerprint_sha256
FROM mod_users.cred_ssh_key k
JOIN mod_users.credentials c ON c.id = k.credential_id
WHERE c.principal_id = $1 AND k.active
ORDER BY c.id;

-- ---------------------------------------------------------------------------
-- api_key
-- ---------------------------------------------------------------------------

-- name: CreateAPIKeyCredential :one
INSERT INTO mod_users.cred_api_key (credential_id, client_id, secret_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetActiveAPIKeyByClientID :one
-- client_credentials grant lookup; revoked or expired keys never match.
SELECT sqlc.embed(c), k.client_id, k.secret_hash
FROM mod_users.cred_api_key k
JOIN mod_users.credentials c ON c.id = k.credential_id
WHERE k.client_id = $1
  AND c.revoked_at IS NULL
  AND (c.expires_at IS NULL OR c.expires_at > now());

-- name: ListAPIKeysByPrincipal :many
SELECT sqlc.embed(c), k.client_id
FROM mod_users.cred_api_key k
JOIN mod_users.credentials c ON c.id = k.credential_id
WHERE c.principal_id = $1 AND c.revoked_at IS NULL
ORDER BY c.id;

-- ---------------------------------------------------------------------------
-- anon
-- ---------------------------------------------------------------------------

-- name: CreateAnonCredential :one
INSERT INTO mod_users.cred_anon (credential_id, device_id, token_hash)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetActiveAnonByTokenHash :one
SELECT sqlc.embed(c), a.device_id, a.token_hash
FROM mod_users.cred_anon a
JOIN mod_users.credentials c ON c.id = a.credential_id
WHERE a.token_hash = $1
  AND c.revoked_at IS NULL
  AND (c.expires_at IS NULL OR c.expires_at > now());
