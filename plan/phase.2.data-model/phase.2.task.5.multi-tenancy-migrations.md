# Phase 2, Task 5 — Multi-tenancy migrations

## Context
Apps are tenants. A user can belong to multiple apps; each membership carries app-scoped roles. `users.default_app_id` provides the fallback context when a request omits the `X-App` header.

## Acceptance

`0010_apps.sql`:
```sql
CREATE TABLE apps (
  id          BIGSERIAL PRIMARY KEY,
  uuid        UUID UNIQUE NOT NULL DEFAULT gen_random_uuid(),
  slug        TEXT NOT NULL UNIQUE,
  name        TEXT NOT NULL,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  archived_at TIMESTAMPTZ
);

ALTER TABLE users
  ADD CONSTRAINT users_default_app_fk
  FOREIGN KEY (default_app_id) REFERENCES apps(id) ON DELETE SET NULL;
```

`0011_apps_users.sql`:
```sql
CREATE TABLE apps_users (
  app_id      BIGINT NOT NULL REFERENCES apps(id) ON DELETE CASCADE,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  roles       TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
  assigned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (app_id, user_id)
);
CREATE INDEX apps_users_user_idx ON apps_users(user_id);
```

## How to verify
- Migrations apply.
- A user with `default_app_id` set to a non-existent app fails FK.
- Deleting an app cascades its `apps_users` rows but does NOT delete the user.

## Notes
- `roles TEXT[]` is intentionally simple for v1. If RBAC matures, lift into `apps_user_roles` later.
- The global `users.is_admin` is system-wide; per-app role membership is the per-tenant authorization signal.
