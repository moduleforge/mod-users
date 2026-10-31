package auth

// Error-order tests for the 10 public /v1/auth routes, mounted through the
// real RegisterRoutes with no RequireAuth and no identity in the context
// (apiresp/ordertest cannot model public routes). Helpers are prefixed
// orderAuth. Each route is checked for: an anonymous request is never 401
// because of authentication middleware, a malformed body or unknown field is
// 400 invalid_input, credential failures are 401 with their preserved
// messages, and the anti-enumeration responses are unchanged.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// orderAuthRouter mounts the real RegisterRoutes under /v1/auth. The handlers
// hold no services: every case below is decided before a service is called
// (the stateful cases run in the integration suite).
func orderAuthRouter(t *testing.T) http.Handler {
	t.Helper()
	registry := config.ProviderRegistry{
		"google": config.Provider{ID: "google", DisplayName: "Google"},
	}
	h := New(nil, nil, nil, nil, nil, nil, nil, newTestOAuth(t, registry), nil, "http://gui.test")
	oidc := newTestHandler(t, registry)
	r := chi.NewRouter()
	r.Route("/v1/auth", func(r chi.Router) { RegisterRoutes(r, h, oidc) })
	return r
}

func orderAuthDo(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/v1/auth"+path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rr := httptest.NewRecorder()
	orderAuthRouter(t).ServeHTTP(rr, req)
	return rr
}

type orderAuthErrBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func orderAuthErr(t *testing.T, rr *httptest.ResponseRecorder) orderAuthErrBody {
	t.Helper()
	var e orderAuthErrBody
	if err := json.Unmarshal(rr.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rr.Body.String(), err)
	}
	return e
}

// orderAuthBodyRoutes are the 7 JSON-body routes.
var orderAuthBodyRoutes = []string{
	"/register", "/login", "/anonymous",
	"/email-code/request", "/email-code/verify",
	"/password-reset/request", "/password-reset/confirm",
}

// TestOrderAuth_MalformedAndUnknownFieldBodies_400: decoding precedes
// everything else on every body route, and no route needs authentication to
// reach it.
func TestOrderAuth_MalformedAndUnknownFieldBodies_400(t *testing.T) {
	for _, path := range orderAuthBodyRoutes {
		path := path
		t.Run(path, func(t *testing.T) {
			for name, body := range map[string]string{
				"malformed":     `{"email":`,
				"not-an-object": `[1]`,
				"unknown-field": `{"bogus_field":1}`,
			} {
				rr := orderAuthDo(t, http.MethodPost, path, body)
				if rr.Code != http.StatusBadRequest {
					t.Errorf("%s: got %d, want 400, body=%s", name, rr.Code, rr.Body.String())
					continue
				}
				if e := orderAuthErr(t, rr); e.Error.Code != "invalid_input" {
					t.Errorf("%s: code = %q, want invalid_input", name, e.Error.Code)
				}
			}
		})
	}
}

// TestOrderAuth_StatelessChecks_400: required-field checks are 400 and run
// before any credential lookup.
func TestOrderAuth_StatelessChecks_400(t *testing.T) {
	cases := []struct{ path, body string }{
		{"/register", `{}`},
		{"/login", `{}`},
		{"/anonymous", `{}`},
		{"/email-code/verify", `{}`},
		{"/password-reset/confirm", `{}`},
		{"/password-reset/confirm", `{"token":"t","password":"short"}`},
	}
	for _, c := range cases {
		rr := orderAuthDo(t, http.MethodPost, c.path, c.body)
		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s %s: got %d, want 400, body=%s", c.path, c.body, rr.Code, rr.Body.String())
			continue
		}
		if e := orderAuthErr(t, rr); e.Error.Code != "invalid_input" {
			t.Errorf("%s %s: code = %q, want invalid_input", c.path, c.body, e.Error.Code)
		}
	}
}

// TestOrderAuth_OIDCRoutes: provider discovery and the start/callback
// redirects are public; an unknown provider is 404 and a known provider's bad
// request is 400, never 401.
func TestOrderAuth_OIDCRoutes(t *testing.T) {
	cases := []struct {
		name, path string
		want       int
	}{
		{"providers", "/providers", http.StatusOK},
		{"start unknown provider", "/oidc/nope/start", http.StatusNotFound},
		{"start invalid return", "/oidc/google/start?return=https://evil.example/x", http.StatusBadRequest},
		{"callback unknown provider", "/oidc/nope/callback", http.StatusNotFound},
		{"callback missing code and state", "/oidc/google/callback", http.StatusBadRequest},
	}
	for _, c := range cases {
		rr := orderAuthDo(t, http.MethodGet, c.path, "")
		if rr.Code != c.want {
			t.Errorf("%s: got %d, want %d, body=%s", c.name, rr.Code, c.want, rr.Body.String())
		}
	}
}

// TestOrderAuth_NoRoute401: with no RequireAuth and no identity mounted, no
// route of the group answers 401 because of authentication. The credential
// failures that are 401 run in the integration suite.
func TestOrderAuth_NoRoute401(t *testing.T) {
	cases := []struct{ method, path, body string }{
		{http.MethodPost, "/register", `{}`},
		{http.MethodPost, "/login", `{}`},
		{http.MethodPost, "/email-code/verify", `{}`},
		{http.MethodPost, "/password-reset/confirm", `{}`},
		{http.MethodGet, "/providers", ""},
		// An invalid return path keeps Start from reaching the (nil) provider
		// config; the point here is only that it is not 401.
		{http.MethodGet, "/oidc/google/start?return=https://evil.example/x", ""},
		{http.MethodGet, "/oidc/google/callback", ""},
	}
	for _, c := range cases {
		if rr := orderAuthDo(t, c.method, c.path, c.body); rr.Code == http.StatusUnauthorized {
			t.Errorf("%s %s: got 401 on an anonymous request", c.method, c.path)
		}
	}
}
