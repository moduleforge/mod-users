# Phase 3, Task 2 — ClaimMapper interface + provider implementations

## Context
OIDC providers ship claims in fundamentally different shapes. A single env var like `OIDC_ADMIN_CLAIM` cannot work; we need a per-provider normalization layer that exposes a uniform `Principal` to the rest of the api.

## Acceptance

`api/internal/auth/principal.go`:
```go
type Principal struct {
    Subject string   // OIDC sub
    Issuer  string   // OIDC iss
    Email   string
    Roles   []string // already-normalized roles, lowercased
}
```

`api/internal/auth/claims.go`:
```go
type ClaimMapper interface {
    Map(rawClaims map[string]any) (Principal, error)
}

func NewClaimMapper(style string, opts MapperOptions) (ClaimMapper, error) {
    // style ∈ {"google","microsoft","authelia","keycloak","cognito","auth0","firebase","generic"}
    // generic accepts JSONPath expressions in opts (RolesPath, EmailPath)
}
```

Provider files (one per style):
- `claims_google.go` — `roles` rarely present; default `Roles=[]`. Email from `email`. Uses `hd` (hosted domain) only as note.
- `claims_microsoft.go` — Roles from `roles` claim or `wids` (Azure AD admin GUIDs); document a small mapping table for common admin GUIDs (e.g., Global Admin → "admin").
- `claims_authelia.go` — Flat `groups` array; map group `admins` → role `admin` (configurable via `OIDC_ADMIN_ROLE`).
- `claims_keycloak.go` — Nested `realm_access.roles`.
- `claims_cognito.go` — `cognito:groups` array.
- `claims_auth0.go` — Custom namespace claim, configurable via `OIDC_ROLES_NAMESPACE` (e.g., `https://example.com/roles`).
- `claims_firebase.go` — Custom claim `roles` or `admin: true` boolean.
- `claims_generic.go` — JSONPath-driven for one-off providers.

Each mapper:
- Returns descriptive error if required claims missing (`sub`, `iss`).
- Lowercases roles for case-insensitive matching.
- Always populates `Subject`, `Issuer`. Email may be empty (rare).

Selected by `Config.OIDCClaimStyle`. `MapperOptions` carries `AdminRole`, `RolesNamespace`, `RolesPath` (generic), `EmailPath` (generic).

## How to verify
- Table-driven test in `api/internal/auth/claims_test.go` covering each provider with a representative raw claim payload.
- Tests assert `Principal.Roles` contains "admin" when input has the provider-specific admin marker.

## Notes
- This is the seam that makes "pluggable OIDC" real. Do NOT pollute downstream code with provider-specific knowledge.
- `OIDC_ADMIN_ROLE` is the post-normalization role string that grants admin scope (default: `"admin"`).
