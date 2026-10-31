package auth

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/moduleforge/mod-users/api/internal/config"
)

func edDSAConfig(secret string) *config.Config {
	return &config.Config{LocalAuth: config.LocalAuthConfig{
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
	// The signer is keyed by the derived state key, not the raw seed.
	key, _ := OAuthStateKey(edDSAConfig("").LocalAuth)
	ref, _ := NewStateSigner(key)
	if _, err := ref.Verify(tok); err != nil {
		t.Errorf("token not verifiable under the derived state key: %v", err)
	}
	seed, _ := NewStateSigner(testSigningKey(7).Seed())
	if _, err := seed.Verify(tok); err == nil {
		t.Error("state token verified under the raw seed")
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
	// No secret and no key, and a config with an unusable key.
	badKey := ed25519.PrivateKey([]byte("SIGNINGKEYMARKER"))
	cfgs := []*config.Config{
		{LocalAuth: config.LocalAuthConfig{}},
		{LocalAuth: config.LocalAuthConfig{JWTSigningKey: badKey}},
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

// No key bytes may appear in resolver errors.
func TestDerivedKeys_ErrorsDoNotLeakKey(t *testing.T) {
	seed := hex.EncodeToString([]byte("0123456789abcdef"))
	b64 := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	for _, lc := range []config.LocalAuthConfig{
		{},
		{JWTSigningKey: ed25519.PrivateKey([]byte("0123456789abcdef"))},
	} {
		_, err := OAuthStateKey(lc)
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
