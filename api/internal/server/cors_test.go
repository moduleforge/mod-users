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

// credentialsHeader returns Access-Control-Allow-Credentials for a cross-origin GET.
func credentialsHeader(r *chi.Mux, origin string) string {
	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Header().Get("Access-Control-Allow-Credentials")
}

// TestNew_EmptyCORSOriginsDeniesOutsideLocal pins the safe default: an unset
// CORS_ORIGINS yields no CORS headers at all in every non-local mode.
func TestNew_EmptyCORSOriginsDeniesOutsideLocal(t *testing.T) {
	modes := []config.DeployMode{
		config.DeployModeServerless,
		config.DeployModeK8s,
		config.DeployModeContainerHost,
	}
	for _, mode := range modes {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			r := newTestRouter(mode, "")
			if got := allowOrigin(r, "https://evil.example"); got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q, want no header", got)
			}
			if got := preflightAllowOrigin(r, "https://evil.example"); got != "" {
				t.Errorf("preflight Access-Control-Allow-Origin = %q, want no header", got)
			}
			if got := credentialsHeader(r, "https://evil.example"); got != "" {
				t.Errorf("Access-Control-Allow-Credentials = %q, want no header", got)
			}
		})
	}
}

// TestNew_AllowListEchoesOriginWithCredentials covers the explicit allow-list:
// match echoes the origin and allows credentials; mismatch gets nothing.
func TestNew_AllowListEchoesOriginWithCredentials(t *testing.T) {
	const ok = "https://app.example.com"
	for _, mode := range []config.DeployMode{config.DeployModeLocal, config.DeployModeK8s} {
		r := newTestRouter(mode, ok+", https://other.example.com")
		if got := allowOrigin(r, ok); got != ok {
			t.Errorf("%s: ACAO = %q, want %q", mode, got, ok)
		}
		if got := credentialsHeader(r, ok); got != "true" {
			t.Errorf("%s: ACAC = %q, want true", mode, got)
		}
		if got := allowOrigin(r, "https://evil.example"); got != "" {
			t.Errorf("%s: mismatched origin ACAO = %q, want none", mode, got)
		}
	}
}

// TestNew_LocalConvenienceNeverCombinesWildcardWithCredentials covers the
// documented local-mode permissive default (empty or explicit "*").
func TestNew_LocalConvenienceNeverCombinesWildcardWithCredentials(t *testing.T) {
	for _, raw := range []string{"", "*"} {
		r := newTestRouter(config.DeployModeLocal, raw)
		if got := allowOrigin(r, "https://any.example"); got != "*" {
			t.Errorf("CORS_ORIGINS=%q: ACAO = %q, want *", raw, got)
		}
		if got := credentialsHeader(r, "https://any.example"); got != "" {
			t.Errorf("CORS_ORIGINS=%q: ACAC = %q, want none (wildcard must not carry credentials)", raw, got)
		}
	}
}

// TestNew_WildcardOutsideLocalFailsClosed: config.Load rejects this; if a
// hand-built Config gets through, the router still denies.
func TestNew_WildcardOutsideLocalFailsClosed(t *testing.T) {
	r := newTestRouter(config.DeployModeK8s, "*")
	if got := allowOrigin(r, "https://any.example"); got != "" {
		t.Errorf("ACAO = %q, want none", got)
	}
}
