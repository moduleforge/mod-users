# Phase 5, Task 3 — Grant / revoke admin

## Context
Dedicated endpoints make the privilege change explicit (and easier to audit) than a generic PUT.

## Acceptance

`POST /v1/users/:uuid/grant-admin` (admin):
- Sets `users.is_admin = TRUE`. Idempotent.
- 204.
- Audit `op='grant'`, `resource='users'`, `target_entity_id=user.entity_id`, `before={"is_admin":false}`, `after={"is_admin":true}`.

`POST /v1/users/:uuid/revoke-admin` (admin):
- Sets `users.is_admin = FALSE`. Idempotent if already false.
- Refuse if it would leave zero admins → 409 `last_admin`.
- Refuse if revoking yourself → 400 `cannot_revoke_self` (forces explicit two-admin handoff).
- 204.
- Audit `op='revoke'`.

## How to verify
- Grant → user gains admin (verify by login; their JWT now includes `roles:["admin"]` after re-login).
- Revoke last admin → 409.
- Revoke self → 400.
- Audits present with right before/after.

## Notes
- Existing tokens for the demoted user remain valid until expiry — that's an acceptable v1 trade-off (24h TTL). Document; revocation lists are out of scope.
