package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Verifier validates JWT tokens. It supports both OIDC (RS256 via JWKS)
// and local (HS256) tokens.
type Verifier struct {
	oidcVerifier *oidc.IDTokenVerifier
	localSecret  []byte
	localIssuer  string
}

// NewVerifier creates a JWT verifier. It discovers JWKS from the OIDC issuer
// and also accepts local HS256 tokens signed with jwtSecret.
func NewVerifier(ctx context.Context, issuerURL, clientID, jwtSecret, localIssuer string) (*Verifier, error) {
	v := &Verifier{
		localSecret: []byte(jwtSecret),
		localIssuer: localIssuer,
	}

	if issuerURL != "" {
		provider, err := oidc.NewProvider(ctx, issuerURL)
		if err != nil {
			return nil, fmt.Errorf("auth: oidc provider discovery: %w", err)
		}
		v.oidcVerifier = provider.Verifier(&oidc.Config{ClientID: clientID})
	}

	return v, nil
}

// Verify validates a raw JWT string and returns the claims.
// It tries OIDC verification first, then falls back to local HS256.
func (v *Verifier) Verify(ctx context.Context, rawToken string) (map[string]any, error) {
	// Try local HS256 first if the token's issuer matches.
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

// verifyLocal validates an HS256 JWT against the local secret.
func (v *Verifier) verifyLocal(rawToken string) (map[string]any, error) {
	parts := strings.Split(rawToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid jwt format")
	}

	// Verify signature.
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, v.localSecret)
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, errors.New("invalid signature")
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
