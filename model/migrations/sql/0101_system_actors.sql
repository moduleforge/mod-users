-- +goose Up

-- ---------------------------------------------------------------------------
-- (a) Seed the 'system_actor' type
-- ---------------------------------------------------------------------------
-- system_actor is a concrete type directly under the root 'entity' type, not
-- under 'legal_entity'. This is load-bearing in three ways (per the anonymous
-- actor architecture proposal):
--   * It never self-owns: entities_owner_default_self (mod-core's
--     0013_entity_ownership.sql) fires only for types descending from
--     natural_person or service_account, neither of which system_actor is,
--     so a system_actor entity keeps owner_id NULL.
--   * It cannot join an actor group: authz_actor_group_members' type-check
--     trigger (mod-authz's 0502_authz_actor_group_members.sql) admits only
--     authz_actor_group and legal_entity descendants.
--   * It stays invisible to every list path: system_actor is deliberately
--     never added to authzSlugs in api/cmd/server/main.go, so no
--     accessible_system_actor_ids_for_actor function is ever generated.
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM types WHERE slug = 'entity') THEN
    RAISE EXCEPTION 'seed for system_actor requires parent slug ''entity'' to exist';
  END IF;
END $$;
-- +goose StatementEnd

INSERT INTO types (slug, parent_id, concrete, name, description)
SELECT
  'system_actor',
  id,
  true,
  'System Actor',
  'A zero-authority, platform-seeded principal (e.g. the shared anonymous actor). Never owns entities, never holds grants, never appears in list results.'
FROM types WHERE slug = 'entity';

