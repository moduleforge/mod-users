# Report — Phase 9 (Google SSO) Complete

Date: 2026-04-16
Status: All three tasks merged to `main`. Smoke test pending (see below).

## What was actually shipped

| Task | Commit | Scope |
| --- | --- | --- |
| 9.0 plan | `2332e18` | Phase + 3 task specs, architecture diagram |
| 9.2 GUI impl | `d9ac60c` | Provider buttons, `/auth/oidc/return`, `completeExternalLogin` |
| 9.2 GUI review fixes | `1f71128` | `skipAuthRedirect` opt-out, absolute API URL, backslash guard |
| 9.2 GUI merge | `1ebace2` | |
| 9.1 API impl | `71a03ef` | Provider registry, OAuth start/callback, state cookie, `/v1/auth/providers` |
| 9.1 API review fixes | `b253af1` | Resolver fast path (pre-existing bug), audit trim, sentinel error, case-insensitive ID, etc. |
| 9.1 API merge | `b8e5ae0` | |
| 9.3 compose+env | `c84de85` | docker-compose, Authelia config, `.env.example`, k8s configmap, Cloud Run service |
| 9.3 merge | `c3dfa09` | |

## Architecture locked in

- **OAuth flow lives on the API.** Browser → `GET /v1/auth/oidc/{provider}/start` → 302 to provider → provider redirects to API callback → API exchanges code, verifies id_token, calls existing `UserResolver`, issues local JWT → 302 to GUI `/auth/oidc/return#token=…&return=…`.
- **Generalized provider registry** in config. `google` and `microsoft` are well-known (only CLIENT_ID + CLIENT_SECRET required); Authelia and other providers configured explicitly. Adding Microsoft now is a config-only change.
- **Old `OIDC_*` env vars dropped entirely** (no backwards-compat shim). Everything migrated to `AUTH_PROVIDER_{ID}_*` in compose, k8s configmap, Cloud Run service, `.env.example`.
- **Token handoff via URL fragment** (`#token=<jwt>`). Matches existing localStorage-based auth-context. Cleared from history via `replaceState` before any async.
- **State CSRF via HMAC-SHA256 signed cookie** (path-scoped to `/v1/auth/oidc/`, 5-minute TTL, `Secure` behind trusted proxy, SameSite=Lax). Sentinel error `ErrStateValidation` drives the 400 vs 302 response split.
- **`RequireAuth` accepts local HS256 JWTs only.** Post-Phase-9, authenticated API requests never see a raw provider id_token — those are exchanged for local JWTs at the callback. A regression test pins this.

## Issues uncovered and fixed

**Critical (pre-existing, activated by Phase 9):** `UserResolver.Resolve` had no fast path for locally-issued JWTs. Every authenticated request would fall through to `autoCreate` → UNIQUE-violation 500. Fixed in `b253af1` by adding a local-issuer fast path keyed on user UUID. Covered by new tests in `api/internal/auth/resolver_test.go`.

**GUI-side UX bug:** the shared `api.ts` `request()` helper hard-redirects on any 401. This would have swallowed OAuth error messages at the return page. Fixed with an opt-in `skipAuthRedirect` flag on `api.self.get()`, used only by `completeExternalLogin`.

**Cross-origin provider-start URL:** initial GUI used a relative path, which breaks in the 3000/8080 dev split. Fixed to use the exported `API_BASE_URL` constant from `api.ts`.

## Open items (not blocking merge)

1. **Microsoft client registration** — code supports it via `AUTH_PROVIDER_MICROSOFT_*`. No Azure AD client registered yet. Optional per CLAUDE.md.
2. **Google client registration** — `.env.example` documents the Google Cloud Console setup steps. Actual client creation is a manual one-time ops step.
3. **GUI test harness** — no vitest/jest scaffolding exists in `gui/`. Phase 9.2 code has no unit tests as a result. Consider a standalone testing-infra phase.
4. **Unrelated `gofmt -l` offenders** on `main` (`claims_test.go`, `local_jwt.go`, `password.go`, `principal.go`, `resolver.go`, `handlers/auditlog.go`, `handlers/self.go`) are still present — pre-date Phase 9 and left alone per scope. Worth a one-commit cleanup.
5. **`refreshUser` 401 interaction** — the auth-context's existing `refreshUser` still routes through the shared helper and will trigger a hard redirect on 401; its local `logout()` call becomes redundant. Same category of issue as the return-page one. Not touched — out of scope for Phase 9.

