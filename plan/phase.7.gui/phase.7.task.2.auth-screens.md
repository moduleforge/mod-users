# Phase 7, Task 2 — Login / signup / forgot / reset / verify screens

## Acceptance

`/login` (`app/(auth)/login/page.tsx`):
- Two panes: "Email + password" form and a list of OIDC providers (Google, Microsoft, Authelia — visibility driven by `NEXT_PUBLIC_OIDC_PROVIDERS`).
- "Sign in with code instead" link → `/login/email-code`.
- Forgot-password link.

`/login/email-code` flow:
- Step 1: email input → POST `/v1/auth/email-code/request`. Always shows "Check your email" on success regardless of result.
- Step 2: 6-digit code input → POST `/v1/auth/email-code/verify`. On 200, store session, redirect.

`/signup`:
- Form: email, password, given_name, family_name. zod validation.
- POST `/v1/auth/register` → "Check your email to verify".

`/forgot`:
- Email input → POST `/v1/auth/password-reset/request` → confirm message.

`/reset?token=…`:
- New password + confirm fields → POST `/v1/auth/password-reset/confirm`. On 204, redirect to `/login` with success toast.

`/verify?token=…`:
- POST `/v1/auth/verify-email/confirm` → success page. If user is currently logged in, refresh session.

## How to verify
- Each flow round-trips against the running api (with mailhog catching emails).
- Validation errors surface inline with field-level messages.
- Auth providers without configured client-id are hidden.

## Notes
- All screens follow shadcn/ui form patterns. Keep copy short and direct.
