package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
)

// The OIDC handoff code is bound to the browser that started the sign-in by a
// PKCE-style verifier (RFC 7636, method S256). The browser generates a random
// verifier, sends only its challenge (the base64url SHA-256 of the verifier) to
// GET /v1/auth/oidc/{provider}/start, and presents the verifier itself when it
// redeems the code at POST /v1/auth/handoff. A party that intercepts only the
// redirect fragment holds the code but not the verifier, so it cannot redeem it.

// ErrInvalidHandoffChallenge is a handoff challenge that is not the unpadded
// base64url encoding of a 32-byte SHA-256 digest.
var ErrInvalidHandoffChallenge = errors.New("oauth: invalid handoff challenge")

const (
	// handoffChallengeLen is the length of a base64url (unpadded) SHA-256 digest.
	handoffChallengeLen = 43
	// Verifier length bounds, per RFC 7636 section 4.1.
	minHandoffVerifierLen = 43
	maxHandoffVerifierLen = 128
)

// HandoffChallenge returns the challenge of verifier: the unpadded base64url
// SHA-256 digest of its bytes.
func HandoffChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ValidHandoffChallenge reports whether c is the unpadded base64url encoding of
// exactly 32 bytes, the shape of a SHA-256 digest.
func ValidHandoffChallenge(c string) bool {
	if len(c) != handoffChallengeLen {
		return false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(c)
	return err == nil && len(raw) == sha256.Size
}

// validHandoffVerifier reports whether v has the RFC 7636 verifier shape: 43 to
// 128 characters of the unreserved set [A-Za-z0-9-._~].
func validHandoffVerifier(v string) bool {
	if len(v) < minHandoffVerifierLen || len(v) > maxHandoffVerifierLen {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-', c == '.', c == '_', c == '~':
		default:
			return false
		}
	}
	return true
}

// VerifyHandoff reports whether verifier answers challenge. It is false for a
// malformed verifier or an empty challenge; otherwise the comparison of the
// verifier's challenge with the stored one is constant time.
func VerifyHandoff(verifier, challenge string) bool {
	if challenge == "" || !validHandoffVerifier(verifier) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HandoffChallenge(verifier)), []byte(challenge)) == 1
}
