package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// Algorithm names a local token may be signed with; the verifier pins the
// accepted set per configured key material and never trusts the token's own
// claim of what it should be verified with.
const (
	algHS256 = "HS256"
	algEdDSA = "EdDSA"
)

// maxVerifyKeys bounds the number of concurrently valid public keys.
const maxVerifyKeys = 32

// Verifier validates JWT tokens. It supports OIDC (RS256 via JWKS) and local
// tokens: HS256 against a shared secret and/or EdDSA against a set of Ed25519
// public keys selected by the token's "kid" header. Which local algorithms
// are accepted is fixed by the configured key material: a Verifier with only
// public keys never accepts HS256, and one with only a secret never accepts
// EdDSA.
type Verifier struct {
	oidcVerifier *oidc.IDTokenVerifier
	localSecret  []byte
	localIssuer  string
	// edKeys maps kid to Ed25519 public key. The empty kid is the key used by
	// tokens that carry no "kid" header. Nil when EdDSA is not configured.
	edKeys map[string]ed25519.PublicKey
}

// verifierConfig is the accumulated state of VerifierOptions.
type verifierConfig struct {
	issuerURL   string
	clientID    string
	secret      string
	localIssuer string
	edKeys      map[string]ed25519.PublicKey
}

// VerifierOption configures NewVerifierWithOptions.
type VerifierOption func(*verifierConfig) error

// WithOIDC enables verification of OIDC provider tokens discovered from
// issuerURL.
func WithOIDC(issuerURL, clientID string) VerifierOption {
	return func(c *verifierConfig) error {
		c.issuerURL, c.clientID = issuerURL, clientID
		return nil
	}
}

// WithHS256Secret enables local HS256 tokens signed with secret. An empty
// secret is rejected: HMAC with an empty key would accept forged tokens.
func WithHS256Secret(secret string) VerifierOption {
	return func(c *verifierConfig) error {
		if secret == "" {
			return errors.New("auth: HS256 secret must not be empty")
		}
		c.secret = secret
		return nil
	}
}

// WithLocalIssuer sets the "iss" value local tokens must carry.
func WithLocalIssuer(issuer string) VerifierOption {
	return func(c *verifierConfig) error {
		c.localIssuer = issuer
		return nil
	}
}

// WithEd25519PublicKeys enables local EdDSA tokens verified against keys,
// selected by the token's "kid" header. The empty-string kid is the key used
// for tokens without a "kid" header. Every key in the set is valid
// concurrently (rotation); repeated use accumulates keys, and a duplicate kid
// is an error. The map and keys are copied.
func WithEd25519PublicKeys(keys map[string]ed25519.PublicKey) VerifierOption {
	return func(c *verifierConfig) error {
		if len(keys) == 0 {
			return errors.New("auth: no Ed25519 public keys supplied")
		}
		if c.edKeys == nil {
			c.edKeys = make(map[string]ed25519.PublicKey, len(keys))
		}
		for kid, k := range keys {
			if err := config.ValidateKID(kid); err != nil {
				return fmt.Errorf("auth: Ed25519 public key kid: %w", err)
			}
			if len(k) != ed25519.PublicKeySize {
				return errors.New("auth: invalid Ed25519 public key length")
			}
			if _, dup := c.edKeys[kid]; dup {
				return errors.New("auth: duplicate Ed25519 public key kid")
			}
			if len(c.edKeys) >= maxVerifyKeys {
				return fmt.Errorf("auth: too many Ed25519 public keys (max %d)", maxVerifyKeys)
			}
			c.edKeys[kid] = append(ed25519.PublicKey(nil), k...)
		}
		return nil
	}
}

// WithEd25519PublicKey enables local EdDSA tokens verified against a single
// key used for tokens that carry no "kid" header.
func WithEd25519PublicKey(key ed25519.PublicKey) VerifierOption {
	return WithEd25519PublicKeys(map[string]ed25519.PublicKey{"": key})
}

// NewVerifierWithOptions creates a JWT verifier from options. At least one
// token source (OIDC, an HS256 secret, or Ed25519 public keys) must be
// configured, and a local issuer is required when any local algorithm is.
// A public-key-only Verifier needs no signing secret.
func NewVerifierWithOptions(ctx context.Context, opts ...VerifierOption) (*Verifier, error) {
	var c verifierConfig
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(&c); err != nil {
			return nil, err
		}
	}
	local := c.secret != "" || len(c.edKeys) > 0
	if !local && c.issuerURL == "" {
		return nil, errors.New("auth: verifier requires an HS256 secret, Ed25519 public keys, or an OIDC issuer")
	}
	if local && c.localIssuer == "" {
		return nil, errors.New("auth: local issuer is required when local token verification is configured")
	}
	return newVerifier(ctx, c)
}

