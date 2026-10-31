package auth

import (
	"errors"
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/useraction"
)

// NewRequireVerifiedEmail returns middleware that blocks the wrapped handler
// unless the authenticated Identity's user holds at least one verified email
// identifier (Identity.EmailVerified). Allowlisted routes must be mounted
// before (or outside) this middleware in the chain.
//
// Missing Identity (middleware ordered incorrectly, or composed with
// NewResolveOrAnonymous) → 500, via apiresp.WriteError's reserved-core
// internal_error default. Unverified → 403 with the users.email_unverified
// action-required envelope (stable wire contract: action.code and
// action.path). The middleware performs no database I/O; the Authenticator
// resolved the flag in its liveness round trip.
func NewRequireVerifiedEmail() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, ok := IdentityFromContext(r.Context())
			if !ok {
				apiresp.WriteError(w, r, errors.New("RequireVerifiedEmail mounted outside RequireAuth"))
				return
			}
			if !id.EmailVerified {
				apiresp.WriteActionRequired(w, r, useraction.EmailUnverified,
					"Verify your email address before continuing.", "/verify-email", nil)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
