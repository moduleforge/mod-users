package auth

import (
	"crypto/ed25519"
	"errors"

	"github.com/golang-jwt/jwt/v5"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// Signer signs locally-issued JWTs with an Ed25519 private key (EdDSA, the
// only algorithm this module issues) and stamps the optional "kid" header. A
// Signer is immutable and safe for concurrent use.
//
// The claims a Signer signs are fixed by carrier.go: interactive and
// client-credentials access tokens (IssueSessionToken,
// IssueClientCredentialsToken) and audience-bound tokens for offline
// verifiers (IssueAudienceToken).
type Signer struct {
	key ed25519.PrivateKey
	kid string
}

// NewEdDSASigner returns a Signer that signs with the Ed25519 private key.
// kid is optional; when empty no "kid" header is set.
func NewEdDSASigner(key ed25519.PrivateKey, kid string) (*Signer, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("signer: invalid Ed25519 private key")
	}
	if err := config.ValidateKID(kid); err != nil {
		return nil, errors.New("signer: invalid kid")
	}
	return &Signer{key: key, kid: kid}, nil
}

// NewSigner builds the Signer for the local-auth configuration: the Ed25519
// signing key (JWT_SIGNING_KEY or the persisted bootstrap key) and the
// optional JWT_SIGNING_KID.
func NewSigner(lc config.LocalAuthConfig) (*Signer, error) {
	return NewEdDSASigner(lc.JWTSigningKey, lc.JWTSigningKID)
}

// Alg returns the JWT "alg" header value this Signer produces ("EdDSA").
func (s *Signer) Alg() string { return jwt.SigningMethodEdDSA.Alg() }

// Sign serializes and signs claims. Errors never include key material.
func (s *Signer) Sign(claims jwt.Claims) (string, error) {
	if s == nil || len(s.key) != ed25519.PrivateKeySize {
		return "", errors.New("signer: nil signer")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	if s.kid != "" {
		token.Header["kid"] = s.kid
	}
	return token.SignedString(s.key)
}
