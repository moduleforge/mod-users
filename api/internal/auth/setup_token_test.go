package auth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// TestGenerateSetupToken_RoundTrip asserts the core invariant: the
// plaintext is 64 hex chars (32 raw bytes × 2), the hash is the
// sha256 hex of the plaintext, and Verify accepts the pair.
func TestGenerateSetupToken_RoundTrip(t *testing.T) {
	plain, hash, err := GenerateSetupToken()
	if err != nil {
		t.Fatalf("GenerateSetupToken: %v", err)
	}
	if len(plain) != 64 {
		t.Fatalf("plaintext length: got %d, want 64", len(plain))
	}
	if _, err := hex.DecodeString(plain); err != nil {
		t.Fatalf("plaintext is not hex: %v", err)
	}
	expectedSum := sha256.Sum256([]byte(plain))
	if hash != hex.EncodeToString(expectedSum[:]) {
		t.Fatalf("hash mismatch: got %q", hash)
	}
	if !VerifySetupToken(plain, hash) {
		t.Fatalf("VerifySetupToken: token failed to roundtrip")
	}
}

// TestGenerateSetupToken_ReaderControl exercises the injectable
// reader path with a deterministic source, so the hashing wire-up is
// unit-testable without touching crypto/rand.
func TestGenerateSetupToken_ReaderControl(t *testing.T) {
	// 32 bytes of 0xAA — convenient to spot in the hex output.
	src := bytes.NewReader(bytes.Repeat([]byte{0xAA}, setupTokenBytes))
	plain, hash, err := generateSetupTokenFrom(src)
	if err != nil {
		t.Fatalf("generateSetupTokenFrom: %v", err)
	}
	wantPlain := strings.Repeat("aa", setupTokenBytes)
	if plain != wantPlain {
		t.Errorf("plain: got %q, want %q", plain, wantPlain)
	}
	if hash != HashSetupToken(plain) {
		t.Errorf("hash not sha256 of plain")
	}
}

// TestGenerateSetupToken_ShortReader surfaces the I/O error path.
func TestGenerateSetupToken_ShortReader(t *testing.T) {
	src := bytes.NewReader([]byte("too short"))
	if _, _, err := generateSetupTokenFrom(src); err == nil {
		t.Fatalf("expected error for short reader, got nil")
	}
}

// TestVerifySetupToken_Rejection pins the rejection paths:
//   - wrong plaintext rejected.
//   - empty stored hash rejected (safe default for "no token active").
//   - malformed (non-hex) plaintext rejected without panic.
func TestVerifySetupToken_Rejection(t *testing.T) {
	plain, hash, err := GenerateSetupToken()
	if err != nil {
		t.Fatalf("GenerateSetupToken: %v", err)
	}

	if VerifySetupToken("wrong-token", hash) {
		t.Errorf("wrong plaintext was accepted")
	}
	if VerifySetupToken(plain, "") {
		t.Errorf("empty stored hash was accepted")
	}
	if VerifySetupToken("not-hex-%%%", hash) {
		t.Errorf("malformed plaintext was accepted")
	}

	// Flipping a single character in the plaintext must break Verify.
	mutated := "0" + plain[1:]
	if mutated == plain {
		// Generation produced a plaintext starting with '0'; flip the
		// second char instead.
		mutated = plain[:1] + "f" + plain[2:]
	}
	if VerifySetupToken(mutated, hash) {
		t.Errorf("mutated plaintext was accepted")
	}
}

// TestHashSetupToken_Stable documents that the hash is deterministic
// across invocations (sha256, no salt) — tests downstream rely on
// that for deterministic comparisons.
func TestHashSetupToken_Stable(t *testing.T) {
	const plain = "deadbeef"
	if HashSetupToken(plain) != HashSetupToken(plain) {
		t.Errorf("HashSetupToken should be deterministic")
	}
	if HashSetupToken("a") == HashSetupToken("b") {
		t.Errorf("different inputs produced identical hashes")
	}
}
