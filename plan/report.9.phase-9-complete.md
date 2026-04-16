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

## Smoke test

Manual browser test not yet executed. Programmatic verification plan:

1. `cd users-module/deploy/local && docker compose up -d`
2. Wait for API `/healthz` and GUI root to respond 200.
3. `curl http://localhost:8080/v1/auth/providers` — expect `[{"id":"authelia","display_name":"Authelia (local)"}]`. Google must be absent (no `AUTH_PROVIDER_GOOGLE_CLIENT_ID` on host).
4. `curl -sI 'http://localhost:8080/v1/auth/oidc/authelia/start?return=/profile'` — expect `302` to Authelia authorize endpoint, with a `Set-Cookie: oidc_state=…; Path=/v1/auth/oidc/; HttpOnly; SameSite=Lax` header.
5. Visit `http://localhost:3000/auth/login` in a browser — expect an "Authelia (local)" button. Click → Authelia login → lands on `/profile`.

Steps 1–4 can be scripted; step 5 needs a human.

## Not in scope for Phase 9

- Account-merging for duplicate OIDC+local accounts (the `MERGE-V2` marker noted in Phase 4).
- Cookie-based session storage (still localStorage + Bearer).
- Real Google production client.
