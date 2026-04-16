# Phase 9 — Google SSO (OAuth 2.0 Authorization Code Flow)

## Why this phase exists

Phase 3–7 built the claim-normalization and user-resolution halves of OIDC (verify inbound ID token → normalize claims → upsert/link user → mint local JWT). What was never built is the **browser-facing authorization-code flow** that actually obtains the ID token in the first place.

Today, the `/auth/login` GUI page shows only email+password. There is no Google button, no `/v1/auth/oidc/google/start`, no callback handler. Phase 7.2's TODO ticked "Login (local + OIDC)" but shipped only the local half.

This phase closes that gap.

## Scope

- **In scope**: Google Sign-In end-to-end. Generalized provider registry so Microsoft, Authelia, and future providers are config-only.
- **Out of scope**: Microsoft as a required deliverable (CLAUDE.md marks it "optional if difficult"). Framework supports it; we don't promise a Microsoft client registration in v1.
- **Out of scope**: Cookie-based sessions (current auth-context uses localStorage; keep that).
- **Out of scope**: Account-merging when the same email is already associated with a different OIDC account — existing `MERGE-V2` marker applies.

## Architecture

```
Browser                API (chi)                 Google
   │                      │                         │
   │  GET /v1/auth/oidc/google/start?return=/profile│
   ├─────────────────────►│                         │
   │                      │ set signed state cookie │
   │  302 Location: Google authorization URL        │
   │◄─────────────────────┤                         │
   │                                                │
   │  GET accounts.google.com/.../authorize         │
   ├────────────────────────────────────────────────►
   │                                                │
   │  user authenticates, consents                  │
   │◄────────────────────────────────────────────────
   │                                                │
   │  302 Location: <API>/v1/auth/oidc/google/callback?code=…&state=…
   │                      │                         │
   │──────────────────────►│                         │
   │                      │ verify state cookie     │
   │                      │ POST token endpoint     │
   │                      ├────────────────────────►│
   │                      │ id_token, access_token  │
   │                      │◄────────────────────────┤
   │                      │ go-oidc verify id_token │
   │                      │ ClaimMapper → Principal │
   │                      │ UserResolver upsert/link│
   │                      │ IssueLocalJWT           │
   │  302 Location: <GUI>/auth/oidc/return#token=…&return=/profile
   │◄─────────────────────┤
   │                                                │
   │  GUI reads fragment, stores token, redirects   │
   │  to /profile. localStorage. auth-context.      │
```

## Tasks

- **9.1 — API OAuth authorization-code flow** (golang-dev)
  - Provider registry in config
  - OAuth initiate + callback handlers
  - HMAC-signed state cookie
  - Provider discovery endpoint (unauth'd)
  - Drop deprecated `OIDC_*` env vars; Authelia migrates to new scheme
  - Unit tests with mocked token endpoint + test signing key

- **9.2 — GUI provider buttons + return page** (node-dev, parallel with 9.1 against the spec)
  - Fetch providers from `/v1/auth/providers` at login page load
  - Render provider buttons
  - `/auth/oidc/return` page reads fragment, calls auth-context, redirects
  - Remove any stale placeholder code in login page

- **9.3 — Compose + .env + CI wiring** (generic agent, after 9.1 and 9.2 merge)
  - Compose: migrate Authelia env vars; pass through `AUTH_PROVIDER_GOOGLE_*` from host
  - `.env.example`: document Google Cloud Console setup steps as comments
  - CI env: keep Authelia-only in CI (no Google secrets in GHA); test harness uses Authelia provider
  - Smoke test: run compose stack, authenticate via Authelia, land on /profile

## Dependencies

- Depends on: Phases 3–7 (exists, though 7.2 is incomplete for OIDC UI).
- Blocks: nothing downstream in the current plan. Cloud deploy (Phase 8) works independently of which providers are configured.

## Risks

- **Token-in-fragment leakage**: mitigated by HTTPS in prod, short-lived JWT, fragment is never sent to server logs. Acceptable for v1.
- **Breaking Authelia local dev**: explicit migration of env vars in the same task, tested before merge.
- **State cookie domain mismatch** when API and GUI are on different hosts in prod: document the requirement that the state cookie lives on the API origin only; the token travels via URL fragment to the GUI origin.

## Exit criteria

- `docker compose up` then browse to `http://localhost:3000/auth/login` shows an Authelia button.
- Clicking it authenticates through Authelia and lands on `/profile` with a valid session.
- A Google button appears when `AUTH_PROVIDER_GOOGLE_CLIENT_ID` is set on the host, absent when it isn't.
- TODO.md Phase 7.2 is corrected (note: OIDC UI was completed by Phase 9, not 7.2).
- All new code has unit tests; CI green.
