# Phase 4, Task 1 — Password registration + login

## Context
Classic email + password is the primary local auth path. Argon2id for hashing; constant-time compares; consistent timing on failed lookups to prevent enumeration via login latency.

## Acceptance

`api/internal/auth/password.go`:
- `Hash(plain string) (string, error)` → PHC-format argon2id string. Params: `m=64MiB, t=3, p=2, saltLen=16, hashLen=32`.
- `Verify(plain, encoded string) (bool, error)` — parses PHC, recomputes, constant-time compare.

`api/internal/handlers/auth/register.go` (`POST /v1/auth/register`):
- Body: `{ email, password, given_name, family_name }`.
- Validate: email format, password length ≥ 12, names non-empty after trim.
- Single transaction:
  1. Insert `entities` row (`kind='legal_entity'`).
  2. Insert `legal_entities` (`kind='natural_person'`).
  3. Insert `natural_persons`.
  4. Insert `users` (email lowercased; `email_verified_at` = NULL; `is_admin` = (count==0)).
  5. Insert `auth_local` with hashed password.
- On unique-violation on email → 409 `{"error":{"code":"email_taken"}}`.
- 201 with `{ uuid, email }` (no token; user must verify email first OR log in to get a token).
- Sends email verification (uses Task 4.2 sender).
- Audits `op='create'`, `resource='users'`.

`api/internal/handlers/auth/login.go` (`POST /v1/auth/login`):
- Body: `{ email, password }`.
- Lookup user by `lower(email)`; load `auth_local`. If either missing → constant-time fake verify against a stored dummy hash, then 401.
- On verify success → mint local JWT (24h, `iss=Config.LocalIssuer`, `sub=user.uuid`, `roles=[]` plus `"admin"` if `is_admin`).
- 200 `{ token, user: {uuid, email, is_admin} }`.
- Audits `op='login'`, `resource='users'`, `target_entity_id=user.entity_id`.

## How to verify
- Register a user → 201, row chain present in DB.
- Log in → 200 with token. `curl -H "Authorization: Bearer <token>" /v1/self` returns the user.
- Wrong password → 401, audit row NOT written (only success audits login per acceptance).
- Two registrations same email → 409.
- First registration → `is_admin=true`; second → `is_admin=false`.

## Notes
- Document the "first user is root" rule in code comments referencing CLAUDE.md.
- Login failure must take roughly the same wall-clock time whether the email exists or not — verify a dummy hash so we always pay the argon2 cost.
