-- Queries over mod_users.party_authn_policies and
-- mod_users.credential_party_authorizations. Credential confinement lives on
-- credentials (SetCredentialConfinement in credentials.sql).

-- name: GetPartyAuthnPolicy :one
-- No row means credential_rule 'any' and min_acr 1.
SELECT * FROM mod_users.party_authn_policies WHERE party_id = $1;

-- name: UpsertPartyAuthnPolicy :one
INSERT INTO mod_users.party_authn_policies (
    party_id, credential_rule, required_oidc_provider_id, min_acr
) VALUES (
    $1, $2, $3, $4
)
ON CONFLICT (party_id) DO UPDATE SET
    credential_rule           = EXCLUDED.credential_rule,
    required_oidc_provider_id = EXCLUDED.required_oidc_provider_id,
    min_acr                   = EXCLUDED.min_acr
RETURNING *;

-- name: DeletePartyAuthnPolicy :execrows
DELETE FROM mod_users.party_authn_policies WHERE party_id = $1;

-- name: AuthorizeCredentialForParty :one
-- Idempotent; an existing authorization is returned unchanged.
INSERT INTO mod_users.credential_party_authorizations (
    credential_id, party_id, authorized_by_profile_id
) VALUES (
    $1, $2, $3
)
ON CONFLICT (credential_id, party_id) DO UPDATE
SET credential_id = EXCLUDED.credential_id
RETURNING *;

-- name: DeleteCredentialPartyAuthorization :execrows
DELETE FROM mod_users.credential_party_authorizations
WHERE credential_id = $1 AND party_id = $2;

-- name: GetCredentialPartyAuthorization :one
SELECT * FROM mod_users.credential_party_authorizations
WHERE credential_id = $1 AND party_id = $2;

-- name: ListCredentialPartyAuthorizationsByParty :many
SELECT sqlc.embed(a), c.uuid AS credential_uuid, c.principal_id, c.kind, c.label
FROM mod_users.credential_party_authorizations a
JOIN mod_users.credentials c ON c.id = a.credential_id
WHERE a.party_id = $1
ORDER BY a.authorized_at, a.credential_id;

-- name: ListCredentialPartyAuthorizationsByCredential :many
SELECT * FROM mod_users.credential_party_authorizations
WHERE credential_id = $1
ORDER BY party_id;

-- name: ListConfinedCredentialsByParty :many
-- Credentials confined to the party, for the confinement listing.
SELECT * FROM mod_users.credentials
WHERE confined_to_party_id = $1 AND revoked_at IS NULL
ORDER BY id;

-- name: DeleteCredentialAuthorizationsForHolderParty :execrows
-- Departure cleanup: removes the party authorizations of every credential held
-- by the departing principal.
DELETE FROM mod_users.credential_party_authorizations a
USING mod_users.credentials c
WHERE a.credential_id = c.id
  AND a.party_id = $1
  AND c.principal_id = $2;

-- name: CopyCredentialPartyAuthorizations :execrows
-- Supersede: carries every party authorization of the old credential to its
-- replacement (same authorizer and time), skipping parties the replacement is
-- already authorized for.
INSERT INTO mod_users.credential_party_authorizations (
    credential_id, party_id, authorized_by_profile_id, authorized_at
)
SELECT sqlc.arg(new_credential_id)::bigint, o.party_id, o.authorized_by_profile_id, o.authorized_at
FROM mod_users.credential_party_authorizations o
WHERE o.credential_id = sqlc.arg(old_credential_id)::bigint
ON CONFLICT (credential_id, party_id) DO NOTHING;

-- name: HolderPolicyMismatch :one
-- Whether the holder's current credentials can meet the party's authentication
-- policy (mod_users.holder_policy_mismatch). reason is NULL when they can.
-- invite_time true relaxes the 'authorized' rule to any usable interactive
-- credential (no profile exists yet); false is the strict outside-collaborator
-- check requiring an explicit authorization or confinement.
SELECT reason, credential_rule, required_provider_id, min_acr, holder_acr
FROM mod_users.holder_policy_mismatch(@holder_id::bigint, @party_id::bigint, @invite_time::boolean);
