package auth

import (
	"strings"
	"testing"
	"time"
)

func TestStateSigner_RoundTrip(t *testing.T) {
	signer, err := NewStateSigner([]byte("test-secret-key-32-bytes-minimum!"))
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}

	original := StatePayload{
		Provider:   "google",
		ReturnPath: "/profile",
		Nonce:      "abc123",
		Expires:    time.Now().Add(5 * time.Minute).Unix(),
	}

	token, err := signer.Sign(original)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	got, err := signer.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if got.Provider != original.Provider {
		t.Errorf("Provider round-trip: got %q, want %q", got.Provider, original.Provider)
	}
	if got.ReturnPath != original.ReturnPath {
		t.Errorf("ReturnPath round-trip: got %q, want %q", got.ReturnPath, original.ReturnPath)
	}
	if got.Nonce != original.Nonce {
		t.Errorf("Nonce round-trip: got %q, want %q", got.Nonce, original.Nonce)
	}
	if got.Expires != original.Expires {
		t.Errorf("Expires round-trip: got %d, want %d", got.Expires, original.Expires)
	}
}

func TestStateSigner_TamperedMACRejected(t *testing.T) {
	signer, err := NewStateSigner([]byte("test-secret"))
	if err != nil {
		t.Fatalf("NewStateSigner: %v", err)
	}

	token, err := signer.Sign(StatePayload{
		Provider: "google",
		Expires:  time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	// Flip one byte in the MAC and expect verification to fail.
	dotIdx := strings.LastIndex(token, ".")
	if dotIdx < 0 {
		t.Fatalf("token has no separator: %q", token)
	}
	tampered := token[:dotIdx+1] + flipFirstChar(token[dotIdx+1:])

	if _, err := signer.Verify(tampered); err == nil {
		t.Fatal("expected Verify to reject tampered MAC, got nil error")
	}
}

func TestStateSigner_DifferentKeyRejected(t *testing.T) {
	s1, _ := NewStateSigner([]byte("key-one"))
	s2, _ := NewStateSigner([]byte("key-two"))

	token, err := s1.Sign(StatePayload{
		Provider: "google",
		Expires:  time.Now().Add(time.Minute).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	if _, err := s2.Verify(token); err == nil {
		t.Fatal("expected Verify with different key to fail")
	}
}

func TestStateSigner_ExpiredRejected(t *testing.T) {
	signer, _ := NewStateSigner([]byte("test-secret"))

	token, err := signer.Sign(StatePayload{
		Provider: "google",
		Expires:  time.Now().Add(-1 * time.Second).Unix(),
	})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	_, err = signer.Verify(token)
	if err == nil {
		t.Fatal("expected Verify to reject expired token")
	}
	if !strings.Contains(err.Error(), "expired") {
		t.Errorf("error should mention expiration, got %v", err)
	}
}

func TestStateSigner_MalformedRejected(t *testing.T) {
	signer, _ := NewStateSigner([]byte("test-secret"))

	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no-dot", "onlyonepart"},
		{"too-many-dots", "a.b.c"},
		{"bad-base64", "!!!.!!!"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := signer.Verify(tc.token); err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestNewStateSigner_EmptyKey(t *testing.T) {
	if _, err := NewStateSigner(nil); err == nil {
		t.Error("expected error for nil key")
	}
	if _, err := NewStateSigner([]byte{}); err == nil {
		t.Error("expected error for empty key")
	}
}

// flipFirstChar returns s with its first character replaced by a different
// one, to simulate a single-bit tamper of the MAC.
func flipFirstChar(s string) string {
	if s == "" {
		return "X"
	}
	flipped := byte('A')
	if s[0] == 'A' {
		flipped = 'B'
	}
	return string(flipped) + s[1:]
}
