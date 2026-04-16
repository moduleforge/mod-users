# Phase 4, Task 2 — Email-code login

## Context
Email magic-code login is passwordless: user requests a 6-digit code, receives it by email, submits it within 5 minutes for a session. Doubles as email verification.

## Acceptance

`api/internal/email/sender.go`:
- `Sender` interface: `Send(ctx, to, subject, htmlBody, textBody) error`.
- SMTP implementation reading `Config.SMTP*`. Locally points at MailHog (`localhost:1025`). Plain-text + HTML alternative.
- A `NoOp` sender for tests.

`api/internal/handlers/auth/emailcode.go`:

`POST /v1/auth/email-code/request`:
- Body: `{ email, purpose: "login" | "verify_email" }`.
- ALWAYS 204 within ~200ms (min-duration sleep) regardless of whether the email exists.
- If email exists: generate a 6-digit code, hash, insert `email_codes` with `expires_at = now() + 5m`, send email containing the plain code.
- Invalidate (mark `consumed_at = now()`) any prior unconsumed codes for the same `(user_id, purpose)` to prevent stockpiling.

`POST /v1/auth/email-code/verify`:
- Body: `{ email, code, purpose }`.
- Lookup user; lookup latest unconsumed unexpired `email_codes` row for `(user_id, purpose)`.
- Constant-time compare of hash. On mismatch: 401 (audit failure as `op='login'` only when we ship Phase 8 metrics — for v1, no audit on failure).
- On success:
  - Mark code `consumed_at = now()`.
  - If `purpose='login'`: mint local JWT, 200 `{ token, user }`. Audit `op='login'`.
  - If `purpose='verify_email'`: set `users.email_verified_at = now()`, 204. Trigger linking (Task 4.4). Audit `op='update'`, `resource='users'`.

## How to verify
- Request code → 204; mailhog UI shows the email.
- Verify with right code within 5 min → 200, token works against `/v1/self`.
- Verify with wrong code → 401.
- Verify after 5 min → 401.
- Request code twice → only the second code works.

## Notes
- Code generation: `crypto/rand` for a uniform 0..999999, zero-padded.
- Hash: `sha256` with a per-row 16-byte salt stored alongside (extend `email_codes.code_hash` schema if needed) OR bcrypt with a fixed cost — pick one and document. Bcrypt is simpler for v1.
- Anti-enumeration: response code/timing must be identical for known/unknown email.
