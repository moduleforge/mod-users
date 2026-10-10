package config

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/json"
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
	return ValidateKID(kid)
}

// ValidateKID reports whether kid is acceptable as a JWT "kid": empty, or a
// short printable-ASCII token without whitespace.
func ValidateKID(kid string) error {
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

// maxJWTVerifyKeys bounds JWT_VERIFY_PUBLIC_KEYS; rotation needs only a handful.
const maxJWTVerifyKeys = 32

// ParseEd25519PublicKeyPEM parses a PKIX PEM-encoded Ed25519 public key
// ("-----BEGIN PUBLIC KEY-----"). Like ParseEd25519PrivateKeyPEM it accepts
// literal "\n" sequences in place of newlines, and its errors are fixed
// strings that never echo the input.
func ParseEd25519PublicKeyPEM(s string) (ed25519.PublicKey, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, `\n`, "\n"))
	block, _ := pem.Decode([]byte(s))
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("must be a PKIX PEM block (BEGIN PUBLIC KEY)")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, errors.New("not a valid PKIX public key")
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("must be an Ed25519 public key")
	}
	return key, nil
}

// loadJWTVerifyPublicKeys reads JWT_VERIFY_PUBLIC_KEYS: either a single PKIX
// PEM public key (stored under the empty kid, for tokens without a "kid"
// header) or a JSON object mapping kid to PEM public key. It returns nil when
// the variable is unset. Problems are returned as messages for Load to report
// with all other config errors; none contains key material or the raw value.
func loadJWTVerifyPublicKeys() (map[string]ed25519.PublicKey, []string) {
	raw := strings.TrimSpace(os.Getenv("JWT_VERIFY_PUBLIC_KEYS"))
	if raw == "" {
		return nil, nil
	}
	const name = "JWT_VERIFY_PUBLIC_KEYS: "

	if !strings.HasPrefix(raw, "{") {
		k, err := ParseEd25519PublicKeyPEM(raw)
		if err != nil {
			return nil, []string{name + err.Error()}
		}
		return map[string]ed25519.PublicKey{"": k}, nil
	}

	var entries map[string]string
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, []string{name + "must be a PEM public key or a JSON object mapping kid to PEM public key"}
	}
	if len(entries) == 0 {
		return nil, []string{name + "no keys in JSON object"}
	}
	if len(entries) > maxJWTVerifyKeys {
		return nil, []string{fmt.Sprintf("%stoo many keys (max %d)", name, maxJWTVerifyKeys)}
	}
	var problems []string
	keys := make(map[string]ed25519.PublicKey, len(entries))
	for kid, pemStr := range entries {
		if kid == "" {
			problems = append(problems, name+"kid must not be empty in a JSON object")
			continue
		}
		if err := ValidateKID(kid); err != nil {
			problems = append(problems, name+"kid: "+err.Error())
			continue
		}
		k, err := ParseEd25519PublicKeyPEM(pemStr)
		if err != nil {
			// kid was validated as printable ASCII above, so it is safe to name.
			problems = append(problems, fmt.Sprintf("%skey for kid %q: %s", name, kid, err.Error()))
			continue
		}
		keys[kid] = k
	}
	if len(problems) > 0 {
		return nil, problems
	}
	return keys, nil
}
