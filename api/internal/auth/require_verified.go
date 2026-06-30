package auth

import (
	"net/http"

	"github.com/moduleforge/mod-users/api/internal/server"
)

// RequireVerifiedEmail blocks the wrapped handler when the resolved UserContext
// has no EmailVerifiedAt timestamp. Allowlisted routes (e.g. GET /v1/self) must
// be mounted before (or outside) this middleware in the chain.
//
// Missing context (middleware ordered incorrectly) → 500 Internal Server Error.
// Unverified account → 403 Forbidden with a stable machine-parseable body.
// The middleware performs no DB I/O; it reads the already-resolved UserContext.
func RequireVerifiedEmail(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uc, ok := FromContext(r.Context())
		if !ok {
			// Programmer error: this middleware was mounted outside the
			// RequireAuth group. Fail loudly so the misconfiguration is
			// caught immediately during development.
			server.JSON(w, http.StatusInternalServerError, map[string]any{
				"error":   "internal_error",
				"message": "server misconfiguration: RequireVerifiedEmail mounted before RequireAuth",
			})
			return
		}

		if uc.EmailVerifiedAt == nil {
			// Stable wire contract — the GUI in Phase 5 depends on the
			// "email_unverified" error code and "verify_path" field.
			server.JSON(w, http.StatusForbidden, map[string]any{
				"error":       "email_unverified",
				"message":     "Verify your email address before continuing.",
				"verify_path": "/v1/auth/email-code/request",
			})
			return
		}

		next.ServeHTTP(w, r)
	})
}
