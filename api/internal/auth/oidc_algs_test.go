package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/config"
)

func TestResolveOIDCSigningAlgs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		discovered []string
		operator   []string
		want       []string
		wantErr    bool
	}{
		{"EdDSA advertised", []string{"EdDSA"}, nil, []string{"EdDSA"}, false},
		{"RS256 advertised", []string{"RS256"}, nil, []string{"RS256"}, false},
		{"none advertised falls back to RS256", nil, nil, []string{"RS256"}, false},
		{"only unusable advertised falls back to RS256", []string{"none", "HS256", "bogus"}, nil, []string{"RS256"}, false},
		{"none and HS dropped from mixed list", []string{"none", "HS256", "HS512", "EdDSA", "ES256"}, nil, []string{"ES256", "EdDSA"}, false},
		{"deterministic allowlist order", []string{"EdDSA", "PS256", "RS256", "ES384"}, nil, []string{"RS256", "PS256", "ES384", "EdDSA"}, false},
		{"unknown names dropped", []string{"rs256", "EdDSA", "EDDSA"}, nil, []string{"EdDSA"}, false},
		{"operator narrows", []string{"RS256", "EdDSA"}, []string{"EdDSA"}, []string{"EdDSA"}, false},
		{"operator cannot add unadvertised", []string{"RS256"}, []string{"EdDSA"}, nil, true},
		{"operator applies to RS256 fallback", nil, []string{"RS256"}, []string{"RS256"}, false},
		{"operator excludes RS256 fallback", nil, []string{"EdDSA"}, nil, true},
		{"operator HS256 never survives", []string{"HS256"}, []string{"HS256"}, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveOIDCSigningAlgs(tc.discovered, tc.operator)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckOIDCTokenTyp(t *testing.T) {
	t.Parallel()
	_, priv := eddsaKey(t)
	tests := []struct {
		typ     string
		wantErr bool
	}{
		{"", false}, {"JWT", false}, {"jwt", false}, {"aud+jwt", false}, {"AUD+JWT", false},
		{"at+jwt", true}, {"step-up", true}, {"logout+jwt", true}, {"JWT ", true},
	}
	for _, tc := range tests {
		raw := mintEdDSAToken(t, priv, "EdDSA", tc.typ, "k1", map[string]any{"sub": "x"})
		err := checkOIDCTokenTyp(raw)
		if (err != nil) != tc.wantErr {
			t.Errorf("typ %q: err = %v, wantErr %v", tc.typ, err, tc.wantErr)
		}
		if err != nil && strings.Contains(err.Error(), tc.typ) {
			t.Errorf("typ %q: error echoes header value: %v", tc.typ, err)
		}
	}
	if err := checkOIDCTokenTyp("not-a-jwt"); err != nil {
		t.Errorf("malformed token has no readable typ, got %v", err)
	}
}

func TestWithOIDCSigningAlgs_Validation(t *testing.T) {
	t.Parallel()
	for _, algs := range [][]string{nil, {"none"}, {"HS256"}, {"EdDSA", "HS384"}, {"rs256"}, {"bogus"}, {""}} {
		var c verifierConfig
		if err := WithOIDCSigningAlgs(algs...)(&c); err == nil {
			t.Errorf("WithOIDCSigningAlgs(%q) accepted, want error", algs)
		}
	}
	var c verifierConfig
	if err := WithOIDCSigningAlgs("EdDSA", "ES256")(&c); err != nil {
		t.Fatalf("valid algs rejected: %v", err)
	}
	if !reflect.DeepEqual(c.oidcSigningAlgs, []string{"EdDSA", "ES256"}) {
		t.Errorf("oidcSigningAlgs = %v", c.oidcSigningAlgs)
	}
}

func TestVerifier_OIDC_AlgPinning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("HS256 advertised alongside EdDSA is not accepted", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"HS256", "none", "EdDSA"}, "k1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(iss.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		good := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, ""))
		if _, err := v.Verify(ctx, good); err != nil {
			t.Fatalf("EdDSA token rejected: %v", err)
		}
		for _, alg := range []string{"HS256", "none"} {
			bad := mintEdDSAToken(t, iss.keys[0].priv, alg, "JWT", "k1", iss.claims(eddsaIssuerClientID, ""))
			if _, err := v.Verify(ctx, bad); err == nil {
				t.Errorf("alg %s token accepted", alg)
			}
		}
	})

	t.Run("RS256 header over an Ed25519 key is rejected by an EdDSA-only verifier", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(iss.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		bad := mintEdDSAToken(t, iss.keys[0].priv, "RS256", "JWT", "k1", iss.claims(eddsaIssuerClientID, ""))
		if _, err := v.Verify(ctx, bad); err == nil {
			t.Error("RS256-labelled token accepted by EdDSA-only verifier")
		}
	})

	t.Run("EdDSA token rejected when issuer advertises only RS256", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"RS256"}, "k1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(iss.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "JWT", "k1", iss.claims(eddsaIssuerClientID, ""))
		if _, err := v.Verify(ctx, raw); err == nil {
			t.Error("EdDSA token accepted although issuer advertises only RS256")
		}
	})

	t.Run("nothing usable advertised falls back to RS256 only", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, nil, "k1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(iss.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "JWT", "k1", iss.claims(eddsaIssuerClientID, ""))
		if _, err := v.Verify(ctx, raw); err == nil {
			t.Error("EdDSA token accepted under the RS256-only fallback")
		}
	})

	t.Run("operator restriction narrows accepted algorithms", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"RS256", "EdDSA"}, "k1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(iss.URL, eddsaIssuerClientID), WithOIDCSigningAlgs("RS256"))
		if err != nil {
			t.Fatal(err)
		}
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "JWT", "k1", iss.claims(eddsaIssuerClientID, ""))
		if _, err := v.Verify(ctx, raw); err == nil {
			t.Error("EdDSA token accepted although the operator allowed RS256 only")
		}
	})

	t.Run("empty intersection fails construction naming the issuer", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
		_, err := NewVerifierWithOptions(ctx, WithOIDC(iss.URL, eddsaIssuerClientID), WithOIDCSigningAlgs("ES256"))
		if err == nil {
			t.Fatal("construction succeeded with an empty algorithm intersection")
		}
		if !strings.Contains(err.Error(), iss.URL) {
			t.Errorf("error does not name the issuer: %v", err)
		}
	})
}

