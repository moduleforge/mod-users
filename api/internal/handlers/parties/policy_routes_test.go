package parties

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
)

// authed wraps h so the request carries an authenticated identity, as
// auth.NewRequireAuth would install it; the nil services are never reached
// when the path is malformed.
func authed(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := localauth.WithIdentity(r.Context(), &localauth.Identity{})
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

func TestPolicyRoutesRejectMalformedUUIDsBeforeTheService(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterPartyRoutes(r, NewPartiesHandler(nil, nil))
	id := uuid.New().String()
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/nope/authn-policy"},
		{http.MethodPut, "/nope/authn-policy"},
		{http.MethodGet, "/nope/credential-authorizations"},
		{http.MethodPut, "/nope/credential-authorizations/" + id},
		{http.MethodPut, "/" + id + "/credential-authorizations/nope"},
		{http.MethodDelete, "/" + id + "/credential-authorizations/nope"},
		{http.MethodPut, "/" + id + "/confinements/nope"},
		{http.MethodDelete, "/nope/confinements/" + id},
	} {
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{"credential_rule":"any"}`))
		rec := httptest.NewRecorder()
		authed(r).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status = %d, want 400", rt.method, rt.path, rec.Code)
		}
	}
}

func TestPutAuthnPolicyRejectsAMalformedBody(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterPartyRoutes(r, NewPartiesHandler(nil, nil))
	path := "/" + uuid.New().String() + "/authn-policy"
	for _, body := range []string{`{"credential_rule":"any","min_acr":"high"}`, `not json`} {
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		authed(r).ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %q: status = %d, want 400", body, rec.Code)
		}
	}
}
