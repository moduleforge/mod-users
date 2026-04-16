package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/moduleforge/users-module/api/internal/config"
)

func TestValidateReturnPath(t *testing.T) {
	okCases := map[string]string{
		"empty defaults to root": "",
		"simple path":            "/profile",
		"path with query":        "/profile?tab=security",
		"nested":                 "/orgs/foo/users",
	}

	for name, input := range okCases {
		t.Run("accept/"+name, func(t *testing.T) {
			got, err := validateReturnPath(input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Empty normalizes to "/".
			if input == "" && got != "/" {
				t.Errorf("empty input should normalize to %q, got %q", "/", got)
			}
			if input != "" && got != input {
				t.Errorf("got %q, want %q", got, input)
			}
		})
	}

	rejectCases := map[string]string{
		"absolute http":     "http://evil.com/",
		"absolute https":    "https://evil.com/path",
		"protocol-relative": "//evil.com/path",
		"javascript scheme": "javascript:alert(1)",
		"no leading slash":  "profile",
		"scheme no slashes": "data:text/plain,hi",
	}

	for name, input := range rejectCases {
		t.Run("reject/"+name, func(t *testing.T) {
			if _, err := validateReturnPath(input); err == nil {
				t.Errorf("expected error for %q, got nil", input)
			}
		})
	}
}

// TestOAuth_EndToEnd walks through AuthorizeURL → token exchange → ID-token
// verification → Principal mapping, using a fully-local fake OIDC provider.
// A correctly-signed id_token with the expected issuer/audience/nonce passes
// all checks; this pins down that our wiring matches the protocol.
func TestOAuth_EndToEnd(t *testing.T) {
	// 1. Generate a throwaway RSA key used to sign id_tokens.
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	const keyID = "test-key-1"

	// 2. Stand up a fake OIDC provider. Shared state between discovery and
	// token handlers is captured in closures; the issuer URL needed for the
	// discovery document has to match the server's final URL, so we wire it
	// up after the test server is started.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	clientID := "test-client-id"
	clientSecret := "test-client-secret"
	expectedCode := "test-auth-code"
	expectedSubject := "google-sub-123"
	expectedEmail := "user@example.com"

	// Shared across handlers.
	var lastNonce string

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		cfg := map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cfg)
	})

	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(signingKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signingKey.E)).Bytes())
		jwks := map[string]any{
			"keys": []map[string]any{
				{
					"kty": "RSA",
					"alg": "RS256",
					"use": "sig",
					"kid": keyID,
					"n":   n,
					"e":   e,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	})

	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if r.Form.Get("code") != expectedCode {
			http.Error(w, "bad code", 400)
			return
		}
		if r.Form.Get("client_id") != clientID {
			http.Error(w, "bad client", 400)
			return
		}

		idToken, err := signIDToken(signingKey, keyID, jwt.MapClaims{
			"iss":   srv.URL,
			"aud":   clientID,
			"sub":   expectedSubject,
			"email": expectedEmail,
			"nonce": lastNonce,
			"iat":   time.Now().Unix(),
			"exp":   time.Now().Add(time.Hour).Unix(),
		})
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		resp := map[string]any{
			"access_token": "test-access-token",
			"id_token":     idToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})

	// 3. Build a Config pointing at the fake provider.
	cfg := &config.Config{
		Auth: config.AuthConfig{
			AdminRole:            "admin",
			FrontendReturnURL:    "http://gui.test/auth/oidc/return",
			OAuthRedirectBaseURL: "http://api.test",
		},
		LocalAuth: config.LocalAuthConfig{
			JWTSecret: "test-jwt-secret-for-state-signer",
		},
		Providers: config.ProviderRegistry{
			"google": config.Provider{
				ID:           "google",
				DisplayName:  "Google",
				IssuerURL:    srv.URL,
				ClientID:     clientID,
				ClientSecret: clientSecret,
				ClaimStyle:   "google",
				Scopes:       []string{"openid", "email", "profile"},
			},
		},
	}

	oauth, err := NewOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}

	// 4. Drive AuthorizeURL → capture the nonce and state for the mock token
	//    endpoint to echo back.
	authURL, stateToken, err := oauth.AuthorizeURL("google", "/profile")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authURL: %v", err)
	}
	if parsed.Query().Get("state") != stateToken {
		t.Errorf("authURL state = %q, want %q", parsed.Query().Get("state"), stateToken)
	}
	lastNonce = parsed.Query().Get("nonce")
	if lastNonce == "" {
		t.Fatal("authURL did not include a nonce")
	}

	// 5. Exchange with a matching state cookie.
	principal, payload, err := oauth.Exchange(context.Background(), "google", expectedCode, stateToken, stateToken)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}

	if principal.Subject != expectedSubject {
		t.Errorf("Subject = %q, want %q", principal.Subject, expectedSubject)
	}
	if principal.Issuer != srv.URL {
		t.Errorf("Issuer = %q, want %q", principal.Issuer, srv.URL)
	}
	if principal.Email != expectedEmail {
		t.Errorf("Email = %q, want %q", principal.Email, expectedEmail)
	}
	if payload.ReturnPath != "/profile" {
		t.Errorf("payload.ReturnPath = %q, want /profile", payload.ReturnPath)
	}
}

