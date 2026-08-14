package auth

import (
	"errors"
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/useraction"
)

// RequireVerifiedEmail blocks the wrapped handler when the resolved UserContext
// has no EmailVerifiedAt timestamp. Allowlisted routes (e.g. GET /v1/self) must
// be mounted before (or outside) this middleware in the chain.
//
// Missing context (middleware ordered incorrectly) → 500 Internal Server Error,
// via apiresp.WriteError's reserved-core internal_error default.
// Unverified account → 403 Forbidden with a users.email_unverified
// action-required envelope, via apiresp.WriteActionRequired.
// The middleware performs no DB I/O; it reads the already-resolved UserContext.
func RequireVerifiedEmail(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uc, ok := FromContext(r.Context())
		if !ok {
			// Programmer error: this middleware was mounted outside the
			// RequireAuth group. Fail loudly so the misconfiguration is
			// caught immediately during development.
			apiresp.WriteError(w, r, errors.New("RequireVerifiedEmail mounted before RequireAuth"))
			return
		}

		if uc.EmailVerifiedAt == nil {
			// Stable wire contract — the GUI in Phase 5 depends on the
			// action.code / action.path fields of the action-required
			// envelope (users.email_unverified, /verify-email).
			apiresp.WriteActionRequired(w, r, useraction.EmailUnverified,
				"Verify your email address before continuing.", "/verify-email", nil)
			return
		}

		next.ServeHTTP(w, r)
	})
}
