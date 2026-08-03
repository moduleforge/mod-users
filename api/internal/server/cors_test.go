package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// newTestRouter builds the base router New produces for the given deploy mode
// and CORS_ORIGINS value, with one route mounted so a request can be driven
// all the way through the middleware stack.
func newTestRouter(mode config.DeployMode, corsOrigins string) *chi.Mux {
	cfg := &config.Config{
		DeployMode: mode,
		Server: config.ServerConfig{
			Addr:        ":0",
			CORSOrigins: corsOrigins,
		},
	}
	_, r := New(cfg)
	r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	r.Post("/ping", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	return r
}

// allowOrigin returns the Access-Control-Allow-Origin header a cross-origin
// GET from origin receives. An empty result means the header was absent, i.e.
// the browser will block the response.
func allowOrigin(r *chi.Mux, origin string) string {
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Header().Get("Access-Control-Allow-Origin")
}

// preflightAllowOrigin is allowOrigin for the CORS preflight rather than the
// simple request.
func preflightAllowOrigin(r *chi.Mux, origin string) string {
	req := httptest.NewRequest(http.MethodOptions, "/ping", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Header().Get("Access-Control-Allow-Origin")
}

// TestNew_CORSAllowAllIsLocalOnly pins the property every non-local deploy
// mode depends on: the explicit allow-all origin list in New is reached only
// under DeployModeLocal. container-host is a non-local mode, so it must take
// the same branch as k8s and serverless, never local's.
func TestNew_CORSAllowAllIsLocalOnly(t *testing.T) {
	const configured = "https://app.example.com"
	const unlisted = "https://evil.example"

	nonLocalModes := []config.DeployMode{
		config.DeployModeContainerHost,
		config.DeployModeK8s,
		config.DeployModeServerless,
	}

	for _, mode := range nonLocalModes {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			if mode == config.DeployModeLocal {
				t.Fatalf("%q must not equal DeployModeLocal; the local-only relaxations key off that comparison", mode)
			}

			r := newTestRouter(mode, configured)

			if got := allowOrigin(r, configured); got != configured {
				t.Errorf("configured origin: Access-Control-Allow-Origin = %q, want %q", got, configured)
			}
			if got := allowOrigin(r, unlisted); got != "" {
				t.Errorf("unlisted origin: Access-Control-Allow-Origin = %q, want no header", got)
			}
			if got := preflightAllowOrigin(r, unlisted); got != "" {
				t.Errorf("unlisted origin preflight: Access-Control-Allow-Origin = %q, want no header", got)
			}
		})
	}
}

// TestNew_EmptyCORSOriginsIsAllowAllInEveryMode is a characterization test,
// not an endorsement. github.com/go-chi/cors treats an empty AllowedOrigins
// list as allow-all (cors.go: allowedOriginsAll is set when the list is empty
// and no AllowOriginFunc is supplied), so an unset CORS_ORIGINS yields
// "Access-Control-Allow-Origin: *" in *every* deploy mode — not just local.
//
// The local-only branch in New is therefore about intent, not effect: local
// asks for allow-all deliberately, while a non-local mode falls into it by
// omission. Deployments are expected to set CORS_ORIGINS explicitly and to
// enforce that boot-side; this test exists so that any future change to the
// shared default fails here loudly rather than silently altering the policy
// every consuming application inherits.
func TestNew_EmptyCORSOriginsIsAllowAllInEveryMode(t *testing.T) {
	modes := []config.DeployMode{
		config.DeployModeLocal,
		config.DeployModeServerless,
		config.DeployModeK8s,
		config.DeployModeContainerHost,
	}

	for _, mode := range modes {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()

			r := newTestRouter(mode, "")

			if got := allowOrigin(r, "https://evil.example"); got != "*" {
				t.Errorf("empty CORS_ORIGINS: Access-Control-Allow-Origin = %q, want %q "+
					"(if this now fails, the shared CORS default changed — update the operator docs with it)", got, "*")
			}
		})
	}
}
