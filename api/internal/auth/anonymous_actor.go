package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/moduleforge/core-api/opctx"
)

// IsAnonymousActor reports whether ctx carries the shared, zero-authority
// anonymous system actor's AuthContext (installed by NewResolveOrAnonymous for
// a request without an Authorization header). It delegates to
// opctx.Auth(ctx).Anonymous, so it agrees with every other module reading the
// same AuthContext.
//
// It is false for every authenticated request, including a guest user's, and
// for any context without an AuthContext. A guest user is a real principal
// with an anon credential, a session and a token; the anonymous system actor
// has no credential, no session and owns nothing.
func IsAnonymousActor(ctx context.Context) bool {
	ac, ok := opctx.Auth(ctx)
	return ok && ac.Anonymous
}

// withAnonymousActor returns ctx carrying the anonymous system actor's
// AuthContext: its principal and personal profile, Anonymous set, and no
// session, credential, assurance or Identity.
func withAnonymousActor(ctx context.Context, anon *AnonymousActor) context.Context {
	return opctx.WithAuthContext(ctx, opctx.AuthContext{
		PrincipalID: anon.PrincipalID(),
		ProfileID:   anon.ProfileID(),
		Anonymous:   true,
	})
}

// NewResolveOrAnonymous returns opt-in middleware that behaves exactly like
// NewRequireAuth except on one branch: a request that presents no
// Authorization header at all proceeds as the shared anonymous system actor
// instead of being rejected with 401.
//
//	Authenticate result                  behavior
//	-----------------------------------  ------------------------------------
//	success                              identical to NewRequireAuth: Identity
//	                                     + AuthContext, then next
//	ErrNoAuthHeader, header truly absent anonymous AuthContext, then next
//	ErrNoAuthHeader, header present      401 (malformed or non-Bearer header)
//	ErrInvalidToken                      401
//	*NotLiveError                        401 (session revoked / profile
//	                                     unavailable)
//	database fault                       500
//
// A presented-but-bad credential never silently downgrades to anonymous: an
// expired token, a bad signature, a revoked session or a malformed header is
// a caller error and stays a 401. Downgrading instead would be the classic
// confused-deputy pattern.
//
// Invariants on the anonymous branch:
//
//   - The AuthContext has Anonymous set and no session, credential,
//     impersonator or assurance; the anonymous profile holds no grants
//     (checked at boot by NewAnonymousActor).
//   - No Identity is placed on the context, so an accidental composition
//     with NewRequireVerifiedEmail fails closed with a 500 in its first
//     test, and mod-core's RequireAuthenticated rejects it with 401.
//
// Hard preconditions for every route group that opts in:
//
//   - Per-IP rate limiting is required: every anonymous request shares one
//     actor, so actor-based throttling is meaningless.
//   - Never compose with requireAuth or requireVerifiedEmail on the same
//     route group.
//   - Keep requireOIDCConfirmed outermost.
//   - Responses differ between anonymous and authenticated callers; any cache
//     in front must key on the Authorization header (handlers emit
//     "Vary: Authorization").
//   - Audit attribution collapses for the shared actor; attribution for
//     anonymous traffic is opctx.RequestID plus HTTP access logs. No module
//     may authorize a mutating operation for the anonymous actor.
func NewResolveOrAnonymous(a *Authenticator, anon *AnonymousActor) func(http.Handler) http.Handler {
	if a == nil {
		panic("auth: NewResolveOrAnonymous: authenticator must not be nil")
	}
	if anon == nil {
		panic("auth: NewResolveOrAnonymous: anon must not be nil")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := a.Authenticate(r)
			if err != nil {
				if errors.Is(err, ErrNoAuthHeader) && r.Header.Get("Authorization") == "" {
					next.ServeHTTP(w, r.WithContext(withAnonymousActor(r.Context(), anon)))
					return
				}
				writeAuthError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
}
