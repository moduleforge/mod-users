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

// AnonymousActor holds the entity id of the seeded, zero-authority anonymous
// system actor, resolved once at startup. See inner.AnonymousActor's doc
// comment (api/internal/auth/anonymous_actor.go) for the full anonymous-actor
// / guest-account terminology split.
type AnonymousActor = inner.AnonymousActor

// NewAnonymousActor resolves the anonymous system actor's entity id by slug
// and asserts, at boot, that it holds zero grants — refusing to start if it
// finds any. Matches moduleforge.module.yaml's anonymousActor provides.services
// entry (constructor: auth.NewAnonymousActor, args: [context, infra:pool]).
func NewAnonymousActor(ctx context.Context, pool *pgxpool.Pool) (*AnonymousActor, error) {
	return inner.NewAnonymousActor(ctx, pool)
}

// NewResolveActorOrAnonymous returns opt-in middleware that behaves exactly
// like RequireAuth except on one branch: a request that presents no
// Authorization header at all falls through to the shared anonymous system
// actor instead of being rejected with 401. See
// inner.ResolveActorOrAnonymous's doc comment (api/internal/auth/anonymous_actor.go)
// for the full branch table and the hard preconditions every opting-in route
// group must satisfy (per-IP rate limiting, never composing with requireAuth
// or requireVerifiedEmail, requireOIDCConfirmed outermost, Vary: Authorization,
// and no audit attribution for the anonymous actor).
//
// This is the adapter between the anonymous-actor architecture proposal's §3
// internal signature (inner.ResolveActorOrAnonymous, which takes a bare int64
// entity id) and its §5 manifest wiring (which passes service:anonymousActor,
// a *AnonymousActor holder). moduleforge.module.yaml's resolveActorOrAnonymous
// provides.middleware entry names this function as its constructor; it
// unwraps the holder via anon.EntityID() and delegates to
// inner.ResolveActorOrAnonymous. inner.ResolveActorOrAnonymous keeps its
// plain int64 signature — the unwrapping happens only here, at the facade
// boundary — because that internal function also has callers that already
// hold a bare entity id (see the plain ResolveActorOrAnonymous re-export
// below).
//
// Consuming modules opt in by referencing this middleware by name
// (resolveActorOrAnonymous) from a scope: public route group's middleware:
// list, with requireOIDCConfirmed listed first. It must never be combined
// with requireAuth or requireVerifiedEmail on the same route group.
func NewResolveActorOrAnonymous(verifier *Verifier, mapper ClaimMapper, resolver *UserResolver, anon *AnonymousActor) func(http.Handler) http.Handler {
	if anon == nil {
		panic("auth: NewResolveActorOrAnonymous: anon must not be nil")
	}
	return inner.ResolveActorOrAnonymous(verifier, mapper, resolver, anon.EntityID())
}

// ResolveActorOrAnonymous is a plain re-export of the internal form, matching
// how RequireAuth is re-exported above, for callers (including the dev
// server) that already hold the anonymous actor's entity id rather than the
// *AnonymousActor holder.
func ResolveActorOrAnonymous(verifier *Verifier, mapper ClaimMapper, resolver *UserResolver, anonActorEntityID int64) func(http.Handler) http.Handler {
	return inner.ResolveActorOrAnonymous(verifier, mapper, resolver, anonActorEntityID)
}

// IsAnonymousActor reports whether ctx was populated by
// ResolveActorOrAnonymous's (or NewResolveActorOrAnonymous's) anonymous
// branch — i.e. the actor on ctx is the shared, zero-authority anonymous
// system actor rather than an authenticated principal. Re-exported so
// handlers and observers in other modules can call it without reaching into
// internal/auth.
func IsAnonymousActor(ctx context.Context) bool {
	return inner.IsAnonymousActor(ctx)
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
