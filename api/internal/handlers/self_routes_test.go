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
	"github.com/google/uuid"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	db "github.com/moduleforge/mod-users/model/db"
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

// TestBuildSelfResponse_EntityUUID asserts that GET /v1/self's response body
// (built by buildSelfResponse) carries entity_uuid — sourced from
// profile.Entity.Uuid, the caller's underlying entity/person UUID used to
// scope requests in other modules (e.g. mod-tags, mod-tasks) — as a field
// distinct from the pre-existing "uuid" (the user_accounts row's own UUID).
func TestBuildSelfResponse_EntityUUID(t *testing.T) {
	userUUID := uuid.New()
	entityUUID := uuid.New()

	ua := db.UserAccount{Uuid: userUUID}
	profile := coreservice.Profile{
		Kind: "natural_person",
		Entity: coredb.GetEntityByUUIDRow{
			Uuid: entityUUID,
		},
	}

	resp := buildSelfResponse(ua, profile)

	gotEntityUUID, ok := resp["entity_uuid"].(string)
	if !ok {
		t.Fatalf("entity_uuid missing or not a string in response: %#v", resp)
	}
	if gotEntityUUID != entityUUID.String() {
		t.Errorf("entity_uuid = %q, want %q", gotEntityUUID, entityUUID.String())
	}

	gotUUID, ok := resp["uuid"].(string)
	if !ok {
		t.Fatalf("uuid missing or not a string in response: %#v", resp)
	}
	if gotUUID != userUUID.String() {
		t.Errorf("uuid = %q, want %q", gotUUID, userUUID.String())
	}
	if gotUUID == gotEntityUUID {
		t.Errorf("uuid and entity_uuid unexpectedly equal (%q); they must be distinct fields", gotUUID)
	}
}
