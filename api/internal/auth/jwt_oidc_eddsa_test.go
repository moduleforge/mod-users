package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// newOIDCVerifier builds a facade verifier for iss.
func newOIDCVerifier(t *testing.T, iss *eddsaIssuer, opts ...VerifierOption) *Verifier {
	t.Helper()
	v, err := NewVerifierWithOptions(context.Background(), append([]VerifierOption{WithOIDC(iss.URL, eddsaIssuerClientID)}, opts...)...)
	if err != nil {
		t.Fatalf("NewVerifierWithOptions: %v", err)
	}
	return v
}

// mustRejectClean asserts the verifier rejects raw and that the error text
// carries no token content.
func mustRejectClean(t *testing.T, v *Verifier, raw, why string) {
	t.Helper()
	_, err := v.Verify(context.Background(), raw)
	if err == nil {
		t.Errorf("token accepted, want rejection: %s", why)
		return
	}
	assertNoTokenContent(t, err, raw, why)
}

func assertNoTokenContent(t *testing.T, err error, raw, why string) {
	t.Helper()
	msg := err.Error()
	if strings.Contains(msg, raw) {
		t.Errorf("%s: error contains the token", why)
	}
	for _, part := range strings.Split(raw, ".") {
		if part != "" && strings.Contains(msg, part) {
			t.Errorf("%s: error contains a token segment", why)
		}
	}
	for _, s := range []string{"owner@example.com", "owner-1"} {
		if strings.Contains(msg, s) {
			t.Errorf("%s: error contains claim value %q", why, s)
		}
	}
}

func TestVerifier_OIDC_EdDSA_Accepts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("claims returned", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
		v := newOIDCVerifier(t, iss)
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, ""))
		claims, err := v.Verify(ctx, raw)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if claims["sub"] != "owner-1" || claims["email"] != "owner@example.com" || claims["email_verified"] != true {
			t.Errorf("claims = %v", claims)
		}
	})

	t.Run("typ variants", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
		v := newOIDCVerifier(t, iss)
		for _, typ := range []string{"JWT", "", "AUD+JWT"} {
			raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", typ, "k1", iss.claims(eddsaIssuerClientID, ""))
			if _, err := v.Verify(ctx, raw); err != nil {
				t.Errorf("typ %q rejected: %v", typ, err)
			}
		}
	})

	t.Run("key rotation selects by kid", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1", "k2")
		v := newOIDCVerifier(t, iss)
		for i, kid := range []string{"k1", "k2"} {
			raw := mintEdDSAToken(t, iss.keys[i].priv, "EdDSA", "aud+jwt", kid, iss.claims(eddsaIssuerClientID, ""))
			if _, err := v.Verify(ctx, raw); err != nil {
				t.Errorf("kid %s rejected: %v", kid, err)
			}
		}
		unknown := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k3", iss.claims(eddsaIssuerClientID, ""))
		mustRejectClean(t, v, unknown, "unknown kid")
		// A kid naming the other key's slot but signed by the wrong key.
		swapped := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k2", iss.claims(eddsaIssuerClientID, ""))
		mustRejectClean(t, v, swapped, "kid names a different key")
	})

	t.Run("initProvider accepts EdDSA", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
		st := initProvider(ctx, "mfidp", config.Provider{
			ID: "mfidp", IssuerURL: iss.URL, ClientID: eddsaIssuerClientID,
			ClientSecret: "s", ClaimStyle: "generic", Scopes: []string{"openid"},
		}, &config.Config{Auth: config.AuthConfig{OAuthRedirectBaseURL: "http://api.test"}})
		if !st.InitOK {
			t.Fatalf("init failed: %v", st.Err)
		}
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, "n"))
		if _, err := st.Verifier.Verify(ctx, raw); err != nil {
			t.Fatalf("rejected: %v", err)
		}
	})
}

// rsaIssuer is a minimal OIDC issuer publishing one RSA key.
type rsaIssuer struct {
	*httptest.Server
	kid  string
	priv *rsa.PrivateKey
}

func newRSAIssuer(t *testing.T, algs []string, kid string) *rsaIssuer {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ri := &rsaIssuer{kid: kid, priv: priv}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": ri.URL, "jwks_uri": ri.URL + "/jwks",
			"authorization_endpoint":                ri.URL + "/authorize",
			"token_endpoint":                        ri.URL + "/token",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": algs,
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "kid": kid,
			"n": base64.RawURLEncoding.EncodeToString(priv.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.E)).Bytes()),
		}}})
	})
	ri.Server = httptest.NewServer(mux)
	t.Cleanup(ri.Close)
	return ri
}

