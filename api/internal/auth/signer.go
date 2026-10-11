package auth

import (
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/golang-jwt/jwt/v5"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// Signer signs locally-issued JWTs. It selects the signing method and key —
// HS256 with a shared secret, or EdDSA (Ed25519) with a private key — and
// stamps the optional "kid" header. A Signer is immutable and safe for
// concurrent use.
type Signer struct {
	method jwt.SigningMethod
	key    any
	kid    string
}

// NewHS256Signer returns a Signer that HMAC-SHA256-signs with secret. kid is
// optional; when empty no "kid" header is set, so the output is identical to
// the historical HS256 tokens. An empty secret is rejected: HMAC with an empty
// key produces tokens anyone can forge.
func NewHS256Signer(secret, kid string) (*Signer, error) {
	if secret == "" {
		return nil, errors.New("local_jwt: HS256 secret must not be empty")
	}
	return &Signer{method: jwt.SigningMethodHS256, key: []byte(secret), kid: kid}, nil
}

// NewEdDSASigner returns a Signer that signs with the Ed25519 private key.
// kid is optional.
func NewEdDSASigner(key ed25519.PrivateKey, kid string) (*Signer, error) {
	if len(key) != ed25519.PrivateKeySize {
		return nil, errors.New("local_jwt: invalid Ed25519 private key")
	}
	return &Signer{method: jwt.SigningMethodEdDSA, key: key, kid: kid}, nil
}

// NewSigner builds the Signer selected by the local-auth configuration:
// EdDSA when lc.JWTAlg is "EdDSA", otherwise (including the empty default)
// HS256 keyed by lc.JWTSecret.
func NewSigner(lc config.LocalAuthConfig) (*Signer, error) {
	switch lc.JWTAlg {
	case "", config.JWTAlgHS256:
		return NewHS256Signer(lc.JWTSecret, lc.JWTSigningKID)
	case config.JWTAlgEdDSA:
		return NewEdDSASigner(lc.JWTSigningKey, lc.JWTSigningKID)
	default:
		return nil, fmt.Errorf("local_jwt: unsupported JWT_ALG %q", lc.JWTAlg)
	}
}

// Alg returns the JWT "alg" header value this Signer produces.
func (s *Signer) Alg() string { return s.method.Alg() }

// Sign serializes and signs claims. Errors never include key material.
func (s *Signer) Sign(claims jwt.Claims) (string, error) {
	if s == nil || s.method == nil {
		return "", errors.New("local_jwt: nil signer")
	}
	token := jwt.NewWithClaims(s.method, claims)
	if s.kid != "" {
		token.Header["kid"] = s.kid
	}
	return token.SignedString(s.key)
}
