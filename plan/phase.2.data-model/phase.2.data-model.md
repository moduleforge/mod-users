# Phase 2 — Data model

## Goal
Land the full Postgres schema, Atlas migrations, triggers, and sqlc-generated query packages. After this phase, the api can compile against generated query types even if no handlers consume them yet.

## Outputs
- `model/migrations/0001_entities.sql` … `00NN_*.sql`
- `model/queries/<concept>.sql` per concept
- `model/sqlc.yaml`
- `model/atlas.hcl`
- `model/scripts/relink_auth.sql` (provider migration script)

## Hard rules
- **Vanilla SQL only.** No Postgres-specific types in the schema; use `TEXT` + `CHECK` for enums, `BIGSERIAL`/sequences for internal IDs, `UUID` for external. (`gen_random_uuid()` is a `pgcrypto` function — wrap inserts at the app layer if portability requires it; document the one acceptable Postgres dependency: the `uuid-ossp` or `pgcrypto` extension.)
- **Tables plural.** `entities`, `users`, `apps_users`.
- **Internal IDs never serialized.** Foreign keys join on `BIGINT id`; APIs only ever expose `uuid`.
- **Foreign keys point upward only** through the CTI hierarchy.
- **No cross-concept views in the database.** Joins requiring multiple hierarchy levels live in the api layer.
- **Soft delete on `entities`** via `archived_at TIMESTAMPTZ`. Children inherit archive state by join.

## CTI hierarchy
```
entities (root)
  ├── legal_entities          ← entity.kind = 'legal_entity'
  │     ├── natural_persons    ← legal_entities.kind = 'natural_person'
  │     └── corporations       ← legal_entities.kind = 'corporation'
  └── service_accounts         ← entity.kind = 'service_account'

users (role extension; users.entity_id → entities.id; trigger enforces leaf kind)
```

## Auth tables
- `users` — one row per identity holder; columns: `id BIGSERIAL`, `uuid UUID UNIQUE`, `entity_id BIGINT REFERENCES entities`, `email TEXT UNIQUE`, `email_verified_at TIMESTAMPTZ`, `is_admin BOOLEAN`, `default_app_id BIGINT REFERENCES apps`, `auth_issuer TEXT`, `auth_id TEXT`, `created_at`, `updated_at`. **`UNIQUE (auth_issuer, auth_id)`** compound index. `entity_id` enforced-leaf via trigger.
- `auth_local` — local credential storage. Columns: `user_id BIGINT REFERENCES users`, `password_hash TEXT NOT NULL` (argon2id), `password_updated_at`, `created_at`. Separate table so no-password OIDC users have no row.
- `email_codes` — short-lived one-time codes for email-code login: `user_id`, `code_hash TEXT`, `purpose TEXT CHECK (purpose IN ('login','verify_email'))`, `expires_at`, `consumed_at`.
- `password_resets` — `user_id`, `token_hash TEXT`, `expires_at`, `consumed_at`.

## Multi-tenancy tables
- `apps` — `id`, `uuid`, `slug TEXT UNIQUE`, `name`, `created_at`, `archived_at`.
- `apps_users` — `app_id`, `user_id`, `roles TEXT[]` (or `role TEXT` + cross table — pick `TEXT[]` here for v1 simplicity; revisit if RBAC grows), `assigned_at`. Composite PK `(app_id, user_id)`.
- `users.default_app_id` — nullable FK; per-request `X-App` header overrides.

## Audit
- `audit_log` — append-only: `id`, `actor_user_id BIGINT REFERENCES users`, `assumed_user_id BIGINT NULL REFERENCES users` (set when admin assumes identity), `target_entity_id BIGINT REFERENCES entities`, `op TEXT CHECK (op IN ('create','update','delete','assume','login','grant','revoke'))`, `resource TEXT`, `before JSONB`, `after JSONB`, `at TIMESTAMPTZ DEFAULT now()`. Indexes on `(actor_user_id, at DESC)`, `(target_entity_id, at DESC)`.

## Tasks
- 2.1 Atlas + sqlc setup
- 2.2 Entity hierarchy migrations (entities, legal_entities, natural_persons, corporations, service_accounts)
- 2.3 Users migration with leaf-kind trigger and UNIQUE auth index
- 2.4 Local auth migrations (auth_local, email_codes, password_resets)
- 2.5 Multi-tenancy migrations (apps, apps_users)
- 2.6 Audit log migration + insert helper
- 2.7 sqlc query files per concept

## Notes
- `legal_id` is OMITTED from natural_persons and corporations per owner decision.
- `relink_auth.sql` script lives in `model/scripts/` for moving a user between OIDC providers.
- Trigger code for `users.entity_id` leaf-kind enforcement must be inline in `0006_users.sql` so the implementer doesn't reinvent it.
