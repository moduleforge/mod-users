package auth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/moduleforge/mod-users/api/internal/config"
)

func testSigningKey(b byte) ed25519.PrivateKey {
	return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{b}, ed25519.SeedSize))
}

func TestDeriveKeyDeterminismAndSeparation(t *testing.T) {
	k1 := testSigningKey(1)
	a, err := deriveKey(k1, LabelOAuthState)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 32 {
		t.Fatalf("len = %d", len(a))
	}
	b, _ := deriveKey(k1, LabelOAuthState)
	if !bytes.Equal(a, b) {
		t.Error("same key+label not deterministic")
	}
	c, _ := deriveKey(testSigningKey(2), LabelOAuthState)
	if bytes.Equal(a, c) {
		t.Error("different signing keys gave same output")
	}
	d, _ := deriveKey(k1, LabelStepUp)
	if bytes.Equal(a, d) {
		t.Error("labels did not separate outputs")
	}
}

func TestDeriveKeyKnownAnswer(t *testing.T) {
	k := testSigningKey(7)
	cases := map[string]string{
		LabelOAuthState: "6ac4d11087d2dc2762b638e896bf43a2d1b6c56dba3dbe2e1bc635c0534630ab",
		LabelStepUp:     "de03306059ea06852e82de4e600d23e2987152aa641ed461a19195e76ecfb90f",
	}
	for label, want := range cases {
		got, err := deriveKey(k, label)
		if err != nil {
			t.Fatal(err)
		}
		if hex.EncodeToString(got) != want {
			t.Errorf("label %q: got %s want %s", label, hex.EncodeToString(got), want)
		}
	}
}

func TestDeriveKeyDoesNotMutateSigningKey(t *testing.T) {
	k := testSigningKey(3)
	orig := append(ed25519.PrivateKey(nil), k...)
	if _, err := deriveKey(k, LabelStepUp); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k, orig) {
		t.Error("signing key mutated")
	}
}

func eddsaCfg(secret string, key ed25519.PrivateKey) config.LocalAuthConfig {
	return config.LocalAuthConfig{JWTSecret: secret, JWTAlg: config.JWTAlgEdDSA, JWTSigningKey: key}
}

func TestCrossRejection(t *testing.T) {
	lc := eddsaCfg("", testSigningKey(4))
	stateKey, err := OAuthStateKey(lc)
	if err != nil {
		t.Fatal(err)
	}
	stepKey, err := StepUpKey(lc)
	if err != nil {
		t.Fatal(err)
	}

	stateSigner, _ := NewStateSigner(stateKey)
	stepSigner, _ := NewStateSigner(stepKey)
	tok, err := stateSigner.Sign(StatePayload{Provider: "p", Expires: time.Now().Add(time.Minute).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stateSigner.Verify(tok); err != nil {
		t.Fatalf("own key should verify: %v", err)
	}
	if _, err := stepSigner.Verify(tok); err == nil {
		t.Error("state token verified under step-up key")
	}

	st, _, _, err := IssueStepUpToken(stepKey, 42, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyStepUpToken(stateKey, st, 42, &sync.Map{}); !errors.Is(err, ErrStepUpRequired) {
		t.Errorf("step-up token under state key: got %v", err)
	}
	if err := VerifyStepUpToken(stepKey, st, 42, &sync.Map{}); err != nil {
		t.Errorf("step-up token under own key: %v", err)
	}
}

func TestResolversPrecedence(t *testing.T) {
	key := testSigningKey(5)
	for name, fn := range map[string]func(config.LocalAuthConfig) ([]byte, error){
		"state": OAuthStateKey, "stepup": StepUpKey,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := fn(eddsaCfg("explicit-secret", key))
			if err != nil || string(got) != "explicit-secret" {
				t.Errorf("secret should win: %q, %v", got, err)
			}
			got, err = fn(eddsaCfg("", key))
			if err != nil || len(got) != 32 || string(got) == "explicit-secret" {
				t.Errorf("expected derived key: len=%d err=%v", len(got), err)
			}
		})
	}
	s, _ := OAuthStateKey(eddsaCfg("", key))
	u, _ := StepUpKey(eddsaCfg("", key))
	if bytes.Equal(s, u) {
		t.Error("resolvers returned identical derived keys")
	}
}

func TestResolversHS256(t *testing.T) {
	key := testSigningKey(6)
	for _, alg := range []string{"", config.JWTAlgHS256} {
		lc := config.LocalAuthConfig{JWTSecret: "hs-secret", JWTAlg: alg}
		for _, fn := range []func(config.LocalAuthConfig) ([]byte, error){OAuthStateKey, StepUpKey} {
			got, err := fn(lc)
			if err != nil || string(got) != "hs-secret" {
				t.Errorf("alg %q: %q, %v", alg, got, err)
			}
			// No derivation under HS256, even with a signing key present.
			lc2 := config.LocalAuthConfig{JWTAlg: alg, JWTSigningKey: key}
			got, err = fn(lc2)
			if err == nil || len(got) != 0 {
				t.Errorf("alg %q empty secret: want error, got %x, %v", alg, got, err)
			}
		}
	}
}

func TestResolverErrorsNeverEmptyKeyNoMaterial(t *testing.T) {
	good := testSigningKey(8)
	bad := ed25519.PrivateKey(bytes.Repeat([]byte{0xAB}, 40))
	cases := []config.LocalAuthConfig{
		{},
		eddsaCfg("", nil),
		eddsaCfg("", bad),
		eddsaCfg("", good[:10]),
	}
	for i, lc := range cases {
		for _, fn := range []func(config.LocalAuthConfig) ([]byte, error){OAuthStateKey, StepUpKey} {
			got, err := fn(lc)
			if err == nil {
				t.Errorf("case %d: expected error", i)
				continue
			}
			if len(got) != 0 {
				t.Errorf("case %d: key returned with error", i)
			}
			msg := err.Error()
			if bytes.Contains([]byte(msg), []byte{0xAB}) || bytes.Contains([]byte(msg), good) ||
				bytes.Contains([]byte(msg), []byte(hex.EncodeToString(good.Seed()))) {
				t.Errorf("case %d: error contains key material", i)
			}
		}
	}
	if _, err := deriveKey(bad, LabelStepUp); err == nil {
		t.Error("wrong-length key accepted")
	}
	if _, err := deriveKey(nil, LabelStepUp); err == nil {
		t.Error("nil key accepted")
	}
}

func TestIssueStepUpTokenEmptySecretGuard(t *testing.T) {
	if _, _, _, err := IssueStepUpToken(nil, 1, time.Minute); !errors.Is(err, ErrStepUpEmptySecret) {
		t.Errorf("got %v", err)
	}
	if err := VerifyStepUpToken(nil, "a.b.c", 1, &sync.Map{}); !errors.Is(err, ErrStepUpRequired) {
		t.Errorf("got %v", err)
	}
}
