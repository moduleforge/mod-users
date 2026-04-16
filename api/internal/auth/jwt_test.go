package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TestVerifier_RejectsExternalOIDC is a Phase-9 regression pin. Before
// Phase 9, RequireAuth was expected to accept either a local HS256 JWT or
// a provider-issued id_token (RS256 verified via JWKS). Post-Phase 9, the
// OAuth callback trades the id_token for a local JWT internally, so
// user-facing endpoints should only ever see local HS256 tokens. Any
// accidental re-enabling of external verification in RequireAuth's Verifier
// would be a silent regression on that architectural decision.
//
// This test stands up a fake OIDC provider (so JWKS discovery could succeed
// if we wired it) but constructs the Verifier with an empty issuer URL —
// mirroring how main.go wires it in cmd/server/main.go — and then hands it a
// perfectly valid RS256 id_token. The token must be rejected.
func TestVerifier_RejectsExternalOIDC(t *testing.T) {
	signingKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	const keyID = "ext-key"

	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		cfg := map[string]any{
			"issuer":                                srv.URL,
			"jwks_uri":                              srv.URL + "/jwks",
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		_ = json.NewEncoder(w).Encode(cfg)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(signingKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(signingKey.E)).Bytes())
		jwks := map[string]any{
			"keys": []map[string]any{
				{"kty": "RSA", "alg": "RS256", "use": "sig", "kid": keyID, "n": n, "e": e},
			},
		}
		_ = json.NewEncoder(w).Encode(jwks)
	})

	// Mint a valid RS256 id_token.
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss":   srv.URL,
		"aud":   "ext-client",
		"sub":   "external-sub-1",
		"email": "user@example.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = keyID
	raw, err := tok.SignedString(signingKey)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Build the verifier the way main.go does — no OIDC issuer, so only
	// local HS256 tokens should verify.
	verifier, err := NewVerifier(context.Background(), "", "", "local-secret", "users-module-local")
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	if _, err := verifier.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected external OIDC token to be rejected; got nil error (Phase-9 regression)")
	}
}

// TestVerifier_AcceptsLocalHS256 rounds out the Phase-9 scope pin: with the
// same Verifier config, a locally-minted HS256 token should verify cleanly.
func TestVerifier_AcceptsLocalHS256(t *testing.T) {
	const secret = "local-secret-for-test"
	const issuer = "users-module-local"

	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss":   issuer,
		"sub":   "00000000-0000-0000-0000-000000000001",
		"email": "u@example.com",
		"iat":   time.Now().Unix(),
		"exp":   time.Now().Add(time.Hour).Unix(),
	})
	raw, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	verifier, err := NewVerifier(context.Background(), "", "", secret, issuer)
	if err != nil {
		t.Fatalf("NewVerifier: %v", err)
	}

	claims, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["iss"] != issuer {
		t.Errorf("iss = %v, want %q", claims["iss"], issuer)
	}
}
