package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/moduleforge/mod-users/api/internal/config"
)

const eddsaTestIssuer = "users-module-local"

func eddsaKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return pub, priv
}

func testClaims(exp time.Time) jwt.MapClaims {
	return jwt.MapClaims{"iss": eddsaTestIssuer, "sub": "u1", "exp": exp.Unix()}
}

func signEdDSA(t *testing.T, priv ed25519.PrivateKey, kid string, claims jwt.MapClaims) string {
	t.Helper()
	s, err := NewEdDSASigner(priv, kid)
	if err != nil {
		t.Fatalf("NewEdDSASigner: %v", err)
	}
	raw, err := s.Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return raw
}

func signHS256(t *testing.T, secret, kid string, claims jwt.MapClaims) string {
	t.Helper()
	raw, err := NewHS256Signer(secret, kid).Sign(claims)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return raw
}

func verifyOnly(t *testing.T, keys map[string]ed25519.PublicKey) *Verifier {
	t.Helper()
	v, err := NewVerifierWithOptions(context.Background(),
		WithLocalIssuer(eddsaTestIssuer), WithEd25519PublicKeys(keys))
	if err != nil {
		t.Fatalf("NewVerifierWithOptions: %v", err)
	}
	return v
}

func mustReject(t *testing.T, v *Verifier, raw, why string) {
	t.Helper()
	if _, err := v.Verify(context.Background(), raw); err == nil {
		t.Errorf("token accepted, want rejection: %s", why)
	}
}

func TestVerifier_PublicKeyOnly(t *testing.T) {
	pub, priv := eddsaKey(t)
	exp := time.Now().Add(time.Hour)

	t.Run("accepts valid token with kid", func(t *testing.T) {
		v := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub})
		claims, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", testClaims(exp)))
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if claims["sub"] != "u1" {
			t.Errorf("sub = %v", claims["sub"])
		}
	})

	t.Run("single key without kid", func(t *testing.T) {
		v, err := NewVerifierWithOptions(context.Background(),
			WithLocalIssuer(eddsaTestIssuer), WithEd25519PublicKey(pub))
		if err != nil {
			t.Fatalf("NewVerifierWithOptions: %v", err)
		}
		if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "", testClaims(exp))); err != nil {
			t.Fatalf("Verify: %v", err)
		}
		// A token naming a kid the verifier does not know is rejected even
		// though the signature is valid under the lone key.
		mustReject(t, v, signEdDSA(t, priv, "other", testClaims(exp)), "unknown kid vs no-kid key")
	})

	t.Run("rejects unknown kid", func(t *testing.T) {
		v := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub})
		mustReject(t, v, signEdDSA(t, priv, "nope", testClaims(exp)), "unknown kid")
		mustReject(t, v, signEdDSA(t, priv, "", testClaims(exp)), "missing kid with only kid-keyed keys")
	})

	t.Run("rejects wrong key, expiry, issuer", func(t *testing.T) {
		v := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub})
		_, other := eddsaKey(t)
		mustReject(t, v, signEdDSA(t, other, "k1", testClaims(exp)), "wrong key")
		mustReject(t, v, signEdDSA(t, priv, "k1", testClaims(time.Now().Add(-time.Hour))), "expired")
		bad := testClaims(exp)
		bad["iss"] = "elsewhere"
		mustReject(t, v, signEdDSA(t, priv, "k1", bad), "issuer")
		noExp := jwt.MapClaims{"iss": eddsaTestIssuer}
		mustReject(t, v, signEdDSA(t, priv, "k1", noExp), "missing exp")
	})

	t.Run("rejects tampered payload and malformed signature", func(t *testing.T) {
		v := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub})
		raw := signEdDSA(t, priv, "k1", testClaims(exp))
		parts := strings.Split(raw, ".")
		forged := testClaims(exp)
		forged["sub"] = "admin"
		other := signEdDSA(t, priv, "k1", forged)
		mustReject(t, v, parts[0]+"."+strings.Split(other, ".")[1]+"."+parts[2], "payload swap")
		mustReject(t, v, parts[0]+"."+parts[1]+".", "empty signature")
		mustReject(t, v, parts[0]+"."+parts[1]+"."+base64.RawURLEncoding.EncodeToString([]byte("short")), "short signature")
	})
}

