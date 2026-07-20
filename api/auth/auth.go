// Package auth is the public facade for users-module authentication.
// It re-exports types and functions from internal/auth so that external
// modules can use the JWT verifier, claim mapper, user resolver, and
// auth middleware without accessing the internal package directly.
package auth

import (
	"context"
	"net/http"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/observer"
	coredb "github.com/moduleforge/core-model/db"
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
// The authCfg provides the AdminRole. For the "generic" style, EmailPath and
// RolesPath are set to the fixed "email"/"roles" claim paths used by this
// module's own locally-minted JWTs (flat "email" + "roles" claims); other
// styles leave those fields zeroed as before.
//
// Scope: this facade exists to construct the mapper used by RequireAuth
// middleware to decode locally-minted JWTs (see cmd/server/main.go's
// "generic"-style construction) — it is not a substitute for per-provider
// OIDC claim mapping. Real external-provider mappers are constructed
// directly against internal/auth's (unexported-package) NewClaimMapper by
// internal/auth/oauth.go's initProvider, using each provider's own
// config.Provider.ClaimStyle and unmodified MapperOptions; that path does
// not go through this facade and is unaffected by the "generic"-style
// EmailPath/RolesPath population above.
func NewClaimMapper(style string, authCfg config.AuthConfig) (ClaimMapper, error) {
	return inner.NewClaimMapper(style, buildMapperOptions(style, authCfg))
}

// buildMapperOptions constructs the inner.MapperOptions for the given claim
// style. Factored out of NewClaimMapper so the per-style option values can be
// asserted directly in a unit test, without needing access to the unexported
// concrete mapper types NewClaimMapper's return value hides behind the
// ClaimMapper interface.
func buildMapperOptions(style string, authCfg config.AuthConfig) inner.MapperOptions {
	opts := inner.MapperOptions{
		AdminRole: authCfg.AdminRole,
	}
	if style == "generic" {
		opts.EmailPath = "email"
		opts.RolesPath = "roles"
	}
	return opts
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

// NewStepUpConsumedCache constructs the process-lifetime consumed-JTI cache for
// step-up tokens and starts the background janitor that prunes expired entries.
// The janitor runs until ctx is cancelled. Declared as a provides.services entry
// so the generated composition root constructs the cache and starts the janitor
// without needing an app-level startup hook.
func NewStepUpConsumedCache(ctx context.Context) *sync.Map {
	m := new(sync.Map)
	inner.StartStepUpJanitor(m, ctx.Done())
	return m
}
