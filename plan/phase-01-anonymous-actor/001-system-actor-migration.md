# System Actor Migration And Sqlc Query

## Purpose and scope

Add the data-layer foundation for the anonymous system actor: a new mod-users migration
`model/migrations/sql/0101_system_actors.sql` that seeds a `system_actor` entity type, creates
the `system_actors` class-table-inheritance (CTI) child table, seeds exactly one row (slug
`anonymous`), and installs the `entities_no_system_actor_owner` ownership guard trigger on
`entities`. Also adds one sqlc query, `GetSystemActorBySlug`, and regenerates `model/db/`.

Invoke the standard `implement-task` skill. This is SQL-first work; dispatch with the SQL
developer role and the SQL design standards as the technology layer.

Scope is `model/` only. No Go code outside the regenerated `model/db/` changes in this task.

## Requirements

### 1. `model/migrations/sql/0101_system_actors.sql`

mod-users' migration range is 100–199 with `after: [core]` (`moduleforge.module.yaml`
`migrations:`). `0100_schema.sql` is currently the only file in `model/migrations/sql/`. The
file must carry `-- +goose Up` and a real `-- +goose Down` (unlike `0100_schema.sql`, which
deliberately omits its Down as the base schema). Wrap every statement containing an internal
semicolon — `DO $$ ... $$` blocks and `CREATE FUNCTION ... $$ ... $$` bodies — in
`-- +goose StatementBegin` / `-- +goose StatementEnd`, exactly as
`mod-core/model/migrations/0007_type_service_account.sql` and `0012_service_accounts.sql` do.

Four statements, in this order:

**(a) Seed the `system_actor` type.** Follow the exact shape of
`mod-core/model/migrations/0007_type_service_account.sql`: a guard `DO $$` block that raises
if parent slug `entity` does not exist, then
`INSERT INTO types (slug, parent_id, concrete, name, description) SELECT 'system_actor', id,
true, ..., ... FROM types WHERE slug = 'entity';`.

The parent must be `entity`, **not** `legal_entity`. This is load-bearing in three ways, all
per the proposal:
- It never self-owns: `entities_owner_default_self`
  (`mod-core/model/migrations/0013_entity_ownership.sql`) fires only for types descending from
  `natural_person` or `service_account`, so a `system_actor` entity keeps `owner_id NULL`.
- It cannot join an actor group: `authz_actor_group_members`' type-check trigger
  (`mod-authz/model/migrations/0502_authz_actor_group_members.sql`) admits only
  `authz_actor_group` and `legal_entity` descendants.
- It stays invisible to every list path (no access function is generated for it — see
  "Must not change" below).

**(b) `system_actors` CTI table.**

```
entity_id  BIGINT PRIMARY KEY REFERENCES entities(id)  -- per the proposal
slug       TEXT UNIQUE NOT NULL
```