func TestVerifier_AlgorithmPinning(t *testing.T) {
	pub, priv := eddsaKey(t)
	exp := time.Now().Add(time.Hour)
	pubOnly := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub})

	t.Run("public-key verifier rejects HS256 including public-key-as-secret", func(t *testing.T) {
		mustReject(t, pubOnly, signHS256(t, "any-secret", "k1", testClaims(exp)), "HS256 vs EdDSA-only verifier")
		// Classic alg-confusion: HMAC the token with the public key bytes.
		confused := signHS256(t, string(pub), "k1", testClaims(exp))
		mustReject(t, pubOnly, confused, "HS256 keyed by public key")
	})

	t.Run("rejects alg none", func(t *testing.T) {
		enc := base64.RawURLEncoding.EncodeToString
		payload := enc([]byte(`{"iss":"` + eddsaTestIssuer + `","exp":` + "4102444800" + `}`))
		for _, alg := range []string{"none", "None", "NONE", "", "RS256", "eddsa", "HS512"} {
			hdr := enc([]byte(`{"alg":"` + alg + `","kid":"k1"}`))
			mustReject(t, pubOnly, hdr+"."+payload+".", "alg "+alg+" empty sig")
			mustReject(t, pubOnly, hdr+"."+payload+"."+enc(make([]byte, 64)), "alg "+alg+" zero sig")
		}
		mustReject(t, pubOnly, enc([]byte(`{"kid":"k1"}`))+"."+payload+".", "missing alg")
	})

	t.Run("secret-only verifier rejects EdDSA and none", func(t *testing.T) {
		v, err := NewVerifier(context.Background(), "", "", "s3cret", eddsaTestIssuer)
		if err != nil {
			t.Fatal(err)
		}
		mustReject(t, v, signEdDSA(t, priv, "k1", testClaims(exp)), "EdDSA vs HS256-only verifier")
		enc := base64.RawURLEncoding.EncodeToString
		mustReject(t, v, enc([]byte(`{"alg":"none"}`))+"."+enc([]byte(`{"iss":"`+eddsaTestIssuer+`","exp":4102444800}`))+".", "alg none")
	})

	t.Run("empty secret never verifies HS256", func(t *testing.T) {
		v, err := NewVerifier(context.Background(), "", "", "", eddsaTestIssuer)
		if err != nil {
			t.Fatal(err)
		}
		mustReject(t, v, signHS256(t, "", "", testClaims(exp)), "HMAC with empty key")
		if _, err := NewVerifierWithOptions(context.Background(), WithHS256Secret(""), WithLocalIssuer("x")); err == nil {
			t.Error("WithHS256Secret(\"\") accepted")
		}
	})

	t.Run("both algs configured accepts both", func(t *testing.T) {
		v, err := NewVerifierWithOptions(context.Background(),
			WithLocalIssuer(eddsaTestIssuer), WithHS256Secret("s3cret"), WithEd25519PublicKeys(map[string]ed25519.PublicKey{"k1": pub}))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(context.Background(), signHS256(t, "s3cret", "", testClaims(exp))); err != nil {
			t.Errorf("HS256: %v", err)
		}
		if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", testClaims(exp))); err != nil {
			t.Errorf("EdDSA: %v", err)
		}
	})
}

func TestVerifier_HS256PathUnchanged(t *testing.T) {
	v, err := NewVerifier(context.Background(), "", "", "local-secret", eddsaTestIssuer)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour)
	// With and without a kid header (the HS256 path ignores kid).
	for _, kid := range []string{"", "anything"} {
		if _, err := v.Verify(context.Background(), signHS256(t, "local-secret", kid, testClaims(exp))); err != nil {
			t.Errorf("kid %q: %v", kid, err)
		}
	}
	mustReject(t, v, signHS256(t, "wrong", "", testClaims(exp)), "wrong secret")
	mustReject(t, v, signHS256(t, "local-secret", "", testClaims(time.Now().Add(-time.Minute))), "expired")
}

func TestVerifier_Rotation(t *testing.T) {
	pub1, priv1 := eddsaKey(t)
	pub2, priv2 := eddsaKey(t)
	exp := time.Now().Add(time.Hour)
	t1 := signEdDSA(t, priv1, "k1", testClaims(exp))
	t2 := signEdDSA(t, priv2, "k2", testClaims(exp))

	both := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub1, "k2": pub2})
	for name, tok := range map[string]string{"k1": t1, "k2": t2} {
		if _, err := both.Verify(context.Background(), tok); err != nil {
			t.Errorf("%s with both keys: %v", name, err)
		}
	}
	// A token cannot claim another kid's key.
	mustReject(t, both, signEdDSA(t, priv1, "k2", testClaims(exp)), "k1 signature under k2 kid")

	// Retire k1: its tokens are rejected, k2 continues to work.
	only2 := verifyOnly(t, map[string]ed25519.PublicKey{"k2": pub2})
	mustReject(t, only2, t1, "retired kid")
	if _, err := only2.Verify(context.Background(), t2); err != nil {
		t.Errorf("k2 after retiring k1: %v", err)
	}
}

