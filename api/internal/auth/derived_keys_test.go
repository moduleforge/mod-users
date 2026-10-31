package auth

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"testing"

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
	d, _ := deriveKey(k1, "mod-users/v1/test-other-label")
	if bytes.Equal(a, d) {
		t.Error("labels did not separate outputs")
	}
}

// TestDeriveKeyKnownAnswer pins the OAuth state key derivation: a change
// here invalidates outstanding OAuth state and must bump the label version.
func TestDeriveKeyKnownAnswer(t *testing.T) {
	got, err := deriveKey(testSigningKey(7), LabelOAuthState)
	if err != nil {
		t.Fatal(err)
	}
	const want = "6ac4d11087d2dc2762b638e896bf43a2d1b6c56dba3dbe2e1bc635c0534630ab"
	if hex.EncodeToString(got) != want {
		t.Errorf("got %s want %s", hex.EncodeToString(got), want)
	}
}

func TestDeriveKeyDoesNotMutateSigningKey(t *testing.T) {
	k := testSigningKey(3)
	orig := append(ed25519.PrivateKey(nil), k...)
	if _, err := deriveKey(k, LabelOAuthState); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(k, orig) {
		t.Error("signing key mutated")
	}
}

func localCfg(secret string, key ed25519.PrivateKey) config.LocalAuthConfig {
	return config.LocalAuthConfig{JWTSecret: secret, JWTSigningKey: key}
}

func TestOAuthStateKeyPrecedence(t *testing.T) {
	key := testSigningKey(5)
	got, err := OAuthStateKey(localCfg("explicit-secret", key))
	if err != nil || string(got) != "explicit-secret" {
		t.Errorf("secret should win: %q, %v", got, err)
	}
	got, err = OAuthStateKey(localCfg("", key))
	if err != nil || len(got) != 32 || string(got) == "explicit-secret" {
		t.Errorf("expected derived key: len=%d err=%v", len(got), err)
	}
	got, err = OAuthStateKey(localCfg("secret-only", nil))
	if err != nil || string(got) != "secret-only" {
		t.Errorf("secret without a signing key: %q, %v", got, err)
	}
}

func TestOAuthStateKeyErrorsNeverEmptyKeyNoMaterial(t *testing.T) {
	good := testSigningKey(8)
	bad := ed25519.PrivateKey(bytes.Repeat([]byte{0xAB}, 40))
	for i, lc := range []config.LocalAuthConfig{{}, localCfg("", nil), localCfg("", bad), localCfg("", good[:10])} {
		got, err := OAuthStateKey(lc)
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
	if _, err := deriveKey(bad, LabelOAuthState); err == nil {
		t.Error("wrong-length key accepted")
	}
	if _, err := deriveKey(nil, LabelOAuthState); err == nil {
		t.Error("nil key accepted")
	}
}

func TestOAuthStateKeyBlankSecretTreatedAsUnset(t *testing.T) {
	key := testSigningKey(7)
	want, err := OAuthStateKey(localCfg("", key))
	if err != nil {
		t.Fatalf("derive: %v", err)
	}
	for _, blank := range []string{"   ", "\t\n"} {
		got, err := OAuthStateKey(localCfg(blank, key))
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("blank %q: want derived key, got %x, %v", blank, got, err)
		}
		if got, err := OAuthStateKey(localCfg(blank, nil)); err == nil || len(got) != 0 {
			t.Errorf("blank %q without a signing key: want error and no key, got %x, %v", blank, got, err)
		}
	}
	got, err := OAuthStateKey(localCfg("  padded  ", key))
	if err != nil || string(got) != "  padded  " {
		t.Errorf("non-blank secret must be untrimmed: %q, %v", got, err)
	}
}
