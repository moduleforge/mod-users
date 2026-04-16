# Phase 9, Task 1 — API OAuth 2.0 Authorization Code Flow

**Agent**: golang-dev
**Working directory**: task worktree (`users-module/worktree/phase-09-task-01-api-oauth-flow/api/`)
**Branch**: `phase-09-task-01-api-oauth-flow`

## Context

Read `phase.9.google-sso.md` (this directory) for the end-to-end architecture diagram. This task implements the API half: provider registry, authorization-code handlers, provider discovery endpoint. The GUI half is Task 9.2.

Existing pieces you will reuse as-is (do not duplicate):

- `api/internal/auth/claims.go` and `claims_google.go`, `claims_authelia.go`, etc. — `ClaimMapper` per provider style. Select via provider's configured `claim_style`.
- `api/internal/auth/resolver.go` — `UserResolver.Resolve(ctx, Principal)` upserts + email-links + applies first-user-is-root rule. Call this after verifying the ID token.
- `api/internal/auth/local_jwt.go` — `IssueLocalJWT(user, isAdmin, secret, issuer)` returns the session JWT to hand back to the GUI.
- `github.com/coreos/go-oidc/v3` — already a direct dep; use for ID token verification (JWKS fetched per-provider).
- `golang.org/x/oauth2` — currently indirect; promote to direct dep.

## Acceptance

### 1. Provider registry (`api/internal/config/providers.go` — NEW)

```go
type Provider struct {
    ID           string // "google", "microsoft", "authelia"
    DisplayName  string // "Google"
    IssuerURL    string
    ClientID     string
    ClientSecret string
    ClaimStyle   string // selects ClaimMapper; must match a registered style
    Scopes       []string // default: []string{"openid", "email", "profile"}
}

type ProviderRegistry map[string]Provider
```

- Parse from env: `AUTH_PROVIDER_{ID}_CLIENT_ID`, `AUTH_PROVIDER_{ID}_CLIENT_SECRET`, `AUTH_PROVIDER_{ID}_ISSUER_URL`, `AUTH_PROVIDER_{ID}_CLAIM_STYLE`, `AUTH_PROVIDER_{ID}_DISPLAY_NAME`, `AUTH_PROVIDER_{ID}_SCOPES` (comma-separated).
- A provider is **enabled** iff its `CLIENT_ID` is set. Missing `CLIENT_SECRET` on an enabled provider is a startup error.
- **Built-in defaults for well-known providers** (applied when only the client_id/secret are set):
  - `google`: issuer `https://accounts.google.com`, claim_style `google`, display_name `Google`.
  - `microsoft`: issuer `https://login.microsoftonline.com/common/v2.0`, claim_style `microsoft`, display_name `Microsoft`.
  - `authelia`: no defaults — all fields must be configured explicitly.
  - Any other provider name: all fields required.
- The registry is built by scanning env vars. Enumerate candidate provider IDs from `AUTH_PROVIDERS` (comma list) if set, otherwise auto-discover by scanning env for `AUTH_PROVIDER_*_CLIENT_ID`.
- Add `Registry ProviderRegistry` to `Config`. **Remove** the old `OIDCConfig` struct and all `OIDC_*` env parsing — this is a clean break, no shim.

### 2. OAuth orchestration (`api/internal/auth/oauth.go` — NEW)

```go
type OAuth struct {
    Registry     config.ProviderRegistry
    Verifiers    map[string]*oidc.IDTokenVerifier // one per provider, init lazily
    OAuthConfigs map[string]*oauth2.Config        // one per provider
    StateSigner  StateSigner                      // HMAC, see below
    RedirectBase string                           // e.g. "http://localhost:8080"
    FrontendReturnURL string                      // e.g. "http://localhost:3000/auth/oidc/return"
}

func NewOAuth(ctx context.Context, cfg config.Config) (*OAuth, error)

// AuthorizeURL returns (url, stateToken) for the browser redirect.
func (o *OAuth) AuthorizeURL(providerID, returnPath string) (string, string, error)

// Exchange handles the callback: verifies state, exchanges code, verifies id_token,
// returns the normalized Principal.
func (o *OAuth) Exchange(ctx context.Context, providerID, code, rawState, cookieState string) (auth.Principal, error)
```

- State/nonce: generate a random 32-byte value; include it as the OAuth `state` parameter. Also put it (plus provider id, return path, exp timestamp) in a cookie. Compare on callback. Rejects mismatch → 400.
- State cookie: `oidc_state`, HttpOnly, Secure (when `TLS_ENABLED` or behind proxy header), SameSite=Lax, Path=`/v1/auth/oidc/`, Max-Age=300.
- Nonce: include in the authorization URL; verify presence and exact match in the returned id_token's `nonce` claim.
- Return-path validation: MUST be a path only (starts with `/`, no scheme/host). Reject otherwise — prevents open-redirect. Default to `/` if empty.
- Token exchange: use `oauth2.Config.Exchange(ctx, code)` with the per-provider config. Extract `id_token` from `tok.Extra("id_token")`. Error if missing.
- ID token verification: use the per-provider `oidc.IDTokenVerifier` initialized from the issuer URL's discovery document. Verify audience = client_id, issuer matches, nonce matches. Extract claims, run the registered `ClaimMapper` for this provider's `claim_style`.

### 3. HMAC state signer (`api/internal/auth/oauth_state.go` — NEW)

