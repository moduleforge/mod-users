# Phase 5, Task 4 — Assume identity

## Context
Per CLAUDE.md: "admins can 'assume' a user's identity, in which case the system should treat the admin account exactly as the assumed user account." Every action while assuming must record both actors in the audit log.

## Acceptance

`POST /v1/users/:uuid/assume` (admin):
- Refuse to assume yourself → 400.
- Refuse to assume an archived user → 400.
- Mints a new local JWT:
  - `sub` = admin's uuid (unchanged)
  - `assume` = target user's uuid (new claim)
  - `roles` = the assumed user's roles (NOT admin) — per CLAUDE.md, treat exactly as the assumed user
  - 4h TTL (shorter than normal 24h to limit blast radius)
- 200 `{ token, assumed_user: { uuid, email } }`.
- Audit `op='assume'`, `actor_user_id=admin`, `assumed_user_id=target`, `target_entity_id=target.entity_id`.

`POST /v1/self/end-assumption`:
- Available to anyone with an `assume`-bearing token.
- Mints a fresh token for the original admin (`sub=admin`, no `assume`, normal 24h TTL, admin roles back).
- 200 `{ token }`. No-op (200) if no assumption active.
- Audit `op='update'`, `resource='session'` is unnecessary; skip.

Middleware update (Phase 3 Task 3.3):
- When `assume` claim present: load assumed user → `UserContext.User`; load admin → `UserContext.AssumedUser` (the actor). All authorization checks see the assumed user, not the admin.
- The audit writer (Phase 3 Task 3.5) already records both actor and assumed when present.

## How to verify
- Admin assumes Bob. Token works against `/v1/self` and returns Bob's profile.
- Admin (while assuming Bob) cannot hit admin-only endpoints (403).
- Mutations during assumption write audit rows with both `actor_user_id` (admin) and `assumed_user_id` (Bob).
- End-assumption returns admin token; admin-only endpoints work again.
- Cannot assume self → 400.

## Notes
- The token claims design must NOT allow a non-admin to forge an `assume` claim. This is enforced by HS256 server-side signing — only the admin path mints these tokens.
- We do NOT support nested assumption (admin assumes Alice, Alice "assumes" Bob — refuse if `assume` already in current token → 400).
