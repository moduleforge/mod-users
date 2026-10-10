package auth

import (
	"net/http"
	"strings"

	"github.com/moduleforge/mod-users/api/internal/config"
)

const (
	// oidcStateCookieMarker is the route segment that scopes the OIDC state cookie.
	oidcStateCookieMarker = "/v1/auth/oidc/"
	// DefaultOIDCStateCookiePath is used when the request path lacks the marker.
	DefaultOIDCStateCookiePath = oidcStateCookieMarker
)

// OIDCStateCookiePath returns the oidc_state cookie Path.
//
// When basePath (USERS_COOKIE_PATH) is non-empty and valid it wins: the result
// is basePath + "/v1/auth/oidc/", which keeps the cookie matching the public
// URL when a proxy strips the mount prefix before the request reaches the app.
// An invalid basePath is ignored (config loading rejects it at startup; this is
// defence in depth so a bad value can never reach a Set-Cookie header).
//
// When basePath is empty the Path is derived from the request's own URL path,
// preserving any mount prefix before "/v1/auth/oidc/", and falls back to
// DefaultOIDCStateCookiePath when the marker is absent.
func OIDCStateCookiePath(r *http.Request, basePath string) string {
	if basePath != "" && config.ValidateCookieBasePath(basePath) == nil {
		return basePath + oidcStateCookieMarker
	}
	if r == nil || r.URL == nil {
		return DefaultOIDCStateCookiePath
	}
	i := strings.Index(r.URL.Path, oidcStateCookieMarker)
	if i < 0 {
		return DefaultOIDCStateCookiePath
	}
	return r.URL.Path[:i] + oidcStateCookieMarker
}
