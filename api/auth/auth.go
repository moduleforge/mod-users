// Package auth is the public facade for users-module authentication. It
// re-exports types and functions from internal/auth so that external modules
// and generated composition roots can authenticate requests, read the
// resolved caller, and verify or issue this module's tokens without
// accessing the internal package directly.
//
// Access tokens are EdDSA-signed carriers for a server-side session ("sid")
// or a non-interactive credential ("cid"); the authentication middleware
// re-checks the session or credential and the acting profile on every
// request and installs both an Identity and the module-agnostic
// opctx.AuthContext on the request context.
package auth

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	coreAuthz "github.com/moduleforge/core-api/authz"
	usersdb "github.com/moduleforge/mod-users/model/db"

	"github.com/moduleforge/mod-users/api/config"
	inner "github.com/moduleforge/mod-users/api/internal/auth"
)

// Identity is the resolved caller, stored on the request context by the
// middleware alongside opctx.AuthContext (which carries the same ids for
// module-agnostic code).
type Identity = inner.Identity

// IdentityFromContext returns the Identity the authentication middleware
// stored on ctx; nil, false for unauthenticated and anonymous contexts.
func IdentityFromContext(ctx context.Context) (*Identity, bool) {
	return inner.IdentityFromContext(ctx)
}

// Authenticator authenticates bearer access tokens with per-request
// liveness (one database round trip per request).
type Authenticator = inner.Authenticator

// NewAuthenticator builds an Authenticator over pool, verifier (normally
// NewLocalVerifier's) and the auth configuration (accepted audiences).
func NewAuthenticator(pool *pgxpool.Pool, verifier *Verifier, cfg config.AuthConfig) *Authenticator {
	return inner.NewAuthenticator(pool, verifier, cfg)
}

// NewAuthenticatorWithAppGate is NewAuthenticator plus the authorizer that
// evaluates the invite-only app gate on every request: for a session whose app
// has apps.invite_only, the request is live only while
// Authorize(ctx-with-acting-profile, "login", &app.entity_id) allows it
// (otherwise 401 users.profile_unavailable, lock reason app_login_not_granted).
// Without an authorizer every invite-only app is refused. The module manifest
// constructs the authenticator through it; NewAuthenticator plus
// Authenticator.SetAppGate is the equivalent for hand-wired hosts.
func NewAuthenticatorWithAppGate(pool *pgxpool.Pool, verifier *Verifier, cfg config.AuthConfig, az coreAuthz.Authorizer) *Authenticator {
	return inner.NewAuthenticatorWithAppGate(pool, verifier, cfg, az)
}

// Errors returned by Authenticator.Authenticate. ErrNotLive wraps the
// liveness status (HTTP 401 users.session_revoked, or
// users.profile_unavailable with the lock reason in details).
var (
	ErrNoAuthHeader = inner.ErrNoAuthHeader
	ErrInvalidToken = inner.ErrInvalidToken
	ErrNotLive      = inner.ErrNotLive
)

// NewRequireAuth returns middleware that answers 401 unless the bearer
// token's session or credential and profile are live, and otherwise installs
// the Identity and opctx.AuthContext.
func NewRequireAuth(a *Authenticator) func(http.Handler) http.Handler {
	return inner.NewRequireAuth(a)
}

// NewResolveOrAnonymous returns opt-in middleware that behaves like
// NewRequireAuth except that a request with no Authorization header at all
// proceeds as the shared anonymous system actor (AuthContext with Anonymous
// set, no Identity). A present but bad credential is still a 401. See
// internal/auth's NewResolveOrAnonymous for the hard preconditions every
// opting-in route group must satisfy (per-IP rate limiting, never composing
// with requireAuth or requireVerifiedEmail, requireOIDCConfirmed outermost,
// Vary: Authorization, no audit attribution for the anonymous actor).
func NewResolveOrAnonymous(a *Authenticator, anon *AnonymousActor) func(http.Handler) http.Handler {
	return inner.NewResolveOrAnonymous(a, anon)
}

// NewRequireVerifiedEmail returns middleware that answers 403 with the
// users.email_unverified action unless the caller's user holds a verified
// email identifier.
func NewRequireVerifiedEmail() func(http.Handler) http.Handler {
	return inner.NewRequireVerifiedEmail()
}

// RequireOIDCConfirmed returns middleware that gates all routes behind the
// OIDC boot-state check.
func RequireOIDCConfirmed(statusFn func() config.BootState) func(http.Handler) http.Handler {
	return inner.RequireOIDCConfirmed(statusFn)
}

// AnonymousActor holds the ids of mod-core's seeded anonymous system actor
// and its personal profile, resolved once at startup.
type AnonymousActor = inner.AnonymousActor

