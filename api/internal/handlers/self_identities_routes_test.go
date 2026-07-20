package handlers

// Tests for the read-unguarded / write-gated split between
// RegisterSelfIdentitiesReadRoute and RegisterSelfIdentitiesWriteRoutes.
// Mirrors self_routes_test.go's TestSelfRoutes_GetPutSplit: the split is
// expressed at the manifest/registration level via two separate route
// entries, each carrying its own middleware list, rather than inside a
// single registrar function with a nested r.Group. This test guards against
// a future accidental edit that merges the two registrars back into one
// function, or applies the verified-email gate to the read route.
//
// IdentitiesHandler's List/StartLink/Unlink/SetPassword/RemovePassword/
// StepUpRequest/StepUpVerify methods all call localauth.MustFromContext as
// their first step, which panics when no *localauth.UserContext is on the
// request context — true here, since this test deliberately exercises only
// the routing/middleware wiring, not IdentitiesHandler's business logic.
// recoverToSentinel (defined in self_routes_test.go, same package) turns
// that panic into a sentinel status code, giving the test an observable
// signal for "the request reached the handler" that is distinct from
// "middleware short-circuited first".

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestSelfIdentitiesRoutes_ReadWriteSplit asserts GET /self/identities never
// runs behind the verified-email marker while a representative mutating
// endpoint (POST /self/credential/password) only reaches the handler when
// the marker's check passes — matching the two manifest entries' middleware
// lists (read: requireOIDCConfirmed + requireAuth; write: those plus
// requireVerifiedEmail).
func TestSelfIdentitiesRoutes_ReadWriteSplit(t *testing.T) {
	h := &IdentitiesHandler{}

	tests := []struct {
		name         string
		method       string
		path         string
		markerBlocks bool
		wantFired    bool
		wantStatus   int
	}{
		{
			name:       "GET /self/identities bypasses the verified-email marker entirely",
			method:     http.MethodGet,
			path:       "/self/identities",
			wantFired:  false,
			wantStatus: handlerReachedStatus,
		},
		{
			name:         "POST /self/credential/password is blocked when the verified-email marker denies",
			method:       http.MethodPost,
			path:         "/self/credential/password",
			markerBlocks: true,
			wantFired:    true,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:       "POST /self/credential/password reaches the handler when the verified-email marker allows",
			method:     http.MethodPost,
			path:       "/self/credential/password",
			wantFired:  true,
			wantStatus: handlerReachedStatus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fired bool
			r := chi.NewRouter()

			// Read group: mirrors the manifest's GET /v1/self/identities
			// entry — requireOIDCConfirmed + requireAuth only, no
			// verified-email marker.
			r.Group(func(r chi.Router) {
				r.Use(recoverToSentinel)
				RegisterSelfIdentitiesReadRoute(r, h)
			})

			// Write group: mirrors the manifest's mutating identity/
			// credential entries — adds the verified-email marker on top of
			// the same base middleware.
			r.Group(func(r chi.Router) {
				r.Use(recoverToSentinel)
				r.Use(verifiedEmailMarker(&fired, tt.markerBlocks))
				RegisterSelfIdentitiesWriteRoutes(r, h)
			})

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			r.ServeHTTP(rr, req)

			if fired != tt.wantFired {
				t.Errorf("verified-email marker fired = %v, want %v", fired, tt.wantFired)
			}
			if rr.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tt.wantStatus)
			}
		})
	}
}