func TestOAuth_Exchange_StateCookieMismatch(t *testing.T) {
	oauth := newOAuthForStateOnlyTest(t)

	// Generate a valid state token for one return path.
	authURL, state, err := oauth.AuthorizeURL("google", "/profile")
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	_ = authURL

	_, _, err = oauth.Exchange(context.Background(), "google", "code", state, "different-cookie")
	if err == nil || !strings.Contains(err.Error(), "state") {
		t.Errorf("expected state mismatch error, got %v", err)
	}
}

func TestOAuth_Exchange_MissingState(t *testing.T) {
	oauth := newOAuthForStateOnlyTest(t)

	_, _, err := oauth.Exchange(context.Background(), "google", "code", "", "")
	if err == nil {
		t.Fatal("expected error for missing state")
	}
}

func TestOAuth_AuthorizeURL_UnknownProvider(t *testing.T) {
	oauth := newOAuthForStateOnlyTest(t)
	_, _, err := oauth.AuthorizeURL("unknown", "/")
	if err == nil {
		t.Fatal("expected ErrUnknownProvider")
	}
}

// newOAuthForStateOnlyTest builds an OAuth with a single bogus provider whose
// issuer points nowhere — enough for state/cookie validation to run but not
// enough to execute a real token exchange. Tests that need a working exchange
// use the fake server setup in TestOAuth_EndToEnd instead.
func newOAuthForStateOnlyTest(t *testing.T) *OAuth {
	t.Helper()

	// Stand up a stub discovery endpoint so NewOAuth doesn't fail.
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w,
			`{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q,"response_types_supported":["code"],"subject_types_supported":["public"],"id_token_signing_alg_values_supported":["RS256"]}`,
			srv.URL, srv.URL+"/authorize", srv.URL+"/token", srv.URL+"/jwks")
	})

	cfg := &config.Config{
		Auth: config.AuthConfig{
			AdminRole:            "admin",
			FrontendReturnURL:    "http://gui.test/return",
			OAuthRedirectBaseURL: "http://api.test",
		},
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-secret"},
		Providers: config.ProviderRegistry{
			"google": config.Provider{
				ID:           "google",
				DisplayName:  "Google",
				IssuerURL:    srv.URL,
				ClientID:     "test-client",
				ClientSecret: "test-secret",
				ClaimStyle:   "google",
				Scopes:       []string{"openid", "email", "profile"},
			},
		},
	}

	oauth, err := NewOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}
	return oauth
}

// signIDToken produces an RS256-signed JWT with the given claims. The kid
// header matches what the /jwks endpoint publishes so the verifier accepts it.
func signIDToken(key *rsa.PrivateKey, kid string, claims jwt.MapClaims) (string, error) {
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	return tok.SignedString(key)
}
