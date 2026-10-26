package handlers_test

// Every authenticated registrar of the facade is mounted behind
// auth.NewRequireAuth by its composer, and 401 is the first rejection whatever
// the request looks like: a valid request, a malformed body and a malformed
// path uuid all answer 401 before any handler code (or its zero-value
// dependencies) runs.
//
// The registrars are mounted the way the manifest composes them: several
// registrars merged onto one shared /v1 router, each group behind the
// authentication middleware. Handlers are zero values; the middleware refuses
// the request before it reaches them.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/mod-users/api/auth"
	"github.com/moduleforge/mod-users/api/config"
	"github.com/moduleforge/mod-users/api/handlers"
)

const mountUUID = "11111111-1111-1111-1111-111111111111"

type mountRoute struct {
	method, pattern string
}

// stubStarter satisfies the OIDC start interfaces.
type stubStarter struct{}

func (stubStarter) LinkStart(http.ResponseWriter, *http.Request)   {}
func (stubStarter) StepUpStart(http.ResponseWriter, *http.Request) {}

func requireAuthMountRouter(t *testing.T) (http.Handler, []mountRoute) {
	t.Helper()
	// An authenticator with no database and no verifier: a request without a
	// bearer token is refused before either is used.
	requireAuth := auth.NewRequireAuth(auth.NewAuthenticator(nil, nil, config.AuthConfig{}))

	r := chi.NewRouter()
	r.Route("/v1", func(r chi.Router) {
		r.Route("/me", func(r chi.Router) {
			r.Use(requireAuth)
			handlers.RegisterMeRoutes(r, &handlers.MeHandler{}, stubStarter{})
		})
		r.Route("/session", func(r chi.Router) {
			r.Use(requireAuth)
			handlers.RegisterSessionRoutes(r, &handlers.SessionHandler{}, stubStarter{})
		})
		r.Route("/impersonations", func(r chi.Router) {
			r.Use(requireAuth)
			handlers.RegisterImpersonationRoutes(r, &handlers.ImpersonationHandler{})
		})
		r.Route("/principals", func(r chi.Router) {
			r.Use(requireAuth)
			handlers.RegisterPrincipalRoutes(r, &handlers.PrincipalsHandler{})
		})
		r.Route("/parties", func(r chi.Router) {
			r.Use(requireAuth)
			handlers.RegisterPartyRoutes(r, &handlers.PartiesHandler{})
		})
		r.Route("/profiles", func(r chi.Router) {
			r.Use(requireAuth)
			handlers.RegisterProfileRoutes(r, &handlers.PartiesHandler{})
		})
	})

	var routes []mountRoute
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, mountRoute{method, route})
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Slice(routes, func(i, j int) bool {
		return routes[i].pattern+routes[i].method < routes[j].pattern+routes[j].method
	})
	return r, routes
}

func concretePath(pattern string) string {
	p := strings.ReplaceAll(pattern, "{uuid}", mountUUID)
	p = strings.ReplaceAll(p, "{party_uuid}", mountUUID)
	p = strings.ReplaceAll(p, "{provider}", "google")
	return strings.TrimSuffix(p, "/*")
}

func assertUnauthorized(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %q)", rec.Code, rec.Body.String())
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v (body %q)", err, rec.Body.String())
	}
	if env.Error.Code != "unauthorized" {
		t.Fatalf("error.code = %q, want unauthorized", env.Error.Code)
	}
}

func TestRequireAuthMount_EveryRouteAnswers401First(t *testing.T) {
	h, routes := requireAuthMountRouter(t)
	if len(routes) < 30 {
		t.Fatalf("only %d routes mounted: %v", len(routes), routes)
	}
	for _, want := range []string{
		"GET /v1/me/", "PUT /v1/me/", "POST /v1/me/upgrade", "DELETE /v1/me/credentials/{uuid}",
		"POST /v1/me/credentials/oidc/{provider}/start",
		"GET /v1/session/", "POST /v1/session/switch", "POST /v1/session/step-up/oidc/{provider}/start",
		"POST /v1/impersonations/", "POST /v1/impersonations/current/renew", "DELETE /v1/impersonations/current",
			"GET /v1/principals/", "POST /v1/principals/{uuid}/disable",
		"POST /v1/me/profiles/{uuid}/accept", "POST /v1/me/profiles/{uuid}/decline", "POST /v1/me/profiles/{uuid}/leave",
		"GET /v1/parties/{party_uuid}/profiles", "POST /v1/parties/{party_uuid}/profiles",
		"POST /v1/parties/{party_uuid}/managed-users", "PATCH /v1/profiles/{uuid}",
	} {
		found := false
		for _, rt := range routes {
			if rt.method+" "+rt.pattern == want {
				found = true
			}
		}
		if !found {
			t.Errorf("route %q is not mounted", want)
		}
	}

	for _, rt := range routes {
		t.Run(rt.method+" "+rt.pattern, func(t *testing.T) {
			path := concretePath(rt.pattern)
			cases := map[string]string{
				"valid request":  `{}`,
				"malformed body": `{not json`,
				"empty body":     ``,
			}
			for name, body := range cases {
				req := httptest.NewRequest(rt.method, path, strings.NewReader(body))
				if body != "" {
					req.Header.Set("Content-Type", "application/json")
				}
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				if rec.Code != http.StatusUnauthorized {
					t.Fatalf("%s: status = %d, want 401 (body %q)", name, rec.Code, rec.Body.String())
				}
				assertUnauthorized(t, rec)
			}
			// A malformed path uuid is still 401 first.
			if strings.Contains(rt.pattern, "{uuid}") || strings.Contains(rt.pattern, "{party_uuid}") {
				bad := strings.ReplaceAll(rt.pattern, "{uuid}", "not-a-uuid")
				bad = strings.ReplaceAll(bad, "{party_uuid}", "not-a-uuid")
				bad = strings.ReplaceAll(bad, "{provider}", "google")
				req := httptest.NewRequest(rt.method, bad, strings.NewReader(`{}`))
				rec := httptest.NewRecorder()
				h.ServeHTTP(rec, req)
				assertUnauthorized(t, rec)
			}
			// A token in a cookie is not a credential.
			req := httptest.NewRequest(rt.method, path, nil)
			req.AddCookie(&http.Cookie{Name: "access_token", Value: "x"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assertUnauthorized(t, rec)
		})
	}
}