func TestNewVerifierWithOptions_Validation(t *testing.T) {
	pub, _ := eddsaKey(t)
	ctx := context.Background()
	cases := map[string][]VerifierOption{
		"no sources":           {WithLocalIssuer("x")},
		"no local issuer":      {WithEd25519PublicKey(pub)},
		"empty key map":        {WithLocalIssuer("x"), WithEd25519PublicKeys(nil)},
		"short key":            {WithLocalIssuer("x"), WithEd25519PublicKey(ed25519.PublicKey("short"))},
		"bad kid":              {WithLocalIssuer("x"), WithEd25519PublicKeys(map[string]ed25519.PublicKey{"a b": pub})},
		"duplicate kid across": {WithLocalIssuer("x"), WithEd25519PublicKey(pub), WithEd25519PublicKey(pub)},
	}
	for name, opts := range cases {
		if _, err := NewVerifierWithOptions(ctx, opts...); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestNewVerifierWithOptions_CopiesKeys(t *testing.T) {
	pub, priv := eddsaKey(t)
	keys := map[string]ed25519.PublicKey{"k1": append(ed25519.PublicKey(nil), pub...)}
	v := verifyOnly(t, keys)
	for i := range keys["k1"] {
		keys["k1"][i] ^= 0xff
	}
	delete(keys, "k1")
	if _, err := v.Verify(context.Background(), signEdDSA(t, priv, "k1", testClaims(time.Now().Add(time.Hour)))); err != nil {
		t.Errorf("verifier affected by caller mutation: %v", err)
	}
}

func TestNewLocalVerifier(t *testing.T) {
	ctx := context.Background()
	pub, priv := eddsaKey(t)
	pubOld, privOld := eddsaKey(t)
	exp := time.Now().Add(time.Hour)

	t.Run("EdDSA mode accepts signer output, rejects HS256", func(t *testing.T) {
		lc := config.LocalAuthConfig{
			JWTSecret: "s3cret", JWTAlg: config.JWTAlgEdDSA, JWTSigningKey: priv,
			JWTSigningKID: "cur", LocalIssuer: eddsaTestIssuer,
			JWTVerifyPublicKeys: map[string]ed25519.PublicKey{"old": pubOld},
		}
		signer, err := NewSigner(lc)
		if err != nil {
			t.Fatal(err)
		}
		v, err := NewLocalVerifier(ctx, lc)
		if err != nil {
			t.Fatalf("NewLocalVerifier: %v", err)
		}
		raw, err := signer.Sign(testClaims(exp))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(ctx, raw); err != nil {
			t.Errorf("own EdDSA token: %v", err)
		}
		if _, err := v.Verify(ctx, signEdDSA(t, privOld, "old", testClaims(exp))); err != nil {
			t.Errorf("rotation key token: %v", err)
		}
		mustReject(t, v, signHS256(t, "s3cret", "cur", testClaims(exp)), "HS256 under EdDSA mode")
		_ = pub
	})

	t.Run("HS256 mode unchanged", func(t *testing.T) {
		lc := config.LocalAuthConfig{JWTSecret: "s3cret", JWTAlg: config.JWTAlgHS256, LocalIssuer: eddsaTestIssuer}
		v, err := NewLocalVerifier(ctx, lc)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := v.Verify(ctx, signHS256(t, "s3cret", "", testClaims(exp))); err != nil {
			t.Errorf("HS256: %v", err)
		}
		mustReject(t, v, signEdDSA(t, priv, "", testClaims(exp)), "EdDSA under HS256 mode")
	})

	t.Run("conflicting kid is an error", func(t *testing.T) {
		lc := config.LocalAuthConfig{
			JWTAlg: config.JWTAlgEdDSA, JWTSigningKey: priv, JWTSigningKID: "cur",
			LocalIssuer: eddsaTestIssuer, JWTVerifyPublicKeys: map[string]ed25519.PublicKey{"cur": pubOld},
		}
		if _, err := NewLocalVerifier(ctx, lc); err == nil {
			t.Error("expected conflict error")
		}
	})
}
