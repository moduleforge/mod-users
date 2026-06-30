package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
)

// newTestOAuth stands up a minimal OAuth struct suitable for handler-level
// tests. The States map carries a ProviderState for each registry entry,
// marked InitOK=true so ProviderAvailable() reports the provider as usable,
// but with nil Verifier/OAuthCfg/Mapper — these tests never exercise
// Exchange's network path, and the branches they cover short-circuit before
// any of those are dereferenced.
func newTestOAuth(t *testing.T, registry config.ProviderRegistry) *localauth.OAuth {
	t.Helper()
	signer, err := localauth.NewStateSigner([]byte("test-secret-at-least-32-bytes-xx"))
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}
	states := make(map[string]*localauth.ProviderState, len(registry))
	for id, p := range registry {
		states[id] = &localauth.ProviderState{
			ID:       id,
			Provider: p,
			InitOK:   true,
		}
	}
	return &localauth.OAuth{
		States:            states,
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
	return NewOIDCHandler(nil, newTestOAuth(t, registry), nil, noopLoginRecorder{}, cfg)
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

	// Pin cookie-cleanup: every callback path must tombstone the state cookie
	// so it cannot be reused. A future refactor that drops h.clearStateCookie
	// would silently regress this without the assertion.
	assertStateCookieDeleted(t, rec)
}

// TestCallback_MissingStateCookie_ClearsCookie exercises a second callback
// branch (missing cookie → 400) and confirms the cleanup header is present
// there too. The cookie is cleared before the branch is decided, so any
// callback outcome ought to include it.
func TestCallback_MissingStateCookie_ClearsCookie(t *testing.T) {
	registry := config.ProviderRegistry{
		"google": config.Provider{ID: "google", DisplayName: "Google"},
	}
	h := newTestHandler(t, registry)

	req := setChiProvider(httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/google/callback?code=x&state=y", nil), "google")
	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	assertStateCookieDeleted(t, rec)
}

// TestRedirectToTestResult_URLShape pins the query-param shape consumed
// by the /oidc-config banner: success carries email/sub/issuer,
// failure carries test_error, both carry test_provider + test_result.
func TestRedirectToTestResult_URLShape(t *testing.T) {
	registry := config.ProviderRegistry{"google": config.Provider{ID: "google"}}
	h := newTestHandler(t, registry)

	t.Run("success", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/google/callback", nil)
		h.redirectToTestResult(rec, req, "google", "zane@example.test", "abc-123", "https://accounts.google.com", "")
		if rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302", rec.Code)
		}
		loc := rec.Header().Get("Location")
		for _, want := range []string{
			"/oidc-config",
			"test_result=ok",
			"test_provider=google",
			"test_email=zane%40example.test",
			"test_sub=abc-123",
			"test_issuer=https%3A%2F%2Faccounts.google.com",
		} {
			if !strings.Contains(loc, want) {
				t.Errorf("Location missing %q: %s", want, loc)
			}
		}
		if strings.Contains(loc, "test_error") {
			t.Errorf("success should not include test_error: %s", loc)
		}
	})

	t.Run("failure", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/google/callback", nil)
		h.redirectToTestResult(rec, req, "microsoft", "", "", "", "issuer mismatch: got X, want Y")
		loc := rec.Header().Get("Location")
		for _, want := range []string{
			"/oidc-config",
			"test_result=fail",
			"test_provider=microsoft",
			"test_error=issuer+mismatch",
		} {
			if !strings.Contains(loc, want) {
				t.Errorf("Location missing %q: %s", want, loc)
			}
		}
		for _, absent := range []string{"test_email=", "test_sub=", "test_issuer="} {
			if strings.Contains(loc, absent) {
				t.Errorf("failure should not include %q: %s", absent, loc)
			}
		}
	})
}

// TestNormalizeProviderID verifies the path-param lowercasing used by Start
// and Callback so a URL like /v1/auth/oidc/Google/start still hits the
// "google" registry entry. Exercising the helper directly keeps this test
// decoupled from the full Start/Callback plumbing, which would otherwise
// fail for unrelated reasons (nil OAuthCfg in the handler-level stub).
func TestNormalizeProviderID(t *testing.T) {
	cases := map[string]string{
		"Google":    "google",
		"MICROSOFT": "microsoft",
		"authelia":  "authelia",
		"":          "",
	}
	for in, want := range cases {
		req := setChiProvider(httptest.NewRequest(http.MethodGet, "/v1/auth/oidc/x/start", nil), in)
		if got := normalizeProviderID(req); got != want {
			t.Errorf("normalizeProviderID(%q) = %q, want %q", in, got, want)
		}
	}
}

