# 001 Configured Cookie Base Path

## Purpose and scope
`OIDCStateCookiePath(r)` (`api/internal/auth/oidc_cookie_path.go`, fix 296147f/izI2) derives Path from the request URL, which breaks when a proxy strips the mount prefix (flow-accounts: Caddy `handle_path /manage/*`). Add a configured base path. Followup E75T.

## Requirements
1. New config `USERS_COOKIE_PATH` (base path, e.g. `/manage`); validate shape (leading `/`, no `..`, no control chars, no trailing `/` normalisation surprises).
2. When set, Set-Cookie Path = base + the existing marker suffix; when unset, current request-derived behaviour unchanged.
3. Apply to every cookie site: `api/internal/handlers/auth/oidc.go` (~507, ~521 clearing) and `api/internal/handlers/identities.go` (~739); clear-cookie Path must match set-cookie Path.
4. Docs for the setting incl. the Caddy strip-prefix example.

## Validation
- Tests with and without a strip proxy (request path `/auth/oidc/...` with `USERS_COOKIE_PATH=/manage`, and with `X-Forwarded-Prefix`-style header if the design supports it); unset behaviour regression test.
- `cd api && go test ./...`.

## Status
- Outcome: succeeded (2026-10-10).
- Added `USERS_COOKIE_PATH` (`api/internal/config/cookie_path.go`, `config.go` `Auth.CookiePath`), strictly validated at load. `OIDCStateCookiePath(r, basePath)` in `api/internal/auth/oidc_cookie_path.go` returns base + `/v1/auth/oidc/` when set, else the unchanged request-derived path.
- Applied at all cookie sites: `api/internal/handlers/auth/oidc.go` (set and clear share `newStateCookie`) and `api/internal/handlers/identities.go`. Facade `handlers.NewIdentitiesHandler` gained a trailing `cookiePath string` arg; `moduleforge.module.yaml` passes `field:cfg.Auth.CookiePath`.
- X-Forwarded-Prefix deliberately not supported (untrusted header; config only).
- Docs: `docs/cookie-path.md`, README link, `.env.example`.
- Validation: `go vet ./...` and `go test ./...` pass. Resolves followup E75T; hDB7 (test redirect path) is not addressed.
