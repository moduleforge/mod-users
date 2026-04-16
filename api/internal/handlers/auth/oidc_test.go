package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/config"
)

// newTestOAuth stands up a minimal OAuth struct suitable for handler-level
// tests. No real provider discovery happens — Verifiers/OAuthConfigs are left
// empty because these tests never exercise Exchange's network path.
func newTestOAuth(t *testing.T, registry config.ProviderRegistry) *localauth.OAuth {
	t.Helper()
	signer, err := localauth.NewStateSigner([]byte("test-secret-at-least-32-bytes-xx"))
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}
	return &localauth.OAuth{
		Registry:          registry,
		Verifiers:         nil,
		OAuthConfigs:      nil,
		Mappers:           nil,
		StateSigner:       signer,
		RedirectBase:      "http://api.test",
		FrontendReturnURL: "http://gui.test/auth/oidc/return",
	}
}

// newTestHandler constructs a handler with a stub oauth. Pool/queries/resolver
// are nil because the tests in this file only cover paths that short-circuit
// before touching the DB.
func newTestHandler(t *testing.T, registry config.ProviderRegistry) *OIDCHandler {
	cfg := &config.Config{
		LocalAuth: config.LocalAuthConfig{
			JWTSecret:   "test-secret",
			LocalIssuer: "test-issuer",
		},
		Auth: config.AuthConfig{
			AdminRole:            "admin",
			FrontendReturnURL:    "http://gui.test/auth/oidc/return",
			OAuthRedirectBaseURL: "http://api.test",
		},
	}
	return NewOIDCHandler(nil, newTestOAuth(t, registry), nil, cfg)
}

func TestListProviders(t *testing.T) {
	t.Run("returns only id and display_name", func(t *testing.T) {
		registry := config.ProviderRegistry{
			"google": config.Provider{
				ID:           "google",
				DisplayName:  "Google",
				ClientID:     "client-id",
				ClientSecret: "client-secret",
				IssuerURL:    "https://accounts.google.com",
				ClaimStyle:   "google",
			},
			"authelia": config.Provider{
				ID:           "authelia",
				DisplayName:  "Authelia",
				ClientID:     "a-id",
				ClientSecret: "a-secret",
				IssuerURL:    "https://auth.local",
				ClaimStyle:   "authelia",
			},
		}
		h := newTestHandler(t, registry)

		req := httptest.NewRequest(http.MethodGet, "/v1/auth/providers", nil)
		rec := httptest.NewRecorder()
		h.ListProviders(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var got []map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2", len(got))
		}

		// Verify each entry has exactly the safe fields and nothing else.
		for _, entry := range got {
			if len(entry) != 2 {
				t.Errorf("entry has %d fields, want 2: %v", len(entry), entry)
			}
			if _, ok := entry["id"]; !ok {
				t.Errorf("missing id: %v", entry)
			}
			if _, ok := entry["display_name"]; !ok {
				t.Errorf("missing display_name: %v", entry)
			}
			for _, forbidden := range []string{"client_id", "client_secret", "issuer_url", "claim_style", "scopes"} {
				if _, leaked := entry[forbidden]; leaked {
					t.Errorf("leaked %q in response: %v", forbidden, entry)
				}
			}
		}

		// And the full body string must not contain any secret.
		body := rec.Body.String()
		for _, secret := range []string{"client-secret", "a-secret", "accounts.google.com", "auth.local"} {
			if strings.Contains(body, secret) {
				t.Errorf("response body leaked %q: %s", secret, body)
			}
		}
	})

	t.Run("empty registry returns empty array", func(t *testing.T) {
		h := newTestHandler(t, config.ProviderRegistry{})

		req := httptest.NewRequest(http.MethodGet, "/v1/auth/providers", nil)
		rec := httptest.NewRecorder()
		h.ListProviders(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rec.Code)
		}
		body := strings.TrimSpace(rec.Body.String())
		if body != "[]" {
			t.Errorf("empty registry should return []; got %q", body)
		}
	})
}

func TestStart_UnknownProvider(t *testing.T) {
	h := newTestHandler(t, config.ProviderRegistry{})

	req := setChiProvider(httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/unknown/start", nil), "unknown")
	rec := httptest.NewRecorder()
	h.Start(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCallback_UnknownProvider(t *testing.T) {
	h := newTestHandler(t, config.ProviderRegistry{})

	req := setChiProvider(httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/unknown/callback?code=x&state=y", nil), "unknown")
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestCallback_ProviderError_RedirectsToFrontend(t *testing.T) {
	registry := config.ProviderRegistry{
		"google": config.Provider{ID: "google", DisplayName: "Google"},
	}
	h := newTestHandler(t, registry)

	req := setChiProvider(httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/google/callback?error=access_denied", nil), "google")
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")
	if !strings.Contains(loc, "error=access_denied") {
		t.Errorf("Location should carry error=access_denied, got %q", loc)
	}
	if !strings.HasPrefix(loc, "http://gui.test/auth/oidc/return") {
		t.Errorf("Location should target frontend return URL, got %q", loc)
	}
}

func TestCallback_MissingStateCookie(t *testing.T) {
	registry := config.ProviderRegistry{
		"google": config.Provider{ID: "google", DisplayName: "Google"},
	}
	h := newTestHandler(t, registry)

	req := setChiProvider(httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/google/callback?code=x&state=y", nil), "google")
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCallback_MissingCodeAndState(t *testing.T) {
	registry := config.ProviderRegistry{
		"google": config.Provider{ID: "google", DisplayName: "Google"},
	}
	h := newTestHandler(t, registry)

	req := setChiProvider(httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/google/callback", nil), "google")
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// setChiProvider simulates chi's URL-param injection so handlers that use
// chi.URLParam work with a plain httptest request.
func setChiProvider(r *http.Request, provider string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("provider", provider)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