// NewAnonymousActor resolves the anonymous system actor by slug and asserts,
// at boot, that it holds zero grants — refusing to start if it holds any.
func NewAnonymousActor(ctx context.Context, pool *pgxpool.Pool) (*AnonymousActor, error) {
	return inner.NewAnonymousActor(ctx, pool)
}

// IsAnonymousActor reports whether ctx carries the anonymous system actor's
// AuthContext; it delegates to opctx.Auth(ctx).Anonymous.
func IsAnonymousActor(ctx context.Context) bool {
	return inner.IsAnonymousActor(ctx)
}

// Verifier validates JWTs: local EdDSA tokens and, when configured, external
// OIDC provider tokens.
type Verifier = inner.Verifier

// VerifierOption configures NewVerifierWithOptions.
type VerifierOption = inner.VerifierOption

// NewLocalVerifier builds the local-only Verifier for the local-auth
// configuration (EdDSA only: the signing key's public half plus any
// JWT_VERIFY_PUBLIC_KEYS).
func NewLocalVerifier(ctx context.Context, lc config.LocalAuthConfig) (*Verifier, error) {
	return inner.NewLocalVerifier(ctx, lc)
}

// NewVerifierWithOptions constructs a JWT verifier from options, for
// external identity providers and offline verifiers of this module's tokens.
// Local tokens are accepted only as EdDSA against the configured public keys.
func NewVerifierWithOptions(ctx context.Context, opts ...VerifierOption) (*Verifier, error) {
	return inner.NewVerifierWithOptions(ctx, opts...)
}

// WithOIDC enables verification of OIDC provider tokens from issuerURL.
func WithOIDC(issuerURL, clientID string) VerifierOption { return inner.WithOIDC(issuerURL, clientID) }

// WithInsecureSkipTLSVerify disables TLS certificate verification for OIDC
// discovery and JWKS fetches. LOCAL DEVELOPMENT ONLY; never enable in
// production.
func WithInsecureSkipTLSVerify() VerifierOption { return inner.WithInsecureSkipTLSVerify() }

// WithLocalIssuer sets the "iss" value local tokens must carry.
func WithLocalIssuer(issuer string) VerifierOption { return inner.WithLocalIssuer(issuer) }

// WithEd25519PublicKeys enables local EdDSA tokens verified against keys
// selected by the token "kid" header (empty kid = tokens without a kid).
// All keys are valid concurrently, enabling rotation.
func WithEd25519PublicKeys(keys map[string]ed25519.PublicKey) VerifierOption {
	return inner.WithEd25519PublicKeys(keys)
}

// WithEd25519PublicKey enables local EdDSA tokens verified against a single
// key, used for tokens without a "kid" header.
func WithEd25519PublicKey(key ed25519.PublicKey) VerifierOption {
	return inner.WithEd25519PublicKey(key)
}

// Signer signs this module's EdDSA tokens; see NewSigner.
type Signer = inner.Signer

// NewSigner builds the Signer for the local-auth configuration (the Ed25519
// signing key and the optional JWT_SIGNING_KID).
func NewSigner(lc config.LocalAuthConfig) (*Signer, error) {
	return inner.NewSigner(lc)
}

// IssueAudienceToken signs an audience-bound token for an offline verifier
// (iss, sub = principal uuid, aud, iat, exp = iat + ttl, plus extra claims,
// which may not override the reserved ones). It is never accepted as a bearer
// token by this module.
func IssueAudienceToken(s *Signer, issuer string, principalUUID uuid.UUID, audience string,
	ttl time.Duration, extra map[string]any,
) (string, error) {
	return inner.IssueAudienceToken(s, issuer, principalUUID, audience, ttl, extra)
}

// HashPassword hashes a plaintext password using Argon2id.
func HashPassword(plain string) (string, error) {
	return inner.HashPassword(plain)
}

// OAuth is the OIDC provider orchestrator (browser authorization-code flow).
type OAuth = inner.OAuth

// NewOAuth constructs the OAuth orchestrator for the configured providers.
func NewOAuth(ctx context.Context, cfg *config.Config) (*OAuth, error) {
	return inner.NewOAuth(ctx, cfg)
}

// OAuthStateKey returns the HMAC key for the OAuth state signer: the
// explicit JWT_SECRET (lc.JWTSecret) when set, otherwise a key derived from
// the Ed25519 signing key with HKDF-SHA256. The result is secret; never log
// it.
func OAuthStateKey(lc config.LocalAuthConfig) ([]byte, error) {
	return inner.OAuthStateKey(lc)
}

// OIDCResolver decides what a verified external OIDC identity maps to. It
// resolves by (issuer, subject) credential, never by email.
type OIDCResolver = inner.OIDCResolver

// NewOIDCResolver constructs an OIDCResolver over the module's queries.
func NewOIDCResolver(q usersdb.Querier) *OIDCResolver {
	return inner.NewOIDCResolver(q)
}
