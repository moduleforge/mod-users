package auth

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"strings"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// Derivation label for the HKDF-SHA256 key derived from the Ed25519 signing
// key. The only derived key is the OAuth state HMAC key.
//
// The label is a non-secret domain-separation context (the HKDF "info"
// input): a key derived from the same signing key under a different label is
// computationally independent. The "v1" segment is a version: changing the
// derivation scheme in any way that changes output must bump it (and a
// known-answer test pins the current value), so a silent label change cannot
// invalidate or cross-wire outstanding OAuth state.
//
// The JWT "kid" is deliberately NOT part of the derivation. kid names which
// key signs a JWT and is rotated together with the signing key; mixing it in
// would make the derived key change whenever an operator relabels a key
// without rotating it, and would tie HMAC key lifetime to a header value that
// is non-secret and attacker-visible. The derived key follows the signing key
// itself: rotating the signing key rotates it, which only invalidates
// short-lived OAuth state.
const LabelOAuthState = "mod-users/v1/oauth-state"

// derivedKeyLen is the length in bytes of every derived HMAC key.
const derivedKeyLen = 32

// Fixed error messages; none ever carries key material.
var (
	errDerivedKeyInvalidSigningKey = errors.New("derived key: invalid signing key")
	errDerivedKeyDerive            = errors.New("derived key: derivation failed")
	errDerivedKeyUnavailable       = errors.New("derived key: neither JWT_SECRET nor a signing key is configured")
)

// deriveKey derives a 32-byte HMAC key from the Ed25519 private key with
// HKDF-SHA256 (RFC 5869): IKM is the key's 32-byte seed, salt is nil, and info
// is label. The seed copy is zeroed before returning. Zeroing is best effort:
// Go gives no guarantee against the runtime or compiler having made other
// copies (stack growth, register spills), and the caller's key and the
// returned key remain live in memory for their own lifetimes.
func deriveKey(key ed25519.PrivateKey, label string) ([]byte, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errDerivedKeyInvalidSigningKey
	}
	seed := key.Seed() // returns a fresh copy
	defer clear(seed)
	out, err := hkdf.Key(sha256.New, seed, nil, label, derivedKeyLen)
	if err != nil {
		return nil, errDerivedKeyDerive
	}
	return out, nil
}

// resolveKey applies the precedence rule: an explicit, non-blank JWTSecret
// wins byte-for-byte and untrimmed; a blank or whitespace-only JWTSecret is
// treated as unset and is never returned as key material; otherwise the key
// is derived from the Ed25519 signing key under label; otherwise an error is
// returned. It never returns an empty key with a nil error.
func resolveKey(lc config.LocalAuthConfig, label string) ([]byte, error) {
	if strings.TrimSpace(lc.JWTSecret) != "" {
		return []byte(lc.JWTSecret), nil
	}
	if len(lc.JWTSigningKey) > 0 {
		k, err := deriveKey(lc.JWTSigningKey, label)
		if err != nil {
			return nil, err
		}
		if len(k) == 0 {
			return nil, errDerivedKeyDerive
		}
		return k, nil
	}
	return nil, errDerivedKeyUnavailable
}

// OAuthStateKey returns the HMAC key for the OAuth state signer: the explicit
// JWT_SECRET when set, otherwise a key derived from the Ed25519 signing key.
func OAuthStateKey(lc config.LocalAuthConfig) ([]byte, error) {
	return resolveKey(lc, LabelOAuthState)
}
