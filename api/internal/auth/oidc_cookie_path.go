package auth

import (
	"net/http"
	"strings"
)

const (
	// oidcStateCookieMarker is the route segment that scopes the OIDC state cookie.
	oidcStateCookieMarker = "/v1/auth/oidc/"
	// DefaultOIDCStateCookiePath is used when the request path lacks the marker.
	DefaultOIDCStateCookiePath = oidcStateCookieMarker
)

// OIDCStateCookiePath derives the oidc_state cookie Path from the request's
// own URL path, preserving any mount prefix before "/v1/auth/oidc/". Falls
// back to DefaultOIDCStateCookiePath when the marker is absent.
func OIDCStateCookiePath(r *http.Request) string {
	if r == nil || r.URL == nil {
		return DefaultOIDCStateCookiePath
	}
	i := strings.Index(r.URL.Path, oidcStateCookieMarker)
	if i < 0 {
		return DefaultOIDCStateCookiePath
	}
	return r.URL.Path[:i] + oidcStateCookieMarker
}
