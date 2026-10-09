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
