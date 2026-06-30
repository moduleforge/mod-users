// Package auth is the public facade for users-module authentication.
// It re-exports types and functions from internal/auth so that external
// modules can use the JWT verifier, claim mapper, user resolver, and
// auth middleware without accessing the internal package directly.
package auth

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	coredb "github.com/moduleforge/core-model/db"
	"github.com/moduleforge/core-api/observer"
	usersdb "github.com/moduleforge/mod-users/model/db"

	"github.com/moduleforge/mod-users/api/config"
	inner "github.com/moduleforge/mod-users/api/internal/auth"
)

// Type aliases — interchangeable with the internal types.
type Verifier = inner.Verifier
type UserResolver = inner.UserResolver
type ClaimMapper = inner.ClaimMapper
type MapperOptions = inner.MapperOptions
type OAuth = inner.OAuth
type Principal = inner.Principal
type UserContext = inner.UserContext

// NewVerifier constructs a JWT verifier that handles both OIDC and local tokens.
func NewVerifier(ctx context.Context, issuerURL, clientID, jwtSecret, localIssuer string) (*Verifier, error) {
	return inner.NewVerifier(ctx, issuerURL, clientID, jwtSecret, localIssuer)
}

// NewClaimMapper returns a ClaimMapper for the named OIDC claim style.
// The authCfg provides the AdminRole; other MapperOptions fields are zeroed.
func NewClaimMapper(style string, authCfg config.AuthConfig) (ClaimMapper, error) {
	return inner.NewClaimMapper(style, inner.MapperOptions{
		AdminRole: authCfg.AdminRole,
	})
}

// NewUserResolver constructs a UserResolver that maps JWT Principals to internal
// UserAccount / entity records.
func NewUserResolver(
	pool *pgxpool.Pool,
	queries *usersdb.Queries,
	coreQ *coredb.Queries,
	adminRole, localIssuer string,
	obs *observer.ObserverGroup,
) *UserResolver {
	return inner.NewUserResolver(pool, queries, coreQ, adminRole, localIssuer, obs)
}

// NewOAuth constructs an OAuth orchestrator that handles OIDC provider
// onboarding and discovery.
func NewOAuth(ctx context.Context, cfg *config.Config) (*OAuth, error) {
	return inner.NewOAuth(ctx, cfg)
}

// RequireAuth returns middleware that validates the Bearer token and sets the
// Principal in the request context.
func RequireAuth(verifier *Verifier, mapper ClaimMapper, resolver *UserResolver) func(http.Handler) http.Handler {
	return inner.RequireAuth(verifier, mapper, resolver)
}

// RequireVerifiedEmail is middleware that gates routes to accounts with
// completed email verification. It implements http.Handler directly.
func RequireVerifiedEmail(next http.Handler) http.Handler {
	return inner.RequireVerifiedEmail(next)
}

// NewRequireVerifiedEmail returns RequireVerifiedEmail as a chi.Middleware value.
// Used by generated wiring which calls constructors with zero args.
func NewRequireVerifiedEmail() func(http.Handler) http.Handler {
	return RequireVerifiedEmail
}

// RequireOIDCConfirmed returns middleware that gates all routes behind the
// OIDC boot-state check.
func RequireOIDCConfirmed(statusFn func() config.BootState) func(http.Handler) http.Handler {
	return inner.RequireOIDCConfirmed(statusFn)
}

// HashPassword hashes a plaintext password using Argon2id.
func HashPassword(plain string) (string, error) {
	return inner.HashPassword(plain)
}

