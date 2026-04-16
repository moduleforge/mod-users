# Phase 7, Task 1 — App shell + auth context + OIDC client

## Acceptance
- App Router base layout with header (logo, app switcher, user menu) and content slot.
- `lib/auth.ts`:
  - `getSession()` server-side using `iron-session` (signed cookie).
  - `requireSession()` redirects to `/login` if missing.
  - Client-side `useSession()` hook.
- `lib/api.ts` typed fetch wrapper:
  - Reads token from session, sends `Authorization: Bearer …`.
  - Reads selected app uuid from cookie, sends `X-App`.
  - On 401 → clears session, redirect to `/login?next=…`.
  - On 410 (archived app) → clears app cookie, redirect to `/profile?msg=app_archived`.
- OIDC code flow (`openid-client`):
  - `GET /api/auth/oidc/start?provider=…` builds authorization URL, sets PKCE + nonce in session.
  - `GET /api/auth/oidc/callback` exchanges code, calls api `POST /v1/auth/oidc/exchange` (NEW endpoint — see below) which returns a local JWT, then sets session cookie.
- New api endpoint backstop: `POST /v1/auth/oidc/exchange` accepts a verified ID token and mints a local-issuer JWT mirroring its claims. Document this in Phase 4 follow-up notes.
- Loading and error boundaries in app router.

## How to verify
- `pnpm dev` → app loads.
- Hitting `/profile` while signed-out redirects to `/login`.
- Selecting Authelia from the OIDC provider list completes the round-trip and lands on `/profile`.

## Notes
- The `oidc/exchange` endpoint exists because the GUI never holds an OIDC ID token directly — sessions hold our local JWT for uniformity.
