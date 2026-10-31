package auth

import (
	"context"
	"crypto/ed25519"
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

	"github.com/moduleforge/mod-users/api/internal/config"
)

// TestVerifier_RejectsExternalOIDC is a Phase-9 regression pin. Before
// Phase 9, RequireAuth was expected to accept either a local JWT or
// a provider-issued id_token (RS256 verified via JWKS). Post-Phase 9, the
// OAuth callback trades the id_token for a local session internally, so
// user-facing endpoints should only ever see local EdDSA tokens. Any
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
	// local EdDSA tokens should verify.
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	verifier, err := NewLocalVerifier(context.Background(), config.LocalAuthConfig{
		JWTSigningKey: edKey, LocalIssuer: "users-module-local",
	})
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}

	if _, err := verifier.Verify(context.Background(), raw); err == nil {
		t.Fatal("expected external OIDC token to be rejected; got nil error (Phase-9 regression)")
	}
}

// TestVerifier_LocalEdDSAOnly rounds out the Phase-9 scope pin: with the
// same Verifier config, a locally-minted EdDSA token verifies cleanly and an
// HMAC token with otherwise identical claims does not.
func TestVerifier_LocalEdDSAOnly(t *testing.T) {
	const issuer = "users-module-local"
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	claims := jwt.MapClaims{
		"iss": issuer,
		"sub": "00000000-0000-0000-0000-000000000001",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	signer, err := NewEdDSASigner(key, "")
	if err != nil {
		t.Fatalf("NewEdDSASigner: %v", err)
	}
	raw, err := signer.Sign(claims)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	hmacRaw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("local-secret"))
	if err != nil {
		t.Fatalf("hmac sign: %v", err)
	}

	verifier, err := NewLocalVerifier(context.Background(), config.LocalAuthConfig{JWTSigningKey: key, LocalIssuer: issuer})
	if err != nil {
		t.Fatalf("NewLocalVerifier: %v", err)
	}

	got, err := verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got["iss"] != issuer {
		t.Errorf("iss = %v, want %q", got["iss"], issuer)
	}
	if _, err := verifier.Verify(context.Background(), hmacRaw); err == nil {
		t.Error("an HMAC-signed token must be rejected")
	}
}