func TestVerifier_OIDC_TypPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	v, err := NewVerifierWithOptions(ctx, WithOIDC(iss.URL, eddsaIssuerClientID))
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"", "JWT", "jwt", "aud+jwt", "AUD+JWT"} {
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", typ, "k1", iss.claims(eddsaIssuerClientID, ""))
		if _, err := v.Verify(ctx, raw); err != nil {
			t.Errorf("typ %q rejected: %v", typ, err)
		}
	}
	for _, typ := range []string{"at+jwt", "step-up", "logout+jwt"} {
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", typ, "k1", iss.claims(eddsaIssuerClientID, ""))
		if _, err := v.Verify(ctx, raw); err == nil {
			t.Errorf("typ %q accepted", typ)
		}
	}
}

func TestInitProvider_OIDC_AlgPinning(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	provider := func(url string) config.Provider {
		return config.Provider{
			ID: "mfidp", IssuerURL: url, ClientID: eddsaIssuerClientID,
			ClientSecret: "s", ClaimStyle: "generic", Scopes: []string{"openid"},
		}
	}
	cfg := func(algs ...string) *config.Config {
		return &config.Config{Auth: config.AuthConfig{
			OAuthRedirectBaseURL: "http://api.test", OIDCAllowedSigningAlgs: algs,
		}}
	}

	t.Run("HS256 advertised is not accepted", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"HS256", "EdDSA"}, "k1")
		st := initProvider(ctx, "mfidp", provider(iss.URL), cfg())
		if !st.InitOK {
			t.Fatalf("init failed: %v", st.Err)
		}
		bad := mintEdDSAToken(t, iss.keys[0].priv, "HS256", "JWT", "k1", iss.claims(eddsaIssuerClientID, "n"))
		if _, err := st.Verifier.Verify(ctx, bad); err == nil {
			t.Error("HS256 token accepted")
		}
	})

	t.Run("operator restriction applies", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"RS256", "EdDSA"}, "k1")
		st := initProvider(ctx, "mfidp", provider(iss.URL), cfg("RS256"))
		if !st.InitOK {
			t.Fatalf("init failed: %v", st.Err)
		}
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "JWT", "k1", iss.claims(eddsaIssuerClientID, "n"))
		if _, err := st.Verifier.Verify(ctx, raw); err == nil {
			t.Error("EdDSA token accepted although the operator allowed RS256 only")
		}
	})

	t.Run("empty intersection is an init error", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
		st := initProvider(ctx, "mfidp", provider(iss.URL), cfg("ES256"))
		if st.InitOK || st.Err == nil {
			t.Fatal("init succeeded with an empty algorithm intersection")
		}
		if !strings.Contains(st.Err.Error(), iss.URL) {
			t.Errorf("error does not name the issuer: %v", st.Err)
		}
		if st.Verifier != nil {
			t.Error("verifier set on a failed provider")
		}
	})
}

