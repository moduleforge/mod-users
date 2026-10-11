package handlers

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
)

// A derived step-up key (EdDSA, no JWT_SECRET) flows through the same
// string-typed handler wiring as an explicit secret.
func TestCheckStepUp_DerivedKey(t *testing.T) {
	lc := config.LocalAuthConfig{
		JWTAlg:        config.JWTAlgEdDSA,
		JWTSigningKey: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{9}, ed25519.SeedSize)),
	}
	key, err := localauth.StepUpKey(lc)
	if err != nil {
		t.Fatal(err)
	}
	secret := string(key)
	token, _, _, err := localauth.IssueStepUpToken([]byte(secret), 42, localauth.StepUpTTL)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	consumed := &sync.Map{}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Step-Up-Token", token)
	if err := checkStepUp(req, 42, true, secret, consumed); err != nil {
		t.Fatalf("derived key: %v", err)
	}
	// Single use.
	if err := checkStepUp(req, 42, true, secret, consumed); err == nil {
		t.Error("replay accepted")
	}
	// Missing header and empty secret still fail closed.
	if err := checkStepUp(httptest.NewRequest(http.MethodPost, "/", nil), 42, true, secret, consumed); !errors.Is(err, localauth.ErrStepUpRequired) {
		t.Errorf("missing header: got %v", err)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/", nil)
	req2.Header.Set("X-Step-Up-Token", token)
	if err := checkStepUp(req2, 42, true, "", &sync.Map{}); err == nil {
		t.Error("empty secret accepted a token")
	}
}
