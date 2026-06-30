package auth

import (
	"net/http"

	"github.com/moduleforge/mod-users/api/internal/config"
	"github.com/moduleforge/mod-users/api/internal/server"
)

// RequireOIDCConfirmed blocks /v1/* traffic when the OIDC onboarding flow
// has not yet been confirmed. The statusFn closure is re-invoked on every
// request so that a POST /v1/oidc-config/confirm that flips the state
// mid-session takes effect for subsequent requests without restart.
//
// The middleware is mounted on /v1/* routes *except* /v1/oidc-config/*
// so the operator can always reach the onboarding endpoints. Health
// endpoints (/healthz, /readyz) sit outside /v1 and are unaffected.
//
// The 503 response body is deliberately machine-parseable (config_path
// in particular) so the GUI can redirect without string-parsing HTTP
// status text.
func RequireOIDCConfirmed(statusFn func() config.BootState) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			state := statusFn()
			if state.Confirmed() {
				next.ServeHTTP(w, r)
				return
			}

			// 503 Service Unavailable is the right signal: the service
			// *exists* but cannot serve normal traffic until the operator
			// completes a setup step. The body identifies the remediation
			// path so clients don't need to guess the onboarding URL.
			server.JSON(w, http.StatusServiceUnavailable, map[string]any{
				"error":       "oidc_not_confirmed",
				"config_path": "/oidc-config",
				"state":       string(state),
			})
		})
	}
}