// NewLocalVerifier builds the local-only Verifier matching the local-auth
// configuration: the algorithm accepted follows JWT_ALG. Under EdDSA it
// accepts only Ed25519 (the public half of JWT_SIGNING_KEY under
// JWT_SIGNING_KID, plus any JWT_VERIFY_PUBLIC_KEYS for rotation) and does not
// accept HS256; under HS256 it accepts JWT_SECRET tokens plus any configured
// public keys.
func NewLocalVerifier(ctx context.Context, lc config.LocalAuthConfig) (*Verifier, error) {
	opts := []VerifierOption{WithLocalIssuer(lc.LocalIssuer)}
	keys := make(map[string]ed25519.PublicKey, len(lc.JWTVerifyPublicKeys)+1)
	for kid, k := range lc.JWTVerifyPublicKeys {
		keys[kid] = k
	}
	switch lc.JWTAlg {
	case "", config.JWTAlgHS256:
		opts = append(opts, WithHS256Secret(lc.JWTSecret))
	case config.JWTAlgEdDSA:
		if len(lc.JWTSigningKey) != ed25519.PrivateKeySize {
			return nil, errors.New("auth: invalid Ed25519 signing key")
		}
		pub, ok := lc.JWTSigningKey.Public().(ed25519.PublicKey)
		if !ok {
			return nil, errors.New("auth: invalid Ed25519 signing key")
		}
		if existing, dup := keys[lc.JWTSigningKID]; dup && !existing.Equal(pub) {
			return nil, errors.New("auth: JWT_VERIFY_PUBLIC_KEYS kid conflicts with JWT_SIGNING_KID")
		}
		keys[lc.JWTSigningKID] = pub
	default:
		return nil, fmt.Errorf("auth: unsupported JWT_ALG %q", lc.JWTAlg)
	}
	if len(keys) > 0 {
		opts = append(opts, WithEd25519PublicKeys(keys))
	}
	return NewVerifierWithOptions(ctx, opts...)
}

// NewVerifier creates a JWT verifier. It discovers JWKS from the OIDC issuer
// and also accepts local HS256 tokens signed with jwtSecret. It is the
// backward-compatible form of NewVerifierWithOptions; an empty jwtSecret
// yields a verifier that accepts no local tokens.
func NewVerifier(ctx context.Context, issuerURL, clientID, jwtSecret, localIssuer string) (*Verifier, error) {
	return newVerifier(ctx, verifierConfig{
		issuerURL:   issuerURL,
		clientID:    clientID,
		secret:      jwtSecret,
		localIssuer: localIssuer,
	})
}

func newVerifier(ctx context.Context, c verifierConfig) (*Verifier, error) {
	v := &Verifier{
		localSecret: []byte(c.secret),
		localIssuer: c.localIssuer,
		edKeys:      c.edKeys,
	}
	issuerURL, clientID := c.issuerURL, c.clientID

	if issuerURL != "" {
		// For local dev with self-signed certs, use an HTTP client that
		// skips TLS verification. In production, use the default client.
		if strings.HasPrefix(issuerURL, "https://") {
			insecureClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				},
			}
			ctx = oidc.ClientContext(ctx, insecureClient)
		}
		provider, err := oidc.NewProvider(ctx, issuerURL)
		if err != nil {
			return nil, fmt.Errorf("auth: oidc provider discovery: %w", err)
		}
		v.oidcVerifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	}

	return v, nil
}

// Verify validates a raw JWT string and returns the claims.
// It tries local verification (HS256 and/or EdDSA, per configuration) first,
// then falls back to OIDC.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (map[string]any, error) {
	// Try local verification first.
	if claims, err := v.verifyLocal(rawToken); err == nil {
		return claims, nil
	}

	// Try OIDC.
	if v.oidcVerifier != nil {
		idToken, err := v.oidcVerifier.Verify(ctx, rawToken)
		if err == nil {
			var claims map[string]any
			if err := idToken.Claims(&claims); err != nil {
				return nil, fmt.Errorf("auth: parse oidc claims: %w", err)
			}
			return claims, nil
		}
	}

	return nil, errors.New("auth: token verification failed")
}

// verifyLocal validates a locally-issued JWT. The algorithm is pinned by the
// configured key material, never taken from the token: the header "alg" must
// be exactly HS256 (and a secret configured) or EdDSA (and public keys
// configured); everything else — including "none" — is rejected before any
// signature check, so an HS256 token can never be verified using a public key
// as the HMAC secret and vice versa.
func (v *Verifier) verifyLocal(rawToken string) (map[string]any, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt format")
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("invalid jwt header")
	}
	var header struct {
		Alg string          `json:"alg"`
		Kid json.RawMessage `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, errors.New("invalid jwt header")
	}

	signingInput := parts[0] + "." + parts[1]
	switch header.Alg {
	case algHS256:
		if len(v.localSecret) == 0 {
			return nil, errors.New("hs256 not accepted")
		}
		mac := hmac.New(sha256.New, v.localSecret)
		mac.Write([]byte(signingInput))
		expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
			return nil, errors.New("invalid signature")
		}
	case algEdDSA:
		if len(v.edKeys) == 0 {
			return nil, errors.New("eddsa not accepted")
		}
		kid := ""
		if len(header.Kid) > 0 && string(header.Kid) != "null" {
			if err := json.Unmarshal(header.Kid, &kid); err != nil {
				return nil, errors.New("invalid kid")
			}
		}
		key, ok := v.edKeys[kid]
		if !ok {
			return nil, errors.New("unknown kid")
		}
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil || len(sig) != ed25519.SignatureSize {
			return nil, errors.New("invalid signature")
		}
		if !ed25519.Verify(key, []byte(signingInput), sig) {
			return nil, errors.New("invalid signature")
		}
	default:
		return nil, errors.New("unsupported alg")
	}

	// Decode payload.
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("unmarshal claims: %w", err)
	}

	// Verify issuer matches local.
	iss, _ := claims["iss"].(string)
	if iss != v.localIssuer {
		return nil, errors.New("issuer mismatch")
	}

	// Verify expiry.
	exp, ok := claims["exp"].(float64)
	if !ok || time.Unix(int64(exp), 0).Before(time.Now()) {
		return nil, errors.New("token expired")
	}

	return claims, nil
}