// assertStateCookieDeleted verifies the response sets the oidc_state cookie
// with an empty value and a non-positive MaxAge, which is how net/http
// signals "delete this cookie".
func assertStateCookieDeleted(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var found *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "oidc_state" {
			found = c
			break
		}
	}
	if found == nil {
		t.Fatal("expected oidc_state cookie deletion header, found none")
	}
	if found.Value != "" {
		t.Errorf("state cookie Value = %q, want empty", found.Value)
	}
	if found.MaxAge >= 0 {
		t.Errorf("state cookie MaxAge = %d, want negative", found.MaxAge)
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

// stubResolver is a test double for userResolver that always returns the
// configured error (or nil if err is nil, in which case uc is returned).
type stubResolver struct {
	err error
	uc  *localauth.UserContext
}

func (s *stubResolver) Resolve(_ context.Context, _ localauth.Principal) (*localauth.UserContext, error) {
	return s.uc, s.err
}

// noopLoginRecorder is a test double for loginRecorder that always succeeds
// and records nothing. Used in tests that exercise paths before or after
// the RecordLogin call, or where the login audit is not the focus.
type noopLoginRecorder struct{}

func (noopLoginRecorder) RecordLogin(_ context.Context, _ int64, _ string) error { return nil }

// TestOIDC_Callback_ResolverDBError_Returns500 verifies that a non-auth error
// from the resolver (e.g., a DB failure during auto-create) surfaces as an
// HTTP 500 with "internal_error" rather than being silently mapped to a
// redirect pretending authentication failed.
func TestOIDC_Callback_ResolverDBError_Returns500(t *testing.T) {
	const providerID = "google"

	// Build a StateSigner so we can mint a valid state token.
	signer, err := localauth.NewStateSigner([]byte("test-secret-at-least-32-bytes-xx"))
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}

	// Sign a state token for the google provider.
	payload := localauth.StatePayload{
		Provider:   providerID,
		ReturnPath: "/",
		Nonce:      "testnonce",
		Expires:    time.Now().Add(5 * time.Minute).Unix(),
	}
	stateToken, err := signer.Sign(payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Build an OAuth whose Exchange is stubbed to return a valid principal
	// so the handler gets past the token-exchange step and reaches the resolver.
	registry := config.ProviderRegistry{
		providerID: config.Provider{ID: providerID, DisplayName: "Google"},
	}
	oauth := newTestOAuth(t, registry)
	oauth.ExchangeFn = func(_ context.Context, _, _, _, _ string) (localauth.Principal, localauth.StatePayload, error) {
		return localauth.Principal{
			Email:   "user@example.com",
			Subject: "sub-123",
			Issuer:  "https://accounts.google.com",
		}, payload, nil
	}

	// Stub resolver that returns a non-ErrUserGone error (simulates a DB crash
	// during auto-create).
	resolver := &stubResolver{err: errors.New("db: connection refused")}

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
	h := NewOIDCHandler(nil, oauth, resolver, noopLoginRecorder{}, cfg)

	// Build the callback request with matching state in both query and cookie.
	target := "/v1/auth/oidc/" + providerID + "/callback?code=testcode&state=" + stateToken
	req := setChiProvider(httptest.NewRequest(http.MethodGet, target, nil), providerID)
	req.AddCookie(&http.Cookie{Name: stateCookieName, Value: stateToken})

	rec := httptest.NewRecorder()
	h.Callback(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500; body = %s", rec.Code, rec.Body.String())
	}

	// Assert the body has the expected error structure produced by server.Error.
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal body: %v; raw=%s", err, rec.Body.String())
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("body[\"error\"] is not an object: %v", body)
	}
	if got := errObj["code"]; got != "internal_error" {
		t.Errorf("error.code = %q, want \"internal_error\"", got)
	}

	// Assert no redirect — response must not be a 3xx.
	if rec.Code/100 == 3 {
		t.Errorf("response must not be a redirect (3xx), got %d location=%s", rec.Code, rec.Header().Get("Location"))
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location header must be absent for a 500 response, got %q", loc)
	}
}
