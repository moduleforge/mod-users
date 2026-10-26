package handlers

import (
	"errors"
	"net/http"

	"github.com/moduleforge/mod-users/api/internal/auth"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// NewOperatorAdminChecker returns the OIDCConfigDeps.AdminChecker of the
// OIDC configuration routes: the request carries a bearer access token of a
// live, non-impersonating session whose acting profile is an operator profile
// (core_is_operator_profile) holding a wildcard `manage` grant
// (BootstrapService.IsOperatorAdmin). A request without a usable token, or
// with one that is not an operator administrator's, is (false, nil), so the
// route falls through to the setup token (accepted only while the bootstrap
// or the OIDC confirmation is pending, decision U9). An authentication or
// database fault is an error (500).
func NewOperatorAdminChecker(authn *auth.Authenticator, boot *svc.BootstrapService) func(r *http.Request) (bool, error) {
	return func(r *http.Request) (bool, error) {
		id, err := authn.Authenticate(r)
		if err != nil {
			var notLive *auth.NotLiveError
			if errors.Is(err, auth.ErrNoAuthHeader) ||
				errors.Is(err, auth.ErrInvalidToken) ||
				errors.As(err, &notLive) {
				return false, nil
			}
			return false, err
		}
		return boot.IsOperatorAdmin(auth.WithIdentity(r.Context(), id))
	}
}
