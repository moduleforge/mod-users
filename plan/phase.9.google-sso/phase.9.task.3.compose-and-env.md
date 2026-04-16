# Phase 9, Task 3 — Compose + .env + CI wiring

**Agent**: generic (no specialized expertise required; this is YAML + shell + docs)
**Working directory**: task worktree (`users-module/worktree/phase-09-task-03-compose-env/`)
**Branch**: `phase-09-task-03-compose-env`
**Depends on**: 9.1 and 9.2 merged.

## Context

Task 9.1 migrated API config from `OIDC_*` env vars to `AUTH_PROVIDER_{NAME}_*`. Compose and .env must follow. CI must still work without real Google credentials.

## Acceptance

### 1. `deploy/local/docker-compose.yml` — UPDATE

For the `api` service:

- Remove: any `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_CLAIM_STYLE`, `OIDC_ADMIN_ROLE` entries.
- Add (for Authelia, the default local provider):
  ```yaml
  AUTH_PROVIDERS: authelia,google
  AUTH_PROVIDER_AUTHELIA_DISPLAY_NAME: Authelia (local)
  AUTH_PROVIDER_AUTHELIA_ISSUER_URL: http://authelia:9091
  AUTH_PROVIDER_AUTHELIA_CLIENT_ID: users-module
  AUTH_PROVIDER_AUTHELIA_CLIENT_SECRET: ${AUTH_PROVIDER_AUTHELIA_CLIENT_SECRET:-local-authelia-secret}
  AUTH_PROVIDER_AUTHELIA_CLAIM_STYLE: authelia
  AUTH_PROVIDER_GOOGLE_CLIENT_ID: ${AUTH_PROVIDER_GOOGLE_CLIENT_ID:-}
  AUTH_PROVIDER_GOOGLE_CLIENT_SECRET: ${AUTH_PROVIDER_GOOGLE_CLIENT_SECRET:-}
  AUTH_OAUTH_REDIRECT_BASE_URL: http://localhost:8080
  AUTH_FRONTEND_RETURN_URL: http://localhost:3000/auth/oidc/return
  ```
- Note: Google provider is only "enabled" at runtime when the host has `AUTH_PROVIDER_GOOGLE_CLIENT_ID` set (per Task 9.1 semantics). When empty, compose passes empty string, the registry skips it, and `GET /v1/auth/providers` returns only Authelia.

For the `authelia` service — confirm its `configuration.yml` has `users-module` registered as a client with redirect URI `http://localhost:8080/v1/auth/oidc/authelia/callback`. Update if it currently uses the old single-path `/v1/auth/oidc/callback`.

### 2. `.env.example` — UPDATE

Replace any old `OIDC_*` lines with:

```bash
# --- Auth providers ---
# Enable providers by setting their CLIENT_ID/CLIENT_SECRET below.
# Local dev uses Authelia (automatic via docker-compose).
# To enable Google in local dev, create a Google Cloud OAuth 2.0 Client
# (https://console.cloud.google.com/apis/credentials) with:
#   - Application type: Web application
#   - Authorized redirect URI: http://localhost:8080/v1/auth/oidc/google/callback
# Then set:
#
# AUTH_PROVIDER_GOOGLE_CLIENT_ID=...apps.googleusercontent.com
# AUTH_PROVIDER_GOOGLE_CLIENT_SECRET=...
#
# Microsoft: create an Azure AD app registration with redirect URI
# http://localhost:8080/v1/auth/oidc/microsoft/callback, then set
# AUTH_PROVIDER_MICROSOFT_CLIENT_ID and AUTH_PROVIDER_MICROSOFT_CLIENT_SECRET.

# URLs used by the OAuth flow (only needed if any provider is enabled).
AUTH_OAUTH_REDIRECT_BASE_URL=http://localhost:8080
AUTH_FRONTEND_RETURN_URL=http://localhost:3000/auth/oidc/return
```

### 3. CI (`.github/workflows/*.yml`) — UPDATE

- The existing CI already runs the API and GUI tests. No Google credentials in CI.
- Verify: integration tests that previously assumed `OIDC_*` env vars are updated to the new names, OR use the fake in-process OIDC server from Task 9.1 (preferred — no external dep).
- If CI had a step that exercised Authelia, confirm it still passes with the new env var names.

### 4. Smoke test — manual gate before marking phase complete

Run this from a clean checkout on a machine with `docker` and `docker compose`:

```bash
cd users-module/deploy/local
docker compose up -d
# wait for healthchecks
open http://localhost:3000/auth/login
# click "Authelia (local)", complete login as seed user
# expect: /profile loads, header shows username
```

Document the result in a `report.9.smoke-test.md` at `users-module/plan/`.

### 5. TODO.md correction — UPDATE

- Add Phase 9 entry (Google SSO) with task checkboxes.
- Add a note on Phase 7.2: "OIDC UI originally stubbed; completed in Phase 9."

## Non-goals

- Do not add production OAuth client IDs — those are injected per-environment at deploy time.
- Do not rework CI structure; just rename env vars if they appear.

## Stop and ask if

- Authelia's `configuration.yml` needs structural changes to support the new redirect URI path — ask before rewriting Authelia config.
- CI has Google secrets in a vault already (unlikely; document if you find them) — don't remove, ask.
