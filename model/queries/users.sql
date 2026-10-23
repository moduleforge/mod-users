-- Queries over mod_users.users. Only mod_users relations appear in this
-- directory's queries: sqlc compiles against the mod_users migrations alone, so
-- anything that needs public.* (entities, principals, profiles) goes through
-- mod-core's coredb queries or a mod_users function that reads them.

-- name: CreateUser :one
-- Insert the users row for an already-created principal entity. The caller
-- creates the entity (owner = the user itself, or managing_party_id) first, and
-- the user's personal profile in the same transaction for a bring-your-own user.
INSERT INTO mod_users.users (
    entity_id, default_app_id, is_guest, managing_party_id, person_id
) VALUES (
    $1, $2, $3, $4, $5
)
RETURNING *;

-- name: GetUserByEntityID :one
SELECT * FROM mod_users.users WHERE entity_id = $1;

-- name: GetUserByEntityIDForUpdate :one
-- Row-locks the user; serializes per-holder flows (admission, upgrade).
SELECT * FROM mod_users.users WHERE entity_id = $1 FOR UPDATE;

-- name: ListUsersByEntityIDs :many
-- Batch read for directory lookups; unknown ids are simply absent.
SELECT * FROM mod_users.users WHERE entity_id = ANY(@entity_ids::bigint[]);

-- name: GetUserByIdentifier :one
-- Resolve the user holding an identifier (kind, canonical value).
SELECT u.*
FROM mod_users.users u
JOIN mod_users.user_identifiers i ON i.user_id = u.entity_id
WHERE i.kind = $1 AND i.value_canon = $2;

-- name: UpdateUserDefaults :one
-- Set both defaults; a NULL clears the default.
UPDATE mod_users.users
SET default_profile_id = @default_profile_id,
    default_app_id     = @default_app_id
WHERE entity_id = @entity_id
RETURNING *;

-- name: SetUserDefaultProfile :exec
UPDATE mod_users.users
SET default_profile_id = $2
WHERE entity_id = $1;

-- name: SetUserPerson :one
-- Link (or clear) the person record; refused by users_guest_unlinked for a guest.
UPDATE mod_users.users
SET person_id = $2
WHERE entity_id = $1
RETURNING *;

-- name: UpgradeGuestUser :one
-- is_guest only goes true -> false (users_validate_invariants refuses the reverse).
UPDATE mod_users.users
SET is_guest = false,
    person_id = $2
WHERE entity_id = $1 AND is_guest
RETURNING *;

-- name: ListManagedUsersByParty :many
SELECT * FROM mod_users.users
WHERE managing_party_id = $1
ORDER BY entity_id;

-- name: ListUsersByPerson :many
SELECT * FROM mod_users.users WHERE person_id = $1 ORDER BY entity_id;