func (r *rsaIssuer) mintRS256(t *testing.T, kid string, claims map[string]any) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(claims))
	tok.Header["kid"] = kid
	raw, err := tok.SignedString(r.priv)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestVerifier_OIDC_RS256Regression(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	claimsFor := func(url string) map[string]any {
		return map[string]any{
			"iss": url, "sub": "owner-1", "aud": eddsaIssuerClientID,
			"exp": time.Now().Add(5 * time.Minute).Unix(), "iat": time.Now().Unix(),
		}
	}

	t.Run("RS256 issuer with discovery RS256", func(t *testing.T) {
		t.Parallel()
		ri := newRSAIssuer(t, []string{"RS256"}, "r1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(ri.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(ctx, ri.mintRS256(t, "r1", claimsFor(ri.URL))); err != nil {
			t.Fatalf("RS256 rejected: %v", err)
		}
	})

	t.Run("no advertised algs falls back to RS256", func(t *testing.T) {
		t.Parallel()
		ri := newRSAIssuer(t, nil, "r1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(ri.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(ctx, ri.mintRS256(t, "r1", claimsFor(ri.URL))); err != nil {
			t.Fatalf("RS256 rejected under fallback: %v", err)
		}
	})

	t.Run("dual-alg discovery accepts either; operator EdDSA excludes RS256", func(t *testing.T) {
		t.Parallel()
		// RSA key "r1" and Ed25519 key "k1" cannot share one harness JWKS, so
		// the dual case uses the RSA issuer for RS256 and the EdDSA issuer
		// for EdDSA, each advertising both algorithms.
		ri := newRSAIssuer(t, []string{"RS256", "EdDSA"}, "r1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(ri.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(ctx, ri.mintRS256(t, "r1", claimsFor(ri.URL))); err != nil {
			t.Errorf("RS256 rejected under dual discovery: %v", err)
		}
		ei := newEdDSAIssuer(t, []string{"RS256", "EdDSA"}, "k1")
		ev := newOIDCVerifier(t, ei)
		if _, err := ev.Verify(ctx, mintEdDSAToken(t, ei.keys[0].priv, "EdDSA", "JWT", "k1", ei.claims(eddsaIssuerClientID, ""))); err != nil {
			t.Errorf("EdDSA rejected under dual discovery: %v", err)
		}

		restricted, err := NewVerifierWithOptions(ctx, WithOIDC(ri.URL, eddsaIssuerClientID), WithOIDCSigningAlgs("EdDSA"))
		if err != nil {
			t.Fatal(err)
		}
		mustRejectClean(t, restricted, ri.mintRS256(t, "r1", claimsFor(ri.URL)), "RS256 under EdDSA-only operator restriction")
		er := newOIDCVerifier(t, ei, WithOIDCSigningAlgs("EdDSA"))
		if _, err := er.Verify(ctx, mintEdDSAToken(t, ei.keys[0].priv, "EdDSA", "JWT", "k1", ei.claims(eddsaIssuerClientID, ""))); err != nil {
			t.Errorf("EdDSA rejected under EdDSA-only restriction: %v", err)
		}
	})

	t.Run("RS256 token rejected when discovery is EdDSA only", func(t *testing.T) {
		t.Parallel()
		ri := newRSAIssuer(t, []string{"EdDSA"}, "r1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(ri.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		mustRejectClean(t, v, ri.mintRS256(t, "r1", claimsFor(ri.URL)), "RS256 token, discovery EdDSA")
	})
}

func TestVerifier_OIDC_KeyTypeMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("JWKS entry is RSA, token is EdDSA", func(t *testing.T) {
		t.Parallel()
		ri := newRSAIssuer(t, []string{"EdDSA", "RS256"}, "k1")
		v, err := NewVerifierWithOptions(ctx, WithOIDC(ri.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		_, priv := eddsaKey(t)
		raw := mintEdDSAToken(t, priv, "EdDSA", "aud+jwt", "k1", map[string]any{
			"iss": ri.URL, "sub": "owner-1", "aud": eddsaIssuerClientID,
			"exp": time.Now().Add(time.Hour).Unix(),
		})
		mustRejectClean(t, v, raw, "EdDSA token against RSA JWKS entry")
	})

	t.Run("JWKS entry is OKP, token is RS256", func(t *testing.T) {
		t.Parallel()
		iss := newEdDSAIssuer(t, []string{"EdDSA", "RS256"}, "k1")
		v := newOIDCVerifier(t, iss)
		rk, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatal(err)
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims(iss.claims(eddsaIssuerClientID, "")))
		tok.Header["kid"] = "k1"
		raw, err := tok.SignedString(rk)
		if err != nil {
			t.Fatal(err)
		}
		mustRejectClean(t, v, raw, "RS256 token against OKP JWKS entry")
	})
}

func TestVerifier_OIDC_AlgConfusionNegative(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("alg none, also advertised", func(t *testing.T) {
		t.Parallel()
		for _, algs := range [][]string{{"EdDSA"}, {"EdDSA", "none"}} {
			iss := newEdDSAIssuer(t, algs, "k1")
			v := newOIDCVerifier(t, iss)
			enc := base64.RawURLEncoding.EncodeToString
			hdr := enc([]byte(`{"alg":"none","typ":"JWT","kid":"k1"}`))
			cb, _ := json.Marshal(iss.claims(eddsaIssuerClientID, ""))
			mustRejectClean(t, v, hdr+"."+enc(cb)+".", "alg none, empty signature")
			mustRejectClean(t, v, mintEdDSAToken(t, iss.keys[0].priv, "none", "JWT", "k1", iss.claims(eddsaIssuerClientID, "")), "alg none with signature")
		}
	})

	t.Run("HS256 keyed by public key encodings, even when advertised", func(t *testing.T) {
		t.Parallel()
		for _, algs := range [][]string{{"EdDSA"}, {"EdDSA", "HS256"}} {
			iss := newEdDSAIssuer(t, algs, "k1")
			v := newOIDCVerifier(t, iss)
			pub := iss.keys[0].pub
			der, err := x509.MarshalPKIXPublicKey(pub)
			if err != nil {
				t.Fatal(err)
			}
			variants := map[string]string{
				"raw":        string(pub),
				"PKIX DER":   string(der),
				"PKIX PEM":   string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
				"base64":     base64.StdEncoding.EncodeToString(pub),
				"base64url":  base64.RawURLEncoding.EncodeToString(pub),
				"hex":        hex.EncodeToString(pub),
				"base64 DER": base64.StdEncoding.EncodeToString(der),
			}
			claims := jwt.MapClaims(iss.claims(eddsaIssuerClientID, ""))
			for name, secret := range variants {
				for _, kid := range []string{"", "k1"} {
					mustRejectClean(t, v, signHS256(t, secret, kid, claims), "HS256 keyed by public key ("+name+")")
				}
			}
		}
	})

	t.Run("algorithm not in discovery, both directions", func(t *testing.T) {
		t.Parallel()
		ri := newRSAIssuer(t, []string{"EdDSA"}, "k1")
		rv, err := NewVerifierWithOptions(ctx, WithOIDC(ri.URL, eddsaIssuerClientID))
		if err != nil {
			t.Fatal(err)
		}
		mustRejectClean(t, rv, ri.mintRS256(t, "k1", map[string]any{
			"iss": ri.URL, "sub": "owner-1", "aud": eddsaIssuerClientID, "exp": time.Now().Add(time.Hour).Unix(),
		}), "RS256 token, discovery EdDSA")

		rs := newEdDSAIssuer(t, []string{"RS256"}, "k1")
		sv := newOIDCVerifier(t, rs)
		mustRejectClean(t, sv, mintEdDSAToken(t, rs.keys[0].priv, "EdDSA", "JWT", "k1", rs.claims(eddsaIssuerClientID, "")), "EdDSA token, discovery RS256")
	})
}

func TestVerifier_OIDC_ClaimAndSignatureNegative(t *testing.T) {
	t.Parallel()
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	v := newOIDCVerifier(t, iss)
	priv := iss.keys[0].priv
	mint := func(c map[string]any) string {
		return mintEdDSAToken(t, priv, "EdDSA", "aud+jwt", "k1", c)
	}

	if _, err := v.Verify(context.Background(), mint(iss.claims(eddsaIssuerClientID, ""))); err != nil {
		t.Fatalf("control token rejected: %v", err)
	}

	bad := iss.claims("someone-else", "")
	mustRejectClean(t, v, mint(bad), "wrong aud")

	bad = iss.claims(eddsaIssuerClientID, "")
	bad["iss"] = "https://elsewhere.example"
	mustRejectClean(t, v, mint(bad), "wrong iss")

	bad = iss.claims(eddsaIssuerClientID, "")
	bad["exp"] = time.Now().Add(-time.Hour).Unix()
	mustRejectClean(t, v, mint(bad), "expired")

	// go-oidc enforces nbf: a not-yet-valid token is rejected.
	bad = iss.claims(eddsaIssuerClientID, "")
	bad["nbf"] = time.Now().Add(time.Hour).Unix()
	mustRejectClean(t, v, mint(bad), "not yet valid")

	_, otherPriv := eddsaKey(t)
	mustRejectClean(t, v, mintEdDSAToken(t, otherPriv, "EdDSA", "aud+jwt", "k1", iss.claims(eddsaIssuerClientID, "")), "wrong key, right kid")

	good := mint(iss.claims(eddsaIssuerClientID, ""))
	parts := strings.Split(good, ".")
	tamperedClaims := iss.claims(eddsaIssuerClientID, "")
	tamperedClaims["sub"] = "attacker"
	tb, _ := json.Marshal(tamperedClaims)
	tampered := parts[0] + "." + base64.RawURLEncoding.EncodeToString(tb) + "." + parts[2]
	mustRejectClean(t, v, tampered, "tampered payload")
}

func TestVerifier_OIDC_TypMismatchAndCarrier(t *testing.T) {
	t.Parallel()
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "k1")
	v := newOIDCVerifier(t, iss)
	for _, typ := range []string{"at+jwt", "step-up", "logout+jwt"} {
		raw := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", typ, "k1", iss.claims(eddsaIssuerClientID, ""))
		mustRejectClean(t, v, raw, "typ "+typ)
	}

	s, lv, _ := newTestKeys(t)
	for _, typ := range []string{"aud+jwt", "Aud+JWT"} {
		claims := jwt.MapClaims{
			"iss": testIssuer, "sub": uuid.NewString(), "prof": uuid.NewString(), "aud": "app-x",
			"exp": time.Now().Add(time.Minute).Unix(), "sid": uuid.NewString(), "acr": 1,
		}
		tok, err := s.signTyped(claims, typ)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lv.verifyCarrier(tok, time.Now()); err == nil {
			t.Errorf("verifyCarrier accepted typ %q", typ)
		} else {
			assertNoTokenContent(t, err, tok, "verifyCarrier typ "+typ)
		}
	}
}

