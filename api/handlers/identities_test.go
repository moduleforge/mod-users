package handlers_test

// Call-shape coverage for the identities/credential public facade — the
// direct guard against the self-route-manifest incident (a signature
// mismatch that compiles here but breaks consuming apps' mfgen-generated
// output). Exercises handlers.NewIdentitiesHandler and the two registrar
// wrappers exactly the way generated composition-root code will call them:
// with individually-typed positional args resolved from the manifest, and
// registration on a bare chi.Router.

import (
	"net/http"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/mod-users/api/handlers"
)

// TestNewIdentitiesHandler_ConstructsFromManifestShapedArgs calls the public
// constructor with representative typed args matching the manifest's
// identitiesHandler entry (§3 of the task doc): nil pool/queries/oauth/obs
// are safe since the constructor only stores fields, sender is a nil
// authhandlers.Sender (the parameter's static type — see handlers.go's
// doc comment on the Sender-typing gotcha), jwtSecret is empty, consumed is a
// fresh *sync.Map, and stepUpRequired is false.
func TestNewIdentitiesHandler_ConstructsFromManifestShapedArgs(t *testing.T) {
	h := handlers.NewIdentitiesHandler(nil, nil, nil, nil, nil, "", &sync.Map{}, false)
	if h == nil {
		t.Fatal("NewIdentitiesHandler returned nil")
	}
}

// TestRegisterSelfIdentitiesReadRoute_MountsGetIdentities proves the read
// registrar wrapper actually mounts GET /self/identities on the router
// passed to it, the way generated wiring's register: call will.
func TestRegisterSelfIdentitiesReadRoute_MountsGetIdentities(t *testing.T) {
	h := handlers.NewIdentitiesHandler(nil, nil, nil, nil, nil, "", &sync.Map{}, false)
	r := chi.NewRouter()
	handlers.RegisterSelfIdentitiesReadRoute(r, h)

	got := walkRoutes(t, r)
	want := map[string]bool{"GET /self/identities": true}
	assertRoutes(t, got, want)
}

// TestRegisterSelfIdentitiesWriteRoutes_MountsSixEndpoints proves the write
// registrar wrapper mounts all six credential-mutating endpoints on the
// router passed to it, the way generated wiring's register: call will.
func TestRegisterSelfIdentitiesWriteRoutes_MountsSixEndpoints(t *testing.T) {
	h := handlers.NewIdentitiesHandler(nil, nil, nil, nil, nil, "", &sync.Map{}, false)
	r := chi.NewRouter()
	handlers.RegisterSelfIdentitiesWriteRoutes(r, h)

	got := walkRoutes(t, r)
	want := map[string]bool{
		"POST /self/identities/oidc/{provider}/start": true,
		"DELETE /self/identities/{identity_uuid}":     true,
		"POST /self/credential/password":              true,
		"DELETE /self/credential/password":            true,
		"POST /self/credential/step-up":               true,
		"POST /self/credential/step-up/verify":        true,
	}
	assertRoutes(t, got, want)
}

// walkRoutes collects "METHOD path" strings for every route mounted on r via
// chi.Walk, without invoking any handler (IdentitiesHandler's methods call
// localauth.MustFromContext and would panic without a request-scoped
// UserContext, which is out of scope for a routing-only test).
func walkRoutes(t *testing.T, r chi.Router) map[string]bool {
	t.Helper()
	got := make(map[string]bool)
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got[method+" "+route] = true
		return nil
	})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	return got
}

func assertRoutes(t *testing.T, got, want map[string]bool) {
	t.Helper()
	for k := range want {
		if !got[k] {
			t.Errorf("expected route %q to be mounted, got routes: %v", k, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("got %d mounted routes, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
}
