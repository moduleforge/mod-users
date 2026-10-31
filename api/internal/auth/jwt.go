package auth

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// algEdDSA is the only algorithm a local token may be signed with. The
// verifier pins it and never trusts the token's own claim of what it should
// be verified with.
const algEdDSA = "EdDSA"

// maxVerifyKeys bounds the number of concurrently valid public keys.
const maxVerifyKeys = 32

// Verifier validates JWT tokens. It supports OIDC (RS256 via JWKS, for
// external identity providers) and local EdDSA tokens verified against a set
// of Ed25519 public keys selected by the token's "kid" header. Local tokens
// signed with any other algorithm are rejected.
type Verifier struct {
	oidcVerifier *oidc.IDTokenVerifier
	localIssuer  string
	// edKeys maps kid to Ed25519 public key. The empty kid is the key used by
	// tokens that carry no "kid" header. Nil when no local keys are configured.
	edKeys map[string]ed25519.PublicKey
}

// verifierConfig is the accumulated state of VerifierOptions.
type verifierConfig struct {
	issuerURL   string
	clientID    string
	localIssuer string
	edKeys      map[string]ed25519.PublicKey
	// insecureSkipTLSVerify disables TLS certificate verification for OIDC
	// discovery and JWKS fetches. Local development only.
	insecureSkipTLSVerify bool
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

// WithInsecureSkipTLSVerify disables TLS certificate verification when
// discovering the OIDC issuer and fetching its JWKS. It is for LOCAL
// DEVELOPMENT ONLY (e.g. an issuer with a self-signed certificate) and must
// never be enabled in production: it exposes token verification to
// man-in-the-middle attacks. It has no effect on non-https issuers.
func WithInsecureSkipTLSVerify() VerifierOption {
	return func(c *verifierConfig) error {
		c.insecureSkipTLSVerify = true
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
// token source (OIDC or Ed25519 public keys) must be configured, and a local
// issuer is required when public keys are. A public-key-only Verifier needs
// no private key.
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
	local := len(c.edKeys) > 0
	if !local && c.issuerURL == "" {
		return nil, errors.New("auth: verifier requires Ed25519 public keys or an OIDC issuer")
	}
	if local && c.localIssuer == "" {
		return nil, errors.New("auth: local issuer is required when local token verification is configured")
	}
	return newVerifier(ctx, c)
}

// NewLocalVerifier builds the local-only Verifier matching the local-auth
// configuration. It accepts only EdDSA tokens: the public half of the signing
// key under JWT_SIGNING_KID, plus any JWT_VERIFY_PUBLIC_KEYS (rotation).
func NewLocalVerifier(ctx context.Context, lc config.LocalAuthConfig) (*Verifier, error) {
	if len(lc.JWTSigningKey) != ed25519.PrivateKeySize {
		return nil, errors.New("auth: invalid Ed25519 signing key")
	}
	pub, ok := lc.JWTSigningKey.Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("auth: invalid Ed25519 signing key")
	}
	keys := make(map[string]ed25519.PublicKey, len(lc.JWTVerifyPublicKeys)+1)
	for kid, k := range lc.JWTVerifyPublicKeys {
		keys[kid] = k
	}
	if existing, dup := keys[lc.JWTSigningKID]; dup && !existing.Equal(pub) {
		return nil, errors.New("auth: JWT_VERIFY_PUBLIC_KEYS kid conflicts with JWT_SIGNING_KID")
	}
	keys[lc.JWTSigningKID] = pub
	return NewVerifierWithOptions(ctx, WithLocalIssuer(lc.LocalIssuer), WithEd25519PublicKeys(keys))
}

func newVerifier(ctx context.Context, c verifierConfig) (*Verifier, error) {
	v := &Verifier{
		localIssuer: c.localIssuer,
		edKeys:      c.edKeys,
	}
	issuerURL, clientID := c.issuerURL, c.clientID

	if issuerURL != "" {
		// TLS certificates are verified by default. Skipping verification
		// requires the explicit local-development opt-in.
		if c.insecureSkipTLSVerify && strings.HasPrefix(issuerURL, "https://") {
			host := issuerURL
			if u, perr := url.Parse(issuerURL); perr == nil {
				host = u.Host
			}
			slog.Warn("auth: TLS verification is disabled for OIDC issuer; local development only", "issuer_host", host)
			insecureClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // explicit local-dev opt-in
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
// It tries local EdDSA verification first, then falls back to OIDC. Access
// tokens presented as bearer credentials go through Authenticator instead,
// which uses local verification only.
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

// verifyLocalSignature checks a locally-issued JWT's signature and returns
// its decoded payload bytes. The algorithm is pinned: the header "alg" must be
// exactly EdDSA and public keys must be configured; everything else —
// including "none" and every HMAC algorithm — is rejected before any
// signature check. Claims are not inspected here.
func (v *Verifier) verifyLocalSignature(rawToken string) ([]byte, error) {
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
	if header.Alg != algEdDSA {
		return nil, errors.New("unsupported alg")
	}
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
	if !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, errors.New("invalid signature")
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid jwt payload")
	}
	return payload, nil
}

// verifyLocal validates a locally-issued JWT (signature, "iss", "exp") and
// returns its claims. Audience and carrier-specific claims are checked by
// verifyCarrier (carrier.go), which bearer authentication uses.
func (v *Verifier) verifyLocal(rawToken string) (map[string]any, error) {
	payload, err := v.verifyLocalSignature(rawToken)
	if err != nil {
		return nil, err
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, errors.New("invalid jwt payload")
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
