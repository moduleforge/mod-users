# Phase 4, Task 3 — Forgot-password flow

## Context
Standard password-reset flow: opaque token sent by email, exchanged for a password update.

## Acceptance

`api/internal/handlers/auth/reset.go`:

`POST /v1/auth/password-reset/request`:
- Body: `{ email }`.
- ALWAYS 204 within ~200ms.
- If user exists: generate 32-byte token (`crypto/rand`), base64url-encode, hash with sha256, insert `password_resets` (`expires_at = now() + 30m`). Email contains link `${GUI_URL}/auth/reset?token=<plain>`.
- Invalidate prior unconsumed reset rows for the user.

`POST /v1/auth/password-reset/confirm`:
- Body: `{ token, new_password }`.
- Validate `new_password` length ≥ 12.
- Hash incoming token, look up unconsumed unexpired row.
- On match: update `auth_local.password_hash`, set `password_updated_at = now()`, mark reset row `consumed_at = now()`. 204.
- On miss: 400 `{"error":{"code":"invalid_or_expired"}}`.
- Audit `op='update'`, `resource='auth_local'`, `target_entity_id=user.entity_id`.
- Bonus: invalidate any in-flight email codes for this user (paranoia).

## How to verify
- Request reset with unknown email → 204, no email sent.
- Request reset with known email → 204, mailhog has email with link.
- Confirm with right token → 204; old password no longer logs in; new password does.
- Confirm with stale token → 400.
- Token can only be consumed once.

## Notes
- Reset link points to GUI; the GUI then submits to `confirm`. Treat the GUI URL as `Config.GUIURL`.
- Reset TTL is 30 minutes; document and surface in the email body.