func TestOAuth_Exchange_RejectsDisallowedTyp(t *testing.T) {
	t.Parallel()
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	o, err := NewOAuth(context.Background(), &config.Config{
		Auth:      config.AuthConfig{FrontendReturnURL: "http://gui.test/r", OAuthRedirectBaseURL: "http://api.test"},
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-jwt-secret-for-state-signer"},
		Providers: config.ProviderRegistry{"mfidp": config.Provider{
			ID: "mfidp", DisplayName: "MFIdP", IssuerURL: iss.URL, ClientID: eddsaIssuerClientID,
			ClientSecret: "s", ClaimStyle: "generic", Scopes: []string{"openid", "email"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, stateTok, err := o.AuthorizeURL("mfidp", "/", false, testHandoffChallenge)
	if err != nil {
		t.Fatal(err)
	}
	iss.setIDToken(mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "at+jwt", "k1", iss.claims(eddsaIssuerClientID, "n")))
	_, _, err = o.Exchange(context.Background(), "mfidp", "code", stateTok, stateTok)
	if err == nil || !strings.Contains(err.Error(), "unsupported token typ") {
		t.Fatalf("Exchange error = %v, want unsupported token typ", err)
	}
}

// mintEdDSATokenRawHeader signs claims under a hand-built protected header so
// a test can set header members the mint helper cannot express.
func mintEdDSATokenRawHeader(t *testing.T, priv ed25519.PrivateKey, header string, claims string) string {
	t.Helper()
	input := base64.RawURLEncoding.EncodeToString([]byte(header)) + "." + base64.RawURLEncoding.EncodeToString([]byte(claims))
	return input + "." + base64.RawURLEncoding.EncodeToString(ed25519.Sign(priv, []byte(input)))
}

var nonStringTypHeaders = map[string]string{
	"array":  `{"alg":"EdDSA","kid":"k1","typ":["JWT"]}`,
	"number": `{"alg":"EdDSA","kid":"k1","typ":7}`,
	"object": `{"alg":"EdDSA","kid":"k1","typ":{"v":"JWT"}}`,
	"bool":   `{"alg":"EdDSA","kid":"k1","typ":true}`,
	"null":   `{"alg":"EdDSA","kid":"k1","typ":null}`,
}

func TestCheckOIDCTokenTyp_FailsClosed(t *testing.T) {
	t.Parallel()
	_, priv := eddsaKey(t)
	for name, hdr := range nonStringTypHeaders {
		raw := mintEdDSATokenRawHeader(t, priv, hdr, `{"sub":"x"}`)
		err := checkOIDCTokenTyp(raw)
		if err != errOIDCTokenTyp {
			t.Errorf("%s typ: err = %v, want errOIDCTokenTyp", name, err)
		}
	}
	undecodable := map[string]string{
		"bad base64": "!!!notbase64!!!.e30.c2ln",
		"bad json":   base64.RawURLEncoding.EncodeToString([]byte("{not json")) + ".e30.c2ln",
		"not object": base64.RawURLEncoding.EncodeToString([]byte(`"secret-header"`)) + ".e30.c2ln",
	}
	for name, raw := range undecodable {
		err := checkOIDCTokenTyp(raw)
		if err != errOIDCTokenTyp {
			t.Errorf("%s: err = %v, want errOIDCTokenTyp", name, err)
			continue
		}
		if strings.Contains(err.Error(), "secret-header") || strings.Contains(err.Error(), "notbase64") {
			t.Errorf("%s: error echoes header content: %v", name, err)
		}
	}
}

func TestVerifier_OIDC_RejectsNonStringTyp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	v, err := NewVerifierWithOptions(ctx, WithOIDC(iss.URL, eddsaIssuerClientID))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(iss.claims(eddsaIssuerClientID, ""))
	if err != nil {
		t.Fatal(err)
	}
	for name, hdr := range nonStringTypHeaders {
		raw := mintEdDSATokenRawHeader(t, iss.keys[0].priv, hdr, string(claims))
		if _, err := v.Verify(ctx, raw); err == nil {
			t.Errorf("%s typ accepted by Verify", name)
		}
	}
}

func TestOAuth_Exchange_RejectsNonStringTyp(t *testing.T) {
	t.Parallel()
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	o, err := NewOAuth(context.Background(), &config.Config{
		Auth:      config.AuthConfig{FrontendReturnURL: "http://gui.test/r", OAuthRedirectBaseURL: "http://api.test"},
		LocalAuth: config.LocalAuthConfig{JWTSecret: "test-jwt-secret-for-state-signer"},
		Providers: config.ProviderRegistry{"mfidp": config.Provider{
			ID: "mfidp", DisplayName: "MFIdP", IssuerURL: iss.URL, ClientID: eddsaIssuerClientID,
			ClientSecret: "s", ClaimStyle: "generic", Scopes: []string{"openid", "email"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := json.Marshal(iss.claims(eddsaIssuerClientID, "n"))
	if err != nil {
		t.Fatal(err)
	}
	for name, hdr := range nonStringTypHeaders {
		_, stateTok, err := o.AuthorizeURL("mfidp", "/", false, testHandoffChallenge)
		if err != nil {
			t.Fatal(err)
		}
		iss.setIDToken(mintEdDSATokenRawHeader(t, iss.keys[0].priv, hdr, string(claims)))
		_, _, err = o.Exchange(context.Background(), "mfidp", "code", stateTok, stateTok)
		if err == nil || !strings.Contains(err.Error(), "unsupported token typ") {
			t.Errorf("%s typ: Exchange error = %v, want unsupported token typ", name, err)
		}
	}
}