## Smoke test — passes after Phase 9.4 TLS fix

**Phase 9.4 (commit `8721465`) resolved the blocker below.** API now boots cleanly with OIDC discovery, provider list returns the Authelia entry, start endpoint 302s with a correct state cookie. Details preserved below for history.

### Original failure (pre-9.4)

Ran `docker compose up -d` after rebuilding the API image (important: `docker compose up -d` alone reuses the cached pre-Phase-9 image; `docker compose build api` is required first).

**Result:** API crash-loops at startup on OIDC discovery:
```
ERROR oauth init failed error="oauth: provider \"authelia\" discovery:
  Get \"http://authelia:9091/.well-known/openid-configuration\": EOF"
```

Root cause: **Authelia 4.38 serves OIDC discovery over HTTPS only, with a self-signed certificate.** Confirmed by curl from a sibling container:
- `http://authelia:9091/.well-known/openid-configuration` → empty reply (TCP connection dropped)
- `https://authelia:9091/.well-known/openid-configuration` (with `-k`) → `200 OK` with full discovery document

Phase 9.3 compose points the API at `http://authelia:9091`. Even if switched to `https://`, Go's default HTTP client doesn't trust Authelia's self-signed CA, so discovery would still fail.

This is **pre-existing** to Phase 9. Commit `27aa1d6` ("make OIDC non-fatal in local mode") documents the prior workaround — they made discovery failure non-fatal so the rest of the stack could boot. Phase 9.1's spec explicitly switched discovery to fail-fast (correct for prod; re-exposes the local-dev gap).

### What the Phase 9 code does prove

- `api/internal/auth/oauth_test.go` end-to-end test (happy path + nonce/aud/iss tampering negatives) spins up an in-process fake OIDC server over HTTP and exercises the full authorization-code flow. That validates the Phase 9 code path; only the live-Authelia integration is blocked.
- `go test ./...` in `api/`: all green, including the new resolver fast-path, jwt verifier-scope, and handler-level state-cookie tests.
- `docker compose config` renders cleanly with the full `AUTH_PROVIDER_*` block; Google correctly absent when host secret unset.

### Phase 9.4 fix (applied)

Chose the CA-mount approach (no Go code change). Commit `d5b05db`:
- `deploy/local/docker-compose.yml`: bind `./authelia/certs/cert.pem` to `/usr/local/share/ca-certificates/authelia.crt:ro` on the API service; restore `AUTH_PROVIDER_AUTHELIA_ISSUER_URL: https://authelia:9091`.
- `api/entrypoint.sh`: run `update-ca-certificates` (Alpine's standard) before exec'ing the server. Alpine's bundle at `/etc/ssl/certs/ca-certificates.crt` now includes Authelia's self-signed cert; Go's default TLS stack picks it up automatically. Production unaffected — no CAs mounted there, `update-ca-certificates` is a no-op.

Post-fix smoke evidence (from agent report):
- `GET /v1/auth/providers` → `[{"id":"authelia","display_name":"Authelia (local)"}]`
- `GET /v1/auth/oidc/authelia/start?return=/profile` → 302 Location to Authelia's `/api/oidc/authorization` with `client_id`, `nonce`, `redirect_uri`, `state`, `response_type=code`, `scope=openid+email+profile`; `Set-Cookie: oidc_state=…; Path=/v1/auth/oidc/; Max-Age=300; HttpOnly; SameSite=Lax`.

**Known non-blocker**: on first boot the API may lose a startup race against Authelia's TLS bootstrap and crash once before `restart: unless-stopped` recovers it. Pre-existing — missing `depends_on: authelia: condition: service_healthy` on the API service. Separate follow-up if this bothers anyone.

### Manual verification that still works today

The Phase 9 plumbing renders correctly in the GUI:

1. `pnpm -F gui dev` (no API needed for the login page's empty-providers fallback)
2. Visit `http://localhost:3000/auth/login`
3. Confirm: local-auth form renders; "or continue with" divider + buttons absent (providers API returned `[]`, which is the correct behavior when no providers are enabled)

## Not in scope for Phase 9

- Account-merging for duplicate OIDC+local accounts (the `MERGE-V2` marker noted in Phase 4).
- Cookie-based session storage (still localStorage + Bearer).
- Real Google production client.
