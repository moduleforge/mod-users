-- name: GetSystemActorBySlug :one
SELECT entity_id, slug FROM system_actors WHERE slug = $1;
