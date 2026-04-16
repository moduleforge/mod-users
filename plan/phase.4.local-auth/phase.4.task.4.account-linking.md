# Phase 4, Task 4 — Account-linking by verified email

## Context
CLAUDE.md: "users can authenticate using any method to their same account (matched on email)". A user who registered locally and later signs in via Google should land on the same account. Same in reverse.

## Acceptance

`api/internal/auth/linking.go`:
- `Linker.AttachOIDC(ctx, principal Principal) (*User, bool, error)` — called by `UserResolver` in middleware (Phase 3 Task 3.3) **before** falling back to "create new user".
- Logic:
  1. If a user already exists with `(auth_issuer, auth_id) = principal`, return them. (Already-linked case — fast path.)
  2. Else if `principal.Email` is non-empty AND we trust it (provider asserts `email_verified=true` OR provider is in a configured trust list `OIDC_TRUSTED_EMAIL_PROVIDERS`):
     - Look up user by `lower(email) = lower(principal.email)` AND `email_verified_at IS NOT NULL`.
     - If found: attach `(auth_issuer, auth_id)` to that user. Return `(user, false /* not newly created */, nil)`. Audit `op='update'`, `resource='users'`, with before/after showing the auth fields populated.
  3. Else: create a fresh user (existing Phase 3 behavior). Return `(user, true, nil)`.
- The same logic applies on local email-verify (Phase 4 Task 2): when a local user verifies their email, scan for any orphan OIDC-only users with the same email and offer linking via an admin task (defer to v2; for v1, simply log a warning). Document this seam.

Email-verify flow (`POST /v1/auth/verify-email/confirm`):
- Token-based variant of email-code with `purpose='verify_email'`. Sets `email_verified_at`, then runs linker introspection (no auto-merge in v1, just log).

## How to verify
- Local user `alice@example.test` verifies email; logs in.
- Same Alice signs into Google with `alice@example.test`; second login attaches OIDC to existing user (verify by checking `users` row count remained the same and `auth_issuer/auth_id` populated).
- Audit row records the linking event.
- Unverified-email OIDC users do NOT auto-link (they get a fresh user).

## Notes
- Trusting `email_verified` from OIDC: Google and Microsoft set this reliably. For Authelia/Keycloak, depends on config — hence the `OIDC_TRUSTED_EMAIL_PROVIDERS` allowlist override.
- Merging existing duplicate accounts (where both already exist) is a manual admin action and is OUT of scope for v1 — note in code as TODO with a `// MERGE-V2` marker.
