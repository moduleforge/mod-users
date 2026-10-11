package auth

import (
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// Derivation labels for HKDF-SHA256 keys derived from the Ed25519 signing key.
//
// The labels are non-secret domain-separation contexts (the HKDF "info"
// input): two keys derived from the same signing key under different labels
// are computationally independent, so a token MAC'd under one purpose cannot
// verify under another. The "v1" segment is a version: changing the derivation
// scheme in any way that changes output must bump it (and a known-answer test
// pins the current values), so a silent label change cannot invalidate or
// cross-wire outstanding state and step-up tokens.
//
// The JWT "kid" is deliberately NOT part of the derivation. kid names which
// key signs a JWT and is rotated together with the signing key; mixing it in
// would make derived keys change whenever an operator relabels a key without
// rotating it, and would tie HMAC key lifetime to a header value that is
// non-secret and attacker-visible. The derived keys follow the signing key
// itself: rotating the signing key rotates the derived keys, which only
// invalidates short-lived state and step-up tokens.
const (
	LabelOAuthState = "mod-users/v1/oauth-state"
	LabelStepUp     = "mod-users/v1/step-up"
)

// derivedKeyLen is the length in bytes of every derived HMAC key.
const derivedKeyLen = 32

// Fixed error messages; none ever carries key material.
var (
	errDerivedKeyInvalidSigningKey = errors.New("derived key: invalid signing key")
	errDerivedKeyDerive            = errors.New("derived key: derivation failed")
	errDerivedKeyUnavailable       = errors.New("derived key: JWT_SECRET is required unless JWT_ALG=EdDSA with a signing key")
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

// resolveKey applies the precedence rule: an explicit JWTSecret wins
// (including the whole HS256 path); otherwise, under EdDSA, the key is
// derived from the signing key under label; otherwise an error is returned.
// It never returns an empty key with a nil error.
func resolveKey(lc config.LocalAuthConfig, label string) ([]byte, error) {
	if lc.JWTSecret != "" {
		return []byte(lc.JWTSecret), nil
	}
	if lc.JWTAlg == config.JWTAlgEdDSA && len(lc.JWTSigningKey) > 0 {
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
// JWT_SECRET when set, otherwise a key derived from the EdDSA signing key.
func OAuthStateKey(lc config.LocalAuthConfig) ([]byte, error) {
	return resolveKey(lc, LabelOAuthState)
}

// StepUpKey returns the HMAC key for step-up tokens: the explicit JWT_SECRET
// when set, otherwise a key derived from the EdDSA signing key.
func StepUpKey(lc config.LocalAuthConfig) ([]byte, error) {
	return resolveKey(lc, LabelStepUp)
}