- Key material: re-use `JWTSecret` from config (32-byte HMAC-SHA256 key).
- Payload (JSON): `{"p":"google","r":"/profile","n":"<base64 nonce>","e":<unix exp>}`.
- Wire format: `<base64url(payload)>.<base64url(hmac)>`.
- Expose `Sign(payload) -> string`, `Verify(token) -> (payload, error)`. Reject expired.

### 4. Handlers (`api/internal/handlers/auth/oidc.go` — NEW)

Three endpoints, registered on the **public** router (not behind `RequireAuth`):

**`GET /v1/auth/providers`** — returns enabled providers:
```json
[{"id": "google", "display_name": "Google"}, {"id": "authelia", "display_name": "Authelia"}]
```
Never exposes client_secret, issuer_url, or scopes.

**`GET /v1/auth/oidc/{provider}/start`**
- Query param `return` (optional, defaults to `/`).
- Returns 404 if provider unknown; 503 if registry initialization failed.
- Calls `OAuth.AuthorizeURL`, sets state cookie, 302 to the authorization URL.

**`GET /v1/auth/oidc/{provider}/callback`**
- Query params: `code`, `state`, `error` (if user cancels).
- On `error`: 302 to `${FrontendReturnURL}?error=<url-encoded>`.
- Read state cookie; delete it (set Max-Age=0 on response).
- Call `OAuth.Exchange` → Principal.
- Call `UserResolver.Resolve(ctx, principal)` → `*UserContext`.
- Call `IssueLocalJWT(uc.User, uc.IsAdmin, cfg.JWTSecret, cfg.JWTIssuer)` → token.
- Decode state to recover return path.
- 302 to `${FrontendReturnURL}#token=<jwt>&return=<url-encoded-return-path>`.
- On any error after state validation, 302 to `${FrontendReturnURL}?error=<message>` with a generic message (don't leak internals).
- Write an audit row: `op='login', resource='users', resource_id=<user id>, actor=<user id>, meta={provider: "google", linked: bool}`.

### 5. Route wiring (`api/cmd/server/main.go` — UPDATE)

- Build `OAuth` during startup; fail fast if any enabled provider's discovery fetch fails.
- Mount the three handlers on the public chi router.
- Existing `RequireAuth` middleware on `/v1/*` routes stays as-is — these new routes are siblings, not behind it.

### 6. Config additions (`api/internal/config/config.go` — UPDATE)

- `FrontendReturnURL string` (env `AUTH_FRONTEND_RETURN_URL`, required if any provider enabled; example `http://localhost:3000/auth/oidc/return`).
- `OAuthRedirectBaseURL string` (env `AUTH_OAUTH_REDIRECT_BASE_URL`, required if any provider enabled; example `http://localhost:8080`). The callback path is `/v1/auth/oidc/{provider}/callback` appended to this base.
- Remove `OIDCConfig` and its env-parsing.

### 7. OpenAPI spec (`api/openapi.yaml` — UPDATE)

Document:
- `GET /v1/auth/providers` → `200 application/json` with array of `{id, display_name}`.
- `GET /v1/auth/oidc/{provider}/start` → `302` to external URL; `404` unknown provider.
- `GET /v1/auth/oidc/{provider}/callback` → `302`; `400` on state mismatch.

## Tests (`api/internal/auth/oauth_test.go`, `api/internal/handlers/auth/oidc_test.go`)

- State signer: sign → verify roundtrip; tampered MAC rejected; expired rejected.
- Return-path validation: absolute URLs rejected; relative paths accepted.
- Callback happy path: spin up an in-process fake OIDC server (httptest) with `/.well-known/openid-configuration`, `/jwks`, `/token` endpoints. Sign a test id_token with a test RSA key. Assert:
  - State cookie verified.
  - Token exchange succeeds.
  - ID token verified.
  - ClaimMapper applied.
  - `UserResolver.Resolve` called with expected Principal.
  - Response is 302 to `{FrontendReturnURL}#token=...&return=/profile`.
- Callback failure paths: missing state cookie → 400; state/cookie mismatch → 400; provider returned `error=access_denied` → 302 to frontend with `?error=access_denied`; token endpoint returns 500 → 302 with generic error.
- Provider-list handler: returns enabled providers; never leaks secrets.

## How to verify end-to-end (manual, after 9.2 and 9.3)

1. `cd users-module/deploy/local && docker compose up -d`
2. Browse `http://localhost:3000/auth/login` — "Sign in with Authelia" button visible.
3. Click → Authelia login → redirects back → lands on `/profile` as the Authelia user.
4. DB check: one row in `users` with `auth_issuer` and `auth_id` populated.

## Non-goals for this task

- Do not build Microsoft or Authelia UI buttons — Task 9.2 owns GUI.
- Do not modify `ClaimMapper` implementations — they're correct as-is.
- Do not change the session storage model (localStorage + Bearer JWT).

## Notes

- The `oidc_state` cookie MUST set `Path=/v1/auth/oidc/` so it's sent on callback but not on unrelated requests.
- If `AUTH_PROVIDERS` is unset AND no `AUTH_PROVIDER_*_CLIENT_ID` env vars are found, the API starts normally with zero providers — `GET /v1/auth/providers` returns `[]`. Local-auth still works. This is the "providers intentionally disabled" state.
- Report back: any surprise in the `UserResolver.Resolve` signature, any `ClaimMapper` behavior that diverges from what you'd expect, any case where existing tests had to change (and why).

## Stop and ask if

- The existing `UserResolver.Resolve` logic doesn't match the description in Phase 4 Task 4 (email-linking, first-user-is-root) — flag it and ask before diverging.
- You get 5+ failures on the fake OIDC test server setup — it's fiddly; ask for help rather than hacking around it.
