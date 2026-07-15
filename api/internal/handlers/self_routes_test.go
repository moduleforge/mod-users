package handlers

// Tests for the GET-unguarded / PUT-gated split between RegisterSelfGetRoute
// and RegisterSelfPutRoute. Since task 001 the split is no longer expressed
// inside a single RegisterSelfRoutes function (a nested r.Group around PUT
// only); it is now expressed at the manifest/registration level via two
// separate /v1 route entries, each carrying its own middleware: list. This
// test guards against a future accidental edit that merges the two entries
// back into one function, or applies the verified-email gate to the wrong
// verb.
//
// SelfHandler.Get/Put both call auth.MustFromContext as their first step,
// which panics when no *auth.UserContext is on the request context — true
// here, since this test deliberately exercises only the routing/middleware
// wiring, not SelfHandler's business logic (already covered by its own
// dependencies elsewhere). recoverToSentinel turns that panic into a sentinel
// status code, giving the test an observable signal for "the request reached
// the handler" that is distinct from "middleware short-circuited first".

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// handlerReachedStatus is written by recoverToSentinel when the wrapped
// handler panics (see package doc comment above for why SelfHandler.Get/Put
// panic in this test's minimal setup).
const handlerReachedStatus = 599

// recoverToSentinel recovers a panic from the wrapped handler and reports
// handlerReachedStatus instead of letting the panic escape — used only to
// distinguish "the request reached the SelfHandler method" from "middleware
// short-circuited before the handler ever ran".
func recoverToSentinel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				w.WriteHeader(handlerReachedStatus)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// verifiedEmailMarker stands in for the generated requireVerifiedEmail
// middleware. It records whether it ran and, when block is true, denies the
// request (mirroring an account with an unverified email) instead of calling
// next.
func verifiedEmailMarker(fired *bool, block bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*fired = true
			if block {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TestSelfRoutes_GetPutSplit asserts GET /self never runs behind the
// verified-email marker while PUT /self only reaches the handler when the
// marker's check passes — matching the two manifest entries' middleware
// lists (GET: requireOIDCConfirmed + requireAuth; PUT: those plus
// requireVerifiedEmail).
func TestSelfRoutes_GetPutSplit(t *testing.T) {
	h := &SelfHandler{}

	tests := []struct {
		name         string
		method       string
		markerBlocks bool
		wantFired    bool
		wantStatus   int
	}{
		{
			name:       "GET bypasses the verified-email marker entirely",
			method:     http.MethodGet,
			wantFired:  false,
			wantStatus: handlerReachedStatus,
		},
		{
			name:         "PUT is blocked when the verified-email marker denies",
			method:       http.MethodPut,
			markerBlocks: true,
			wantFired:    true,
			wantStatus:   http.StatusForbidden,
		},
		{
			name:       "PUT reaches the handler when the verified-email marker allows",
			method:     http.MethodPut,
			wantFired:  true,
			wantStatus: handlerReachedStatus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var fired bool
			r := chi.NewRouter()

			// GET group: mirrors the manifest's GET /v1/self entry —
			// requireOIDCConfirmed + requireAuth only, no verified-email marker.
			r.Group(func(r chi.Router) {
				r.Use(recoverToSentinel)
				RegisterSelfGetRoute(r, h)
			})

			// PUT group: mirrors the manifest's PUT /v1/self entry — adds the
			// verified-email marker on top of the same base middleware.
			r.Group(func(r chi.Router) {
				r.Use(recoverToSentinel)
				r.Use(verifiedEmailMarker(&fired, tt.markerBlocks))
				RegisterSelfPutRoute(r, h)
			})

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, "/self", nil)
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
