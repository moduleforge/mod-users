package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// eddsaIssuerKey is one Ed25519 signing key published by an eddsaIssuer.
type eddsaIssuerKey struct {
	kid  string
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
}

// eddsaIssuer is an httptest OIDC issuer that signs with Ed25519 keys,
// publishes them as OKP JWKS entries, and advertises configurable signing
// algorithms in discovery. Its /token endpoint returns whatever ID token
// idToken currently holds.
type eddsaIssuer struct {
	*httptest.Server

	mu      sync.Mutex
	algs    []string
	keys    []eddsaIssuerKey
	idToken string
}

// newEdDSAIssuer starts an issuer advertising algs in discovery and
// publishing one freshly generated Ed25519 key per kid.
func newEdDSAIssuer(t *testing.T, algs []string, kids ...string) *eddsaIssuer {
	t.Helper()
	iss := &eddsaIssuer{algs: algs}
	for _, kid := range kids {
		iss.addKey(t, kid)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		iss.mu.Lock()
		doc := map[string]any{
			"issuer":                                iss.URL,
			"authorization_endpoint":                iss.URL + "/authorize",
			"token_endpoint":                        iss.URL + "/token",
			"jwks_uri":                              iss.URL + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": iss.algs,
		}
		iss.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(doc)
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		iss.mu.Lock()
		keys := make([]map[string]any, 0, len(iss.keys))
		for _, k := range iss.keys {
			keys = append(keys, map[string]any{
				"kty": "OKP", "crv": "Ed25519", "use": "sig", "kid": k.kid,
				"x": base64.RawURLEncoding.EncodeToString(k.pub),
			})
		}
		iss.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		iss.mu.Lock()
		tok := iss.idToken
		iss.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"id_token":     tok,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})
	iss.Server = httptest.NewServer(mux)
	t.Cleanup(iss.Close)
	return iss
}

// addKey generates and publishes another Ed25519 key under kid.
func (i *eddsaIssuer) addKey(t *testing.T, kid string) eddsaIssuerKey {
	t.Helper()
	pub, priv := eddsaKey(t)
	k := eddsaIssuerKey{kid: kid, pub: pub, priv: priv}
	i.mu.Lock()
	i.keys = append(i.keys, k)
	i.mu.Unlock()
	return k
}

// setIDToken sets the ID token the /token endpoint returns.
func (i *eddsaIssuer) setIDToken(tok string) {
	i.mu.Lock()
	i.idToken = tok
	i.mu.Unlock()
}

// claims returns a valid ID token claim set for aud with the issuer's URL as
// iss. Nonce is omitted when empty.
func (i *eddsaIssuer) claims(aud, nonce string) map[string]any {
	c := map[string]any{
		"iss":            i.URL,
		"sub":            "owner-1",
		"aud":            aud,
		"email":          "owner@example.com",
		"email_verified": true,
		"iat":            time.Now().Unix(),
		"exp":            time.Now().Add(5 * time.Minute).Unix(),
	}
	if nonce != "" {
		c["nonce"] = nonce
	}
	return c
}

// mintEdDSAToken signs claims with priv using a header carrying the given
// alg, typ and kid (each omitted when empty). The signature is always
// Ed25519 over the signing input, regardless of the alg header.
func mintEdDSAToken(t *testing.T, priv ed25519.PrivateKey, alg, typ, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{}
	if alg != "" {
		header["alg"] = alg
	}
	if typ != "" {
		header["typ"] = typ
	}
	if kid != "" {
		header["kid"] = kid
	}
	hb, err := json.Marshal(header)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	input := base64.RawURLEncoding.EncodeToString(hb) + "." + base64.RawURLEncoding.EncodeToString(cb)
	sig := ed25519.Sign(priv, []byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

const eddsaIssuerClientID = "mftodo-client"

// Baseline: the facade verifier accepts an EdDSA aud+jwt token from an issuer
// that advertises EdDSA and publishes an OKP JWKS.
func TestVerifier_OIDC_EdDSAAudienceToken(t *testing.T) {
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	key := iss.keys[0]
	raw := mintEdDSAToken(t, key.priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, ""))

	v, err := NewVerifierWithOptions(context.Background(), WithOIDC(iss.URL, eddsaIssuerClientID))
	if err != nil {
		t.Fatalf("NewVerifierWithOptions: %v", err)
	}
	if _, err := v.oidcVerifier.Verify(context.Background(), raw); err != nil {
		t.Fatalf("oidc verifier rejected EdDSA aud+jwt token: %v", err)
	}
	claims, err := v.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if claims["sub"] != "owner-1" {
		t.Errorf("sub = %v", claims["sub"])
	}
}

// Baseline for the login path: the verifier built by initProvider accepts the
// same token.
func TestInitProvider_OIDC_EdDSAAudienceToken(t *testing.T) {
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, "n1"))

	state := initProvider(context.Background(), "mfidp", config.Provider{
		ID: "mfidp", IssuerURL: iss.URL, ClientID: eddsaIssuerClientID,
		ClientSecret: "s", ClaimStyle: "generic", Scopes: []string{"openid"},
	}, &config.Config{Auth: config.AuthConfig{OAuthRedirectBaseURL: "http://api.test"}})
	if !state.InitOK {
		t.Fatalf("initProvider failed: %v", state.Err)
	}
	idToken, err := state.Verifier.Verify(context.Background(), raw)
	if err != nil {
		t.Fatalf("login-path verifier rejected EdDSA aud+jwt token: %v", err)
	}
	if idToken.Nonce != "n1" {
		t.Errorf("nonce = %q", idToken.Nonce)
	}
}

// Baseline: an ID token without a nonce claim fails Exchange with a nonce
// mismatch even though the signature verifies.
func TestOAuth_Exchange_EdDSAIDTokenWithoutNonce(t *testing.T) {
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	cfg := &config.Config{
		Auth:      config.AuthConfig{FrontendReturnURL: "http://gui.test/auth/oidc/return", OAuthRedirectBaseURL: "http://api.test"},
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-jwt-secret-for-state-signer"},
		Providers: config.ProviderRegistry{"mfidp": config.Provider{
			ID: "mfidp", DisplayName: "MFIdP", IssuerURL: iss.URL, ClientID: eddsaIssuerClientID,
			ClientSecret: "s", ClaimStyle: "generic", Scopes: []string{"openid", "email"},
		}},
	}
	o, err := NewOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}
	authURL, stateTok, err := o.AuthorizeURL("mfidp", "/", false, testHandoffChallenge)
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, _ := url.Parse(authURL)
	if u.Query().Get("nonce") == "" {
		t.Fatal("authorization URL has no nonce")
	}
	iss.setIDToken(mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, "")))

	_, _, err = o.Exchange(context.Background(), "mfidp", "code", stateTok, stateTok)
	if err == nil || !strings.Contains(err.Error(), "nonce mismatch") {
		t.Fatalf("Exchange error = %v, want nonce mismatch", err)
	}
}
