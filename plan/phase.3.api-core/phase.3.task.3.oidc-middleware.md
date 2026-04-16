# Phase 3, Task 3 — OIDC middleware, Principal-on-context, role mapping

## Context
The middleware layer turns an inbound `Authorization: Bearer <jwt>` into a `*UserContext` for handlers. It also resolves (or upserts) the `users` row.

## Acceptance

`api/internal/auth/jwt.go`:
- `Verifier` wraps `coreos/go-oidc/v3` `IDTokenVerifier`. Initialized once at startup from `Config.OIDCIssuerURL`. Caches JWKS.
- For local-auth JWTs (Phase 4) — accepts a second verifier keyed by the local issuer + HS256 secret.
- `Verify(ctx, raw) (claims map[string]any, err error)`.

`api/internal/auth/middleware.go`:
- `RequireAuth(verifier, mapper, resolver UserResolver) func(http.Handler) http.Handler`.
- Steps per request:
  1. Read `Authorization: Bearer …`. 401 if missing/malformed.
  2. `verifier.Verify(ctx, token)` → raw claims. 401 if invalid/expired.
  3. `mapper.Map(claims)` → Principal. 500 if mapper errors (config issue, not client).
  4. `resolver.Resolve(ctx, principal)` → `*UserContext`. Auto-creates `users` row on first sight (with new entity + natural_person leaf, given_name = principal email local-part). First-ever user gets `is_admin = TRUE` (per CLAUDE.md "first account is root").
  5. Stash `*UserContext` on context via `WithUserContext(ctx, uc)`. Add `user_uuid` to slog request log.

`api/internal/auth/principal.go` additions:
```go
type UserContext struct {
    User         *modelusers.User
    IsAdmin      bool             // user.is_admin OR principal.Roles contains AdminRole
    AssumedUser  *modelusers.User // non-nil while admin is assuming
    AppID        int64            // resolved app context (default_app_id or X-App)
    AppRoles     []string         // from apps_users.roles for AppID
}

func FromContext(ctx context.Context) (*UserContext, bool)
func WithUserContext(ctx context.Context, uc *UserContext) context.Context
```

- `RequireAdmin` middleware checks `uc.IsAdmin`; 403 otherwise.
- `WithAppContext` middleware (used after `RequireAuth`) reads `X-App: <uuid>` header, falls back to `user.default_app_id`. 400 if user has neither.

## How to verify
- Unit tests with a fake verifier and fake resolver covering:
  - Missing header → 401
  - Invalid token → 401
  - Valid token, new user → user row created, `is_admin=true` if first user
  - Valid token, second user → `is_admin=false`
  - Admin role in claims → `IsAdmin=true` even if DB row says false
- Integration smoke (after Task 3.4): `curl -H "Authorization: Bearer <authelia token>" /v1/self` returns 200 with the user JSON.

## Notes
- Auto-create on first-sight is intentional: this is how OIDC accounts come into existence. For local auth (Phase 4) a separate `POST /v1/auth/register` flow exists.
- "First user is root" is a one-shot rule: check `COUNT(*) FROM users` inside the resolver's transaction.