-- ---------------------------------------------------------------------------
-- (b) system_actors CTI table
-- ---------------------------------------------------------------------------
-- No updated_at / set_updated_at: system_actors rows are seed data and are
-- never updated after insert.
CREATE TABLE system_actors (
  entity_id  BIGINT PRIMARY KEY REFERENCES entities(id) ON DELETE RESTRICT,
  slug       TEXT UNIQUE NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Enforce that the referenced entity's fundamental type is exactly
-- 'system_actor' (or a descendant of it).
-- +goose StatementBegin
CREATE FUNCTION system_actors_check_type() RETURNS TRIGGER AS $$
DECLARE
  v_type_id BIGINT;
BEGIN
  SELECT fundamental_type_id INTO v_type_id FROM entities WHERE id = NEW.entity_id;
  IF NOT FOUND THEN
    RAISE EXCEPTION 'system_actors: entity_id % does not reference a known entity', NEW.entity_id;
  END IF;
  IF NOT type_is_or_descends_from(v_type_id, 'system_actor') THEN
    RAISE EXCEPTION 'system_actors: entity % fundamental type is not system_actor', NEW.entity_id;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER system_actors_type_check
  BEFORE INSERT ON system_actors
  FOR EACH ROW EXECUTE FUNCTION system_actors_check_type();

-- ---------------------------------------------------------------------------
-- (c) Seed the one row: slug 'anonymous'
-- ---------------------------------------------------------------------------
-- Idempotent-safe: bails out entirely if the 'anonymous' system_actors row
-- already exists, so the entities insert and the system_actors insert are
-- guarded consistently -- no orphan entities row can be produced by a re-run.
-- Does not hardcode entities.id (BIGSERIAL, not deterministic across
-- environments); everything downstream resolves the actor by slug via the
-- GetSystemActorBySlug query.
-- +goose StatementBegin
DO $$
DECLARE
  v_type_id   BIGINT;
  v_entity_id BIGINT;
BEGIN
  IF EXISTS (SELECT 1 FROM system_actors WHERE slug = 'anonymous') THEN
    RETURN;
  END IF;

  SELECT id INTO v_type_id FROM types WHERE slug = 'system_actor';
  IF NOT FOUND THEN
    RAISE EXCEPTION 'seed for the anonymous system actor requires type slug ''system_actor'' to exist';
  END IF;

  -- owner_id is left unset: entities_owner_default_self only defaults
  -- owner_id for natural_person/service_account descendants, so it never
  -- fires for system_actor -- this row's owner_id stays NULL.
  INSERT INTO entities (fundamental_type_id) VALUES (v_type_id) RETURNING id INTO v_entity_id;
  INSERT INTO system_actors (entity_id, slug) VALUES (v_entity_id, 'anonymous');
END $$;
-- +goose StatementEnd

-- ---------------------------------------------------------------------------
-- (d) entities_no_system_actor_owner ownership guard
-- ---------------------------------------------------------------------------
-- Deliberately BEFORE INSERT OR UPDATE (not BEFORE UPDATE OF owner_id), so it
-- fires on every entities UPDATE, not only ones that touch owner_id. The
-- cost is a primary-key lookup against a one-row table; that is the
-- proposal's accepted trade.
--
-- Postgres fires same-event row-level triggers in alphabetical trigger-name
-- order -- a convention mod-core's 0013_entity_ownership.sql already relies
-- on and documents. Two firing-order facts are load-bearing here:
--   * On INSERT, 'entities_no_system_actor_owner' sorts BEFORE
--     'entities_owner_self_default' ('...no...' < '...ow...'). That is
--     safe: the self-default only ever assigns NEW.id for types descending
--     from natural_person/service_account, neither of which a system_actor
--     is, so no defaulted value can evade this guard.
--   * On UPDATE, 'entities_no_system_actor_owner' sorts BEFORE
--     'entities_owner_immutable'. An UPDATE ... SET owner_id = <anon entity
--     id> therefore raises this trigger's "a system actor may not own an
--     entity" message, not the immutability message.
-- +goose StatementBegin
CREATE FUNCTION entities_check_no_system_actor_owner() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.owner_id IS NOT NULL
     AND EXISTS (SELECT 1 FROM system_actors WHERE entity_id = NEW.owner_id)
  THEN
    RAISE EXCEPTION 'entities: a system actor may not own an entity (owner_id=%)', NEW.owner_id;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER entities_no_system_actor_owner
  BEFORE INSERT OR UPDATE ON entities
  FOR EACH ROW EXECUTE FUNCTION entities_check_no_system_actor_owner();

-- +goose Down

-- Reverse order: drop the entities_no_system_actor_owner trigger, then its
-- function, then the system_actors_type_check trigger and its function,
-- then delete the seeded system_actors and entities rows, then drop the
-- system_actors table. The seeded 'system_actor' type row is a documented
-- exception -- see the note at the end of this block.
DROP TRIGGER IF EXISTS entities_no_system_actor_owner ON entities;
DROP FUNCTION IF EXISTS entities_check_no_system_actor_owner();

DROP TRIGGER IF EXISTS system_actors_type_check ON system_actors;
DROP FUNCTION IF EXISTS system_actors_check_type();

-- Delete the seeded system_actors row first -- its entity_id FK is
-- ON DELETE RESTRICT, so the entities row cannot be deleted while a
-- system_actors row still references it -- then delete the entities row it
-- pointed at, in one statement via a writable CTE so the entity id is still
-- available to the second DELETE after the first has removed its row.
WITH removed AS (
  DELETE FROM system_actors WHERE slug = 'anonymous' RETURNING entity_id
)
DELETE FROM entities WHERE id IN (SELECT entity_id FROM removed);

DROP TABLE IF EXISTS system_actors;

-- Note: types rows are append-only (the types_reject_mutation trigger,
-- defined in mod-core's 0002_types.sql, unconditionally rejects every
-- DELETE FROM types). The 'system_actor' type row therefore cannot be
-- deleted and remains in place after a DOWN migration. This mirrors the
-- same, already-established limitation documented in mod-authz's
-- 0501_authz_actor_groups.sql ("types rows are append-only ... they cannot
-- be deleted ... a deprecated type causes no harm").
