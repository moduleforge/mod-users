# Phase 5 — User management

## Goal
Admin-facing user CRUD, search, role grants, identity assumption, and audit/history queries.

## Endpoints (under `/v1`, all require `RequireAuth`; admin-only flagged)

| Method | Path | Auth | Purpose |
|---|---|---|---|
| POST | `/users` | admin | Create user (any kind: natural_person, corporation, service_account) |
| GET | `/users` | admin | Search/list users (`?q=`, `?email=`, `?app=<uuid>`, pagination) |
| GET | `/users/:uuid` | admin OR self | Get user detail |
| PUT | `/users/:uuid` | admin OR self | Update profile |
| DELETE | `/users/:uuid` | admin | Soft-delete (archive entity) |
| POST | `/users/:uuid/grant-admin` | admin | Set `is_admin=true` |
| POST | `/users/:uuid/revoke-admin` | admin | Set `is_admin=false` |
| POST | `/users/:uuid/assume` | admin | Begin assuming this user's identity |
| POST | `/self/end-assumption` | any | End assumption (no-op if not assuming) |
| GET | `/users/:uuid/audit` | admin | Audit rows where `actor_user_id = user.id` |
| GET | `/audit/:object_uuid` | admin | Audit rows where `target_entity_id` resolves to this entity uuid |
| GET | `/audit` | admin | Recent audit (paginated, filterable by `op`, `actor_uuid`) |

## Hard rules
- Admins are global; per-app authorization is layered on top via `apps_users.roles` (Phase 6).
- "Edit own profile" rule: `PUT /users/:uuid` allowed if `:uuid == self.uuid` (subset of fields) OR `IsAdmin`.
- Deleting a user archives the underlying entity (`archived_at = now()`); does not hard-delete.
- Cannot delete or revoke-admin yourself if you're the last admin (return 409 `last_admin`).
- Assumption: when an admin calls `POST /users/:uuid/assume`, the API mints a NEW token whose `sub` remains the admin's uuid but adds claim `assume: <target uuid>`. All subsequent middleware uses the assumed user for `UserContext.User` and stores the original admin in `UserContext.AssumedUser` (read: actor). Audit rows record both. End-assumption mints back a token without the `assume` claim.

## Tasks
- 5.1 Admin user CRUD
- 5.2 User search
- 5.3 Admin grant/revoke
- 5.4 Assume identity
- 5.5 Audit endpoints

## Notes
- `POST /users` requires choosing the entity kind. Body shape:
  ```json
  { "kind": "natural_person|corporation|service_account",
    "email": "...",
    "given_name": "...", "family_name": "...",      // natural_person
    "legal_name": "...", "jurisdiction": "...",     // corporation
    "label": "..."                                  // service_account
  }
  ```
- `POST /users` as an admin-create flow does NOT auto-send an email. Optional `?invite=true` query param sends an email-code with `purpose='login'` so the user can claim the account. Document this clearly.
- Service-account creation skips `legal_entities`; the trigger from Phase 2 Task 3 already accommodates this leaf path.
