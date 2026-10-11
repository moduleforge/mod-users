package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"testing"
	"time"
)

// TestVerifier_PublicKeyAsHMACSecretVariants extends the alg-confusion
// coverage with every plausible encoding of the public key an attacker could
// use as an HMAC secret against a public-key-only verifier.
func TestVerifier_PublicKeyAsHMACSecretVariants(t *testing.T) {
	pub, _ := eddsaKey(t)
	exp := time.Now().Add(time.Hour)
	pubOnly := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub})

	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})

	variants := map[string]string{
		"raw bytes":   string(pub),
		"PKIX DER":    string(der),
		"PKIX PEM":    string(pemBytes),
		"base64 raw":  base64.StdEncoding.EncodeToString(pub),
		"base64url":   base64.RawURLEncoding.EncodeToString(pub),
		"hex":         hex.EncodeToString(pub),
		"base64 PKIX": base64.StdEncoding.EncodeToString(der),
	}
	for name, secret := range variants {
		for _, kid := range []string{"", "k1"} {
			mustReject(t, pubOnly, signHS256(t, secret, kid, testClaims(exp)),
				"HS256 keyed by public key ("+name+", kid="+kid+")")
		}
	}
}

// TestVerifier_MixedModeWrongKeyAndClaims confirms that when both algorithms
// are enabled, each path still enforces its own key plus issuer and expiry.
func TestVerifier_MixedModeWrongKeyAndClaims(t *testing.T) {
	pub, priv := eddsaKey(t)
	_, otherPriv := eddsaKey(t)
	exp := time.Now().Add(time.Hour)
	v, err := NewVerifierWithOptions(context.Background(),
		WithLocalIssuer(eddsaTestIssuer), WithHS256Secret("s3cret"),
		WithEd25519PublicKeys(map[string]ed25519.PublicKey{"k1": pub}))
	if err != nil {
		t.Fatal(err)
	}
	mustReject(t, v, signEdDSA(t, otherPriv, "k1", testClaims(exp)), "EdDSA wrong key")
	mustReject(t, v, signHS256(t, "wrong", "", testClaims(exp)), "HS256 wrong secret")
	// Public key as HMAC secret must not work even though HS256 is enabled.
	mustReject(t, v, signHS256(t, string(pub), "k1", testClaims(exp)), "HS256 keyed by public key")

	past := testClaims(time.Now().Add(-time.Hour))
	mustReject(t, v, signEdDSA(t, priv, "k1", past), "EdDSA expired")
	mustReject(t, v, signHS256(t, "s3cret", "", past), "HS256 expired")
	bad := testClaims(exp)
	bad["iss"] = "elsewhere"
	mustReject(t, v, signEdDSA(t, priv, "k1", bad), "EdDSA wrong issuer")
	mustReject(t, v, signHS256(t, "s3cret", "", bad), "HS256 wrong issuer")
}

// TestVerifier_MalformedKidHeader ensures a non-string kid cannot select a
// key or panic the verifier.
func TestVerifier_MalformedKidHeader(t *testing.T) {
	pub, priv := eddsaKey(t)
	v := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub})
	enc := base64.RawURLEncoding.EncodeToString
	payload := enc([]byte(`{"iss":"` + eddsaTestIssuer + `","exp":4102444800}`))
	for _, kid := range []string{`1`, `["k1"]`, `{"k":"k1"}`, `true`} {
		hdr := enc([]byte(`{"alg":"EdDSA","kid":` + kid + `}`))
		sig := ed25519.Sign(priv, []byte(hdr+"."+payload))
		mustReject(t, v, hdr+"."+payload+"."+enc(sig), "non-string kid "+kid)
	}
}
