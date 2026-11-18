package auth

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/config"
)

func newEdDSAOAuth(t *testing.T, iss *eddsaIssuer, claimStyle string) *OAuth {
	t.Helper()
	cfg := &config.Config{
		Auth:      config.AuthConfig{FrontendReturnURL: "http://gui.test/auth/oidc/return", OAuthRedirectBaseURL: "http://api.test"},
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-jwt-secret-for-state-signer"},
		Providers: config.ProviderRegistry{"mfidp": config.Provider{
			ID: "mfidp", DisplayName: "MFIdP", IssuerURL: iss.URL, ClientID: eddsaIssuerClientID,
			ClientSecret: "s", ClaimStyle: claimStyle, Scopes: []string{"openid", "email"},
		}},
	}
	o, err := NewOAuth(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}
	return o
}

// authorizeNonce starts a login at the provider and returns the state token
// and the nonce the authorization URL carries.
func authorizeNonce(t *testing.T, o *OAuth) (stateTok, nonce string) {
	t.Helper()
	authURL, stateTok, err := o.AuthorizeURL("mfidp", "/profile", false, testHandoffChallenge)
	if err != nil {
		t.Fatalf("AuthorizeURL: %v", err)
	}
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	nonce = u.Query().Get("nonce")
	if nonce == "" {
		t.Fatal("authorization URL has no nonce")
	}
	return stateTok, nonce
}

// The whole login path against an issuer that signs EdDSA aud+jwt ID tokens:
// discovery, authorization URL with a nonce, code exchange, ID token
// verification and claim mapping. The provider uses the production claim
// mapping built by initProvider, which for the generic style has no claim
// paths configured and therefore cannot map; the google style reads
// sub, iss, email and email_verified directly.
func TestOAuth_EndToEnd_EdDSA(t *testing.T) {
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	o := newEdDSAOAuth(t, iss, "google")
	stateTok, nonce := authorizeNonce(t, o)

	iss.setIDToken(mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, nonce)))
	id, payload, err := o.Exchange(context.Background(), "mfidp", "code", stateTok, stateTok)
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if id.Subject != "owner-1" || id.Issuer != iss.URL || id.Email != "owner@example.com" || !id.EmailVerified {
		t.Errorf("identity = %+v", id)
	}
	if payload.ReturnPath != "/profile" || payload.Provider != "mfidp" {
		t.Errorf("payload = %+v", payload)
	}
}

// A signature-valid EdDSA token carrying a nonce other than the one the
// authorization URL carried is refused.
func TestOAuth_Exchange_EdDSANonceMismatch(t *testing.T) {
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	o := newEdDSAOAuth(t, iss, "google")
	stateTok, _ := authorizeNonce(t, o)

	iss.setIDToken(mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, "not-the-nonce")))
	_, _, err := o.Exchange(context.Background(), "mfidp", "code", stateTok, stateTok)
	if err == nil || !strings.Contains(err.Error(), "nonce mismatch") {
		t.Fatalf("Exchange error = %v, want nonce mismatch", err)
	}
}

// The generic claim style is built with no claim paths, so even a valid
// EdDSA token with the right nonce fails at the claim-mapping step.
func TestOAuth_Exchange_EdDSAGenericStyleHasNoClaimPaths(t *testing.T) {
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	o := newEdDSAOAuth(t, iss, "generic")
	stateTok, nonce := authorizeNonce(t, o)

	iss.setIDToken(mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, nonce)))
	_, _, err := o.Exchange(context.Background(), "mfidp", "code", stateTok, stateTok)
	if err == nil || !strings.Contains(err.Error(), "map claims") {
		t.Fatalf("Exchange error = %v, want a claim-mapping failure", err)
	}
}
