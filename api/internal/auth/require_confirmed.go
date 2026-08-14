package auth

import (
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/config"
	"github.com/moduleforge/mod-users/api/internal/useraction"
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
// The 503 response is deliberately machine-parseable (action.path in
// particular) so the GUI can redirect without string-parsing HTTP status
// text.
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
			// completes a setup step. The action-required envelope
			// identifies the remediation path so clients don't need to
			// guess the onboarding URL.
			apiresp.WriteActionRequired(w, r, useraction.OIDCNotConfirmed,
				"Single sign-on is not finished configuring.", "/oidc-config",
				map[string]any{"state": string(state)})
		})
	}
}
