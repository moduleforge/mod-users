package config

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Supported values for JWT_ALG.
const (
	// JWTAlgHS256 is the default: locally-issued JWTs are HMAC-signed with
	// JWT_SECRET.
	JWTAlgHS256 = "HS256"
	// JWTAlgEdDSA signs locally-issued JWTs with an Ed25519 private key
	// supplied via JWT_SIGNING_KEY.
	JWTAlgEdDSA = "EdDSA"
)

// maxJWTKIDLen bounds JWT_SIGNING_KID; a kid is an opaque short identifier.
const maxJWTKIDLen = 128

// loadJWTSigning reads JWT_ALG, JWT_SIGNING_KEY, and JWT_SIGNING_KID from the
// environment and returns the validated algorithm, parsed Ed25519 private key
// (nil unless the algorithm is EdDSA), and kid. Every problem is returned as a
// message in problems so Load can report them alongside all other config
// errors. No message ever contains key material or the raw env value.
func loadJWTSigning() (alg string, key ed25519.PrivateKey, kid string, problems []string) {
	alg = JWTAlgHS256
	if v := strings.TrimSpace(os.Getenv("JWT_ALG")); v != "" {
		switch v {
		case JWTAlgHS256, JWTAlgEdDSA:
			alg = v
		default:
			problems = append(problems,
				"JWT_ALG: invalid value (must be HS256 or EdDSA)")
		}
	}

	kid = strings.TrimSpace(os.Getenv("JWT_SIGNING_KID"))
	if err := validateKID(kid); err != nil {
		problems = append(problems, "JWT_SIGNING_KID: "+err.Error())
		kid = ""
	}

	if alg == JWTAlgEdDSA {
		raw := os.Getenv("JWT_SIGNING_KEY")
		if strings.TrimSpace(raw) == "" {
			problems = append(problems, "JWT_SIGNING_KEY: required when JWT_ALG=EdDSA")
		} else {
			k, err := ParseEd25519PrivateKeyPEM(raw)
			if err != nil {
				problems = append(problems, "JWT_SIGNING_KEY: "+err.Error())
			} else {
				key = k
			}
		}
	}
	return alg, key, kid, problems
}

// validateKID accepts an empty kid (not configured) or a short printable-ASCII
// token without whitespace. The kid lands in a JWT header and in operator
// logs, so control characters and whitespace are rejected at the boundary.
func validateKID(kid string) error {
	if len(kid) > maxJWTKIDLen {
		return fmt.Errorf("too long (max %d bytes)", maxJWTKIDLen)
	}
	for i := 0; i < len(kid); i++ {
		if c := kid[i]; c <= 0x20 || c >= 0x7f {
			return errors.New("must contain only printable ASCII without whitespace")
		}
	}
	return nil
}

// ParseEd25519PrivateKeyPEM parses a PKCS#8 PEM-encoded Ed25519 private key
// ("-----BEGIN PRIVATE KEY-----"). Because multi-line values are awkward in
// env files, literal two-character "\n" sequences are accepted in place of
// newlines. The returned errors are fixed strings that never echo the input.
func ParseEd25519PrivateKeyPEM(s string) (ed25519.PrivateKey, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, `\n`, "\n"))
	block, _ := pem.Decode([]byte(s))
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("must be a PKCS#8 PEM block (BEGIN PRIVATE KEY)")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, errors.New("not a valid PKCS#8 private key")
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("must be an Ed25519 private key")
	}
	return key, nil
}