Give the FK an explicit `ON DELETE RESTRICT` (matching `service_accounts`' FK to `entities`),
and add the standard type-check trigger copied from `mod-core`'s
`0012_service_accounts.sql` / `0010_natural_persons.sql` pattern: a
`system_actors_check_type()` PL/pgSQL function asserting
`type_is_or_descends_from(v_type_id, 'system_actor')` for the referenced entity, plus a
`BEFORE INSERT` trigger `system_actors_type_check` on `system_actors`. Include a
`created_at TIMESTAMPTZ NOT NULL DEFAULT now()` column if it fits the surrounding convention;
do **not** add `updated_at`/`set_updated_at` — these rows are seed data and are never updated.

**(c) Seed the one row.** Insert into `entities` with the `system_actor`
`fundamental_type_id` (resolved by `SELECT id FROM types WHERE slug = 'system_actor'`), leaving
`owner_id` unset — the self-own default trigger does not fire for this type — then insert the
matching `system_actors` row with `slug = 'anonymous'`. Make the seed idempotent-safe in the
usual project style (a single migration runs once per database, but the insert must not
silently create a second row if re-run; a `WHERE NOT EXISTS` guard or an
`ON CONFLICT (slug) DO NOTHING` on the `system_actors` insert is acceptable, provided the
`entities` insert is guarded consistently so no orphan `entities` row can be produced).

Do not hardcode or assume any particular `entities.id` value — it is `BIGSERIAL` and is not
deterministic across environments. Everything downstream resolves by slug.

**(d) `entities_no_system_actor_owner` trigger.** A `BEFORE INSERT OR UPDATE ON entities`
row-level trigger whose function body is, per the proposal:

```
IF NEW.owner_id IS NOT NULL
   AND EXISTS (SELECT 1 FROM system_actors WHERE entity_id = NEW.owner_id)
THEN RAISE EXCEPTION 'entities: a system actor may not own an entity (owner_id=%)', NEW.owner_id;
```

Use exactly the trigger name `entities_no_system_actor_owner` — it is named in the proposal and
in the change request. Two firing-order facts must be understood and recorded in a comment in
the migration (Postgres fires same-event row triggers in **alphabetical name order**, a
convention `0013_entity_ownership.sql` already relies on and documents):

- On INSERT, `entities_no_system_actor_owner` sorts **before** `entities_owner_self_default`
  (`...no...` < `...ow...`). That is safe: the self-default only ever assigns `NEW.id` for
  types descending from `natural_person`/`service_account`, neither of which a `system_actor`
  is, so no defaulted value can evade the guard. State this in the comment so a future reader
  does not have to re-derive it.
- On UPDATE, `entities_no_system_actor_owner` sorts **before** `entities_owner_immutable`. An
  `UPDATE ... SET owner_id = <anon entity id>` therefore raises the system-actor message, not
  the immutability message. Task 006's tests assert on this; note it in the comment.

Note the trigger is deliberately `BEFORE INSERT OR UPDATE` (not `BEFORE UPDATE OF owner_id`),
so it fires on every `entities` UPDATE. The cost is a primary-key lookup against a one-row
table; that is the proposal's accepted trade.

**Down migration.** Reverse order: drop the `entities_no_system_actor_owner` trigger, then its
function, then the `system_actors_type_check` trigger and its function, then delete the seeded
`system_actors` and `entities` rows, then drop the `system_actors` table, then delete the
`system_actor` row from `types`.

### 2. `model/queries/system_actors.sql` — `GetSystemActorBySlug`

One new query file (or an added query in an appropriately named new file — do not append to an
unrelated existing file):

```sql
-- name: GetSystemActorBySlug :one
SELECT entity_id, slug FROM system_actors WHERE slug = $1;
```

Select only the columns the caller uses (no `SELECT *`), per the SQL standards. Adjust the
column list if the table carries a `created_at` the caller does not need.

### 3. Regenerate `model/db/`

Run `cd model && sqlc generate`. `model/db/*.go` is generated and committed; **never** hand-edit
it. `model/sqlc.yaml` points `schema:` at `./migrations/sql`, so the new migration file is what
teaches sqlc about the `system_actors` table. mod-core's own sqlc config parses equivalent
`DO $$` and `CREATE FUNCTION` blocks without incident, so the new statements are expected to
parse; if `sqlc generate` nonetheless fails on them, halt and report rather than restructuring
the migration to please the generator.

Commit the regenerated files alongside the migration.

### Must not change

- Do **not** add `"system_actor"` to `authzSlugs` in `api/cmd/server/main.go`. No
  `accessible_system_actor_ids_for_actor` function may ever be generated; the type must stay
  invisible to every list path. This is a deliberate, load-bearing omission.
- Do not modify `anon_tokens`, `user_accounts`, or anything else in `0100_schema.sql`.
- Do not add a `user_accounts` row for the system actor, now or ever.
- Do not add a guard on `grants` — `grants` is a mod-authz table in migration range 500s, which
  runs *after* mod-users' 100–199 range. The no-grants invariant is enforced by the boot-time
  assertion in task 002 instead.
- Do not touch `model/schema/migrations/` by hand: it is a build artifact assembled by
  `model/Makefile`'s `compose` target from mod-core + mod-authz + mod-users migrations.

## Validation

- `model/migrations/sql/0101_system_actors.sql` exists and is the only new file under
  `model/migrations/sql/`.
- `cd model && sqlc generate` succeeds and produces a `GetSystemActorBySlug` method on the
  generated `Querier` interface and `*Queries` type in `model/db/`. `git status` shows
  `model/db/` changes as generated output, with no hand edits.
- `make -C model compose` succeeds (the composed `model/schema/migrations/` directory is
  reassembled and includes `0101_system_actors.sql`).
- Forward migration applies cleanly from a clean database:
  `cd model && goose -dir migrations/sql postgres "$DB_URL" up` — or the equivalent through
  `make dev.start`. Confirm afterwards, via `make dev.db-connect`:
  - `SELECT count(*) FROM system_actors;` returns `1`.
  - `SELECT slug FROM system_actors;` returns `anonymous`.
  - `SELECT e.owner_id FROM entities e JOIN system_actors s ON s.entity_id = e.id;` returns
    `NULL` — the self-own default did not fire.
  - `SELECT concrete, (SELECT slug FROM types p WHERE p.id = t.parent_id) FROM types t WHERE
    t.slug = 'system_actor';` returns `t, entity`.
- Rollback applies cleanly and leaves no residue:
  `goose -dir migrations/sql postgres "$DB_URL" down` succeeds, and afterwards
  `system_actors`, both trigger functions, both triggers, and the `system_actor` row in `types`
  are all gone.
- Constraint behaviour, checked manually in psql before handing off (task 006 automates these):
  - `INSERT INTO entities (fundamental_type_id, owner_id) VALUES (<corporation type id>,
    (SELECT entity_id FROM system_actors WHERE slug='anonymous'));` raises the
    `a system actor may not own an entity` exception.
  - Inserting a `system_actors` row pointing at an entity of a different fundamental type
    raises the type-check exception.
- `grep -rn "system_actor" api/cmd/server/main.go` returns no match (the `authzSlugs` omission
  holds).
- `make lint` passes for `model/`.

## Metadata

architectural_impact: true

## Assumptions

- `sqlc` and `goose` are installed and on `PATH`; a Postgres instance is reachable. Per
  `AGENTS.md`, `make dev.start` provides one.
- The migration is applied to environments that have already run mod-core's migrations
  (`entities` and the ownership triggers land at core `0008`/`0013`), guaranteed by
  `migrations.after: [core]`.
- `make clean.build` removes `model/db/`; restore with `git checkout HEAD -- model/db/` if it
  gets wiped mid-task.

## References

- [Anonymous-actor architecture proposal](../notes/anonymous-actor-architecture-proposal.md) —
  §2 (ownership guard), §"Data model changes" (the four statements), §"Migration / rollout
  notes" (rollback shape). **Authoritative; do not re-derive the design.**
- `mod-core/model/migrations/0007_type_service_account.sql` — the type-seed pattern to copy.
- `mod-core/model/migrations/0012_service_accounts.sql` and `0010_natural_persons.sql` — the
  CTI table + type-check trigger pattern to copy.
- `mod-core/model/migrations/0013_entity_ownership.sql` — `entities_owner_default_self`,
  `entities_immutable_owner`, and the alphabetical trigger-ordering convention.
- `mod-authz/model/migrations/0502_authz_actor_group_members.sql` — the member-type trigger
  that makes actor-group membership structurally impossible for this type.
- `AGENTS.md` — "Database migrations" and "Code generation (sqlc)".
