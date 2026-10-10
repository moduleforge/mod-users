package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
	db "github.com/moduleforge/mod-users/model/db"
)

func TestHandler_NoSignerEmptySecretFails(t *testing.T) {
	h := New(nil, nil, nil, "", "iss", nil, "")
	if _, err := h.issueLocalJWT(db.UserAccount{}); err == nil {
		t.Fatal("handler with no signer and no secret must not mint a token")
	}
	if _, err := h.issueAnonymousJWT(db.UserAccount{}); err == nil {
		t.Fatal("anonymous: handler with no signer and no secret must not mint a token")
	}
}

func TestNewWithSigner_RejectsNilAndUsesInjected(t *testing.T) {
	if _, err := NewWithSigner(nil, nil, nil, nil, "iss", nil, ""); err == nil {
		t.Fatal("nil signer must be rejected")
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := localauth.NewEdDSASigner(priv, "k1")
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewWithSigner(nil, nil, nil, s, "iss", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.issueLocalJWT(db.UserAccount{}); err != nil {
		t.Fatalf("issue: %v", err)
	}
	got, err := h.jwtSigner()
	if err != nil || got.Alg() != "EdDSA" {
		t.Fatalf("want injected EdDSA signer, got %v, %v", got, err)
	}
}

func TestOIDCHandler_SignerBuiltOnceAtConstruction(t *testing.T) {
	good := &config.Config{LocalAuth: config.LocalAuthConfig{JWTSecret: "s", LocalIssuer: "i"}}
	h := NewOIDCHandler(nil, nil, nil, noopLoginRecorder{}, good)
	if h.SignerErr() != nil || h.signer == nil {
		t.Fatalf("expected signer built at construction, err=%v", h.SignerErr())
	}
	first := h.signer
	if h.signer != first {
		t.Fatal("signer must be stable across uses")
	}

	bad := &config.Config{LocalAuth: config.LocalAuthConfig{JWTAlg: config.JWTAlgEdDSA}}
	if NewOIDCHandler(nil, nil, nil, noopLoginRecorder{}, bad).SignerErr() == nil {
		t.Fatal("bad EdDSA config must surface a signer error at construction")
	}
}
