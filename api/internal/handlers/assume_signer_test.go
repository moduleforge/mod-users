package handlers

import (
	"testing"

	"github.com/moduleforge/mod-users/api/internal/auth"
)

func TestAssumeHandler_SignerWiring(t *testing.T) {
	if _, err := NewAssumeHandlerWithSigner(nil, nil, "iss"); err == nil {
		t.Fatal("nil signer must be rejected")
	}
	h := NewAssumeHandler(nil, "", "iss")
	if _, err := h.jwtSigner(); err == nil {
		t.Fatal("no signer and empty secret must fail")
	}
	s, err := auth.NewHS256Signer("secret", "")
	if err != nil {
		t.Fatal(err)
	}
	h2, err := NewAssumeHandlerWithSigner(nil, s, "iss")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := h2.jwtSigner(); err != nil || got != s {
		t.Fatalf("want injected signer, got %v, %v", got, err)
	}
}
