package handlers

import (
	"errors"
	"net/http"

	localAuthz "github.com/moduleforge/mod-users/api/internal/authz"
	"github.com/moduleforge/mod-users/api/internal/server"
)

// writeAuthzError maps an authz error to the appropriate HTTP status.
// ErrUnauthenticated → 401; anything else (including ErrForbidden) → 403.
func writeAuthzError(w http.ResponseWriter, err error) {
	if errors.Is(err, localAuthz.ErrUnauthenticated) {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	server.Error(w, http.StatusForbidden, "forbidden", "access denied")
}
