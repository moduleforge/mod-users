package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
)

// setupTokenBytes is the length in bytes of the raw random material behind a
// setup token. 32 bytes (256 bits) is overkill for an anti-CSRF / one-shot
// admin-bootstrap nonce and cheap to emit; matches the plan's spec.
const setupTokenBytes = 32

// GenerateSetupToken returns a fresh setup token as a pair of strings:
// the plaintext hex-encoded value (64 hex chars — this is what the operator
// pastes into the GUI) and the sha256 hex of the plaintext (what the DB
// stores). The plaintext must never be logged or persisted in plaintext.
func GenerateSetupToken() (plain, hash string, err error) {
	return generateSetupTokenFrom(rand.Reader)
}

// generateSetupTokenFrom is the io.Reader-injectable core of GenerateSetupToken;
// tests use it with a deterministic reader to verify the hash wiring without
// having to also read crypto/rand. Production always calls GenerateSetupToken.
func generateSetupTokenFrom(src io.Reader) (plain, hash string, err error) {
	raw := make([]byte, setupTokenBytes)
	if _, err := io.ReadFull(src, raw); err != nil {
		return "", "", fmt.Errorf("setup token: read random: %w", err)
	}
	plain = hex.EncodeToString(raw)
	hash = HashSetupToken(plain)
	return plain, hash, nil
}

// HashSetupToken returns the sha256 hex digest of a setup-token plaintext.
// Separate from GenerateSetupToken so callers can hash an inbound token to
// compare with the DB-stored hash without first generating a new one.
func HashSetupToken(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// VerifySetupToken reports whether plain hashes to storedHash using a
// constant-time comparison. Both inputs are treated as opaque strings; a
// malformed (non-hex or wrong length) plain still runs the full compare
// against the stored hash so callers cannot time the rejection path.
func VerifySetupToken(plain, storedHash string) bool {
	if storedHash == "" {
		return false
	}
	computed := HashSetupToken(plain)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}

// PrintSetupTokenBanner writes a human-scannable ASCII banner to stderr so
// the operator can copy the token even when JSON logs dominate the stream.
// A matching structured slog.Error is emitted so log aggregators can still
// alert on it. Returns nothing — banner emission is best-effort; a failed
// stderr write falls through to the structured log.
func PrintSetupTokenBanner(token, guiConfigURL string) {
	// Boxed banner for humans. Width is fixed so the output stays aligned
	// regardless of token length (64 hex chars fits comfortably inside 66).
	const rule = "=================================================================="
	const thin = "------------------------------------------------------------------"
	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, rule)
	fmt.Fprintln(os.Stderr, "                 OIDC SETUP TOKEN (one-time use)")
	fmt.Fprintln(os.Stderr, thin)
	fmt.Fprintln(os.Stderr, "  "+token)
	fmt.Fprintln(os.Stderr, thin)
	fmt.Fprintln(os.Stderr, "  Paste into "+guiConfigURL+" to configure OIDC.")
	fmt.Fprintln(os.Stderr, "  This banner repeats only on first boot in this state.")
	fmt.Fprintln(os.Stderr, rule)
	fmt.Fprintln(os.Stderr)

	// Structured counterpart for log aggregators / automation. The token is
	// deliberately NOT in the structured log — ops should find it in the
	// banner above; the structured event just flags that a token exists.
	slog.Error("oidc onboarding required: setup token generated",
		"setup_token_required", true,
		"config_url", guiConfigURL,
	)
}
