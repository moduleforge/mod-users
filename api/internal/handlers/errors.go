package handlers

import (
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
)

// writeAuthzError maps an authz error to the appropriate HTTP status by
// delegating to apiresp.WriteError, which classifies err via errors.Is
// against the canonical sentinel set (localAuthz.ErrUnauthenticated/
// ErrForbidden are aliases of apiresp.ErrUnauthenticated/ErrForbidden, so
// they match here without a local switch). Any error that is not one of the
// recognized sentinels maps to 500 internal_error — apiresp.WriteError's
// default — rather than being folded into 403 as the previous hand-written
// switch did.
func writeAuthzError(w http.ResponseWriter, r *http.Request, err error) {
	apiresp.WriteError(w, r, err)
}