func TestVerifier_LocalPathUnchangedWithOIDC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	iss := newEdDSAIssuer(t, []string{"EdDSA"}, "oidc-k")
	localPub, localPriv := eddsaKey(t)
	const localIssuer = "users-module-local"
	v, err := NewVerifierWithOptions(ctx,
		WithOIDC(iss.URL, eddsaIssuerClientID),
		WithLocalIssuer(localIssuer),
		WithEd25519PublicKeys(map[string]ed25519.PublicKey{"loc": localPub}),
	)
	if err != nil {
		t.Fatal(err)
	}

	local := mintEdDSAToken(t, localPriv, "EdDSA", "", "loc", map[string]any{
		"iss": localIssuer, "sub": "u1", "exp": time.Now().Add(time.Hour).Unix(),
	})
	if _, err := v.verifyLocal(local); err != nil {
		t.Fatalf("verifyLocal rejected a local EdDSA token: %v", err)
	}

	// An upstream-issued token (iss != localIssuer) is not accepted locally
	// and falls through to OIDC.
	upstream := mintEdDSAToken(t, iss.keys[0].priv, "EdDSA", "aud+jwt", "oidc-k", iss.claims(eddsaIssuerClientID, ""))
	if _, err := v.verifyLocal(upstream); err == nil {
		t.Error("verifyLocal accepted a token from another issuer")
	}
	if claims, err := v.Verify(ctx, upstream); err != nil || claims["iss"] != iss.URL {
		t.Errorf("Verify did not fall through to OIDC: claims=%v err=%v", claims, err)
	}

	// A token signed by the local key but claiming the upstream issuer is
	// rejected locally (issuer mismatch) and by OIDC (unknown key).
	forged := mintEdDSAToken(t, localPriv, "EdDSA", "aud+jwt", "loc", iss.claims(eddsaIssuerClientID, ""))
	mustRejectClean(t, v, forged, "local key with upstream issuer claim")
}
