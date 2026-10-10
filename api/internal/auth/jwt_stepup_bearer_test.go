package auth

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestVerifier_RejectsStepUpAsBearer(t *testing.T) {
	const secret = "s3cret"
	token, _, _, err := IssueStepUpToken([]byte(secret), 7, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, issuer := range []string{eddsaTestIssuer, "x"} {
		v, err := NewVerifierWithOptions(context.Background(), WithHS256Secret(secret), WithLocalIssuer(issuer))
		if err != nil {
			t.Fatal(err)
		}
		mustReject(t, v, token, "step-up token (no iss) issuer="+issuer)

		withIss := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"iss": issuer, "exp": time.Now().Add(time.Hour).Unix(), "sub": "u",
		})
		withIss.Header["typ"] = "step-up"
		raw, err := withIss.SignedString([]byte(secret))
		if err != nil {
			t.Fatal(err)
		}
		mustReject(t, v, raw, "step-up typ with iss claim issuer="+issuer)
	}
}

func TestNewVerifier_RefusesEmptyIssuerWithSecret(t *testing.T) {
	if _, err := NewVerifier(context.Background(), "", "", "s3cret", ""); err == nil {
		t.Fatal("NewVerifier accepted a secret with an empty local issuer")
	}
	// Normal bearer tokens still verify.
	v, err := NewVerifier(context.Background(), "", "", "s3cret", eddsaTestIssuer)
	if err != nil {
		t.Fatal(err)
	}
	exp := time.Now().Add(time.Hour)
	if _, err := v.Verify(context.Background(), signHS256(t, "s3cret", "", testClaims(exp))); err != nil {
		t.Fatalf("HS256 bearer rejected: %v", err)
	}
	pub, priv := eddsaKey(t)
	pv := verifyOnly(t, map[string]ed25519.PublicKey{"k1": pub})
	if _, err := pv.Verify(context.Background(), signEdDSA(t, priv, "k1", testClaims(exp))); err != nil {
		t.Fatalf("EdDSA bearer rejected: %v", err)
	}
}

// A verifier with an empty local issuer (unreachable via the constructors)
// must still reject step-up tokens, which carry no "iss" claim.
func TestVerifyLocal_StepUpRejectedEvenWithEmptyIssuer(t *testing.T) {
	token, _, _, err := IssueStepUpToken([]byte("s3cret"), 7, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	v := &Verifier{localSecret: []byte("s3cret")}
	mustReject(t, v, token, "step-up token vs empty-issuer verifier")
}
