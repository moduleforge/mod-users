package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moduleforge/mod-users/api/internal/config"
)

func edDSAConfig(secret string) *config.Config {
	return &config.Config{LocalAuth: config.LocalAuthConfig{
		JWTAlg:        config.JWTAlgEdDSA,
		JWTSigningKey: testSigningKey(7),
		JWTSecret:     secret,
	}}
}

func sampleState() StatePayload {
	return StatePayload{Provider: "google", ReturnPath: "/", Nonce: "n", Expires: time.Now().Add(time.Minute).Unix()}
}

func TestNewOAuth_EdDSANoSecretDerivesStateKey(t *testing.T) {
	o, err := NewOAuth(context.Background(), edDSAConfig(""))
	if err != nil {
		t.Fatalf("NewOAuth: %v", err)
	}
	tok, err := o.StateSigner.Sign(sampleState())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.StateSigner.Verify(tok); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	// The signer is keyed by the derived state key, not the raw seed or a
	// step-up key.
	key, _ := OAuthStateKey(edDSAConfig("").LocalAuth)
	ref, _ := NewStateSigner(key)
	if _, err := ref.Verify(tok); err != nil {
		t.Errorf("token not verifiable under the derived state key: %v", err)
	}
	stepKey, _ := StepUpKey(edDSAConfig("").LocalAuth)
	other, _ := NewStateSigner(stepKey)
	if _, err := other.Verify(tok); err == nil {
		t.Error("state token verified under the step-up key")
	}
}

func TestNewOAuth_ExplicitSecretWins(t *testing.T) {
	const secret = "an-explicit-secret-value"
	for _, cfg := range []*config.Config{edDSAConfig(secret), {LocalAuth: config.LocalAuthConfig{JWTSecret: secret}}} {
		o, err := NewOAuth(context.Background(), cfg)
		if err != nil {
			t.Fatalf("NewOAuth: %v", err)
		}
		ext, _ := NewStateSigner([]byte(secret))
		tok, err := ext.Sign(sampleState())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := o.StateSigner.Verify(tok); err != nil {
			t.Errorf("token signed with the explicit secret rejected: %v", err)
		}
	}
}

func TestNewOAuth_NoKeyFailsWithoutKeyMaterial(t *testing.T) {
	// HS256, no secret, and an EdDSA config with an unusable key.
	badKey := ed25519.PrivateKey([]byte("SIGNINGKEYMARKER"))
	cfgs := []*config.Config{
		{LocalAuth: config.LocalAuthConfig{}},
		{LocalAuth: config.LocalAuthConfig{JWTAlg: config.JWTAlgEdDSA, JWTSigningKey: badKey}},
	}
	for _, cfg := range cfgs {
		_, err := NewOAuth(context.Background(), cfg)
		if err == nil {
			t.Fatal("expected error")
		}
		if strings.Contains(err.Error(), "SIGNINGKEYMARKER") {
			t.Errorf("error leaks key bytes: %v", err)
		}
	}
}

// State tokens and step-up tokens must not be interchangeable even though both
// are keyed from the same signing key.
func TestDerivedKeys_CrossRejectionAtWiring(t *testing.T) {
	lc := edDSAConfig("").LocalAuth
	stateKey, err := OAuthStateKey(lc)
	if err != nil {
		t.Fatal(err)
	}
	stepKey, err := StepUpKey(lc)
	if err != nil {
		t.Fatal(err)
	}

	// Step-up token under the derived key issues and verifies.
	token, _, _, err := IssueStepUpToken(stepKey, 42, StepUpTTL)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if err := VerifyStepUpToken(stepKey, token, 42, &sync.Map{}); err != nil {
		t.Fatalf("verify under step-up key: %v", err)
	}
	// ... but not under the state key.
	if err := VerifyStepUpToken(stateKey, token, 42, &sync.Map{}); err == nil {
		t.Error("step-up token verified under the state key")
	}

	// A state token is rejected by the step-up verifier.
	ss, _ := NewStateSigner(stateKey)
	st, _ := ss.Sign(sampleState())
	if err := VerifyStepUpToken(stepKey, st, 42, &sync.Map{}); err == nil {
		t.Error("state token accepted by step-up verifier")
	}
	// And a step-up token is rejected by the state signer.
	if _, err := ss.Verify(token); err == nil {
		t.Error("step-up token accepted by state signer")
	}
}

func TestDerivedStepUp_EmptySecretGuardsStillFire(t *testing.T) {
	if _, _, _, err := IssueStepUpToken(nil, 1, StepUpTTL); err != ErrStepUpEmptySecret {
		t.Errorf("issue with empty secret: got %v, want ErrStepUpEmptySecret", err)
	}
}

// No key bytes may appear in resolver errors.
func TestDerivedKeys_ErrorsDoNotLeakKey(t *testing.T) {
	seed := hex.EncodeToString([]byte("0123456789abcdef"))
	b64 := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	for _, lc := range []config.LocalAuthConfig{
		{},
		{JWTAlg: config.JWTAlgEdDSA, JWTSigningKey: ed25519.PrivateKey([]byte("0123456789abcdef"))},
	} {
		_, err := StepUpKey(lc)
		if err == nil {
			t.Fatal("expected error")
		}
		for _, bad := range []string{"0123456789abcdef", seed, b64} {
			if strings.Contains(err.Error(), bad) {
				t.Errorf("error leaks key material: %v", err)
			}
		}
	}
}
