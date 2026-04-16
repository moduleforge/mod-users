# Phase 5, Task 1 — Admin user CRUD

## Context
Admins need to create, read, update, and archive users of any kind (natural_person, corporation, service_account).

## Acceptance

`api/internal/handlers/users/users.go`:

`POST /v1/users` (admin):
- Body per kind (see phase notes).
- Single transaction: insert entity → (legal_entity → natural_person|corporation) | service_account → user.
- Email uniqueness honored; 409 on conflict.
- Optional `?invite=true` triggers an email-code request with `purpose='login'`.
- 201 with full user JSON.
- Audit `op='create'`, `resource='users'`.

`GET /v1/users/:uuid` (admin OR self):
- 200 with full user JSON (entity block included; same shape as `/v1/self`).
- 404 if not found OR (non-admin AND not self).

`PUT /v1/users/:uuid` (admin OR self):
- Editable by self: `given_name`, `family_name`, `legal_name`, `jurisdiction`, `label`, `default_app_uuid`.
- Editable by admin only: `is_admin` (use grant/revoke endpoints — reject here), `email` (with verification reset).
- Email change: clears `email_verified_at` and queues a verification email.
- 200 with updated JSON.
- Audit `op='update'` with before/after.

`DELETE /v1/users/:uuid` (admin):
- Sets `entities.archived_at = now()`. Does NOT delete `users` row.
- Refuse if `is_admin=true` AND this would leave zero non-archived admins → 409 `last_admin`.
- 204.
- Audit `op='delete'`.

## How to verify
- Admin creates a natural_person user → 201, three rows present (entity, legal_entity, natural_person, user).
- Self GET own profile → 200; non-admin GET other user → 404.
- Self PUT own profile changes given_name → 200, audit row written.
- Self attempts to PUT another user → 404.
- Admin DELETE last admin → 409.

## Notes
- Use a `usersService.Create(ctx, req)` to encapsulate the multi-table insert; handlers stay thin.
- The "last admin" guard is a SELECT inside the same transaction.
