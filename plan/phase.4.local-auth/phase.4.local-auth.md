# Phase 4 — Local authentication

## Goal
Enable account creation, login, password recovery, and email-code login without an external OIDC provider — and seamlessly link those local identities with OIDC identities by verified email.

## Outputs
- `api/internal/handlers/auth/{register,login,emailcode,reset}.go`
- `api/internal/auth/local.go` — local JWT issuer/verifier + UserResolver path for local tokens
- `api/internal/email/sender.go` — SMTP sender (uses MailHog locally)
- `api/internal/auth/linking.go` — verified-email account-linking service

## Endpoints
- `POST /v1/auth/register` — `{ email, password, given_name, family_name }` → 201, sends verification email. Creates entity → legal_entity → natural_person → user → auth_local rows in one transaction.
- `POST /v1/auth/login` — `{ email, password }` → 200 with `{ token }` (local JWT) or 401.
- `POST /v1/auth/email-code/request` — `{ email }` → 204 (always, even if email unknown — anti-enumeration). Sends a 6-digit code, valid 5 min.
- `POST /v1/auth/email-code/verify` — `{ email, code }` → 200 `{ token }`. Marks email_verified_at on first use.
- `POST /v1/auth/password-reset/request` — `{ email }` → 204 (anti-enumeration). Sends opaque reset link.
- `POST /v1/auth/password-reset/confirm` — `{ token, new_password }` → 204.
- `POST /v1/auth/verify-email/confirm` — `{ token }` → 204; sets `email_verified_at` and triggers linking.

## Hard rules
- Passwords hashed with **argon2id** (`golang.org/x/crypto/argon2`). Minimum length 12, no other complexity rules. Encode using PHC string format (`$argon2id$v=19$m=…$t=…$p=…$salt$hash`).
- Email codes are 6 digits, hashed in DB (never stored plain). Compare with constant-time.
- Reset tokens are 32-byte opaque (base64url), hashed in DB.
- All anti-enumeration endpoints (`request`) ALWAYS return 204 within constant time (use a min-duration sleep) regardless of whether the email exists.
- Local JWT: HS256, signed with `Config.JWTSecret`, `iss = Config.LocalIssuer` (e.g. `users-module-local`), 24h TTL, `sub = users.uuid`, custom claim `roles`.
- All anti-enumeration responses, all login attempts, and all reset/verify confirms write `audit_log` rows.

## Tasks
- 4.1 Password (argon2id) registration + login
- 4.2 Email-code request + verify (5-min TTL)
- 4.3 Forgot-password flow
- 4.4 Account-linking by verified email

## Notes
- The auth middleware in Phase 3 must accept BOTH the OIDC verifier and the local JWT verifier, choosing by `iss`.
- Linking by verified email: when a user with a verified email logs in via OIDC for the first time and the OIDC `email` matches an existing local user, attach `(auth_issuer, auth_id)` to that existing user row instead of creating a new one. Requires the OIDC token to expose a verified email.
- "Forgot password" UI lives in Phase 7.
