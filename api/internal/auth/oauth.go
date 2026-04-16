package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/moduleforge/users-module/api/internal/config"
)

// stateTTL is how long a state token (and its cookie) remain valid. Five
// minutes matches the spec and is comfortably longer than a typical IdP
// round-trip while staying short enough to resist replay.
const stateTTL = 5 * time.Minute

// OAuth orchestrates the browser-facing OAuth 2.0 authorization-code flow
// across a set of configured OIDC providers. It is safe for concurrent use
// after NewOAuth returns.
type OAuth struct {
	Registry          config.ProviderRegistry
	Verifiers         map[string]*oidc.IDTokenVerifier
	OAuthConfigs      map[string]*oauth2.Config
	Mappers           map[string]ClaimMapper
	StateSigner       *StateSigner
	RedirectBase      string
	FrontendReturnURL string
}

// NewOAuth builds an OAuth for every provider in cfg.Providers. Each
// provider's discovery document is fetched eagerly so the caller fails fast
// on a misconfigured provider rather than serving a broken /start route later.
func NewOAuth(ctx context.Context, cfg *config.Config) (*OAuth, error) {
	if cfg == nil {
		return nil, errors.New("oauth: nil config")
	}
	if len(cfg.Providers) == 0 {
		// Zero providers is a valid state — return a shell that always 404s.
		signer, err := NewStateSigner([]byte(cfg.LocalAuth.JWTSecret))
		if err != nil {
			return nil, err
		}
		return &OAuth{
			Registry:          cfg.Providers,
			Verifiers:         map[string]*oidc.IDTokenVerifier{},
			OAuthConfigs:      map[string]*oauth2.Config{},
			Mappers:           map[string]ClaimMapper{},
			StateSigner:       signer,
			RedirectBase:      cfg.Auth.OAuthRedirectBaseURL,
			FrontendReturnURL: cfg.Auth.FrontendReturnURL,
		}, nil
	}

	if cfg.LocalAuth.JWTSecret == "" {
		return nil, errors.New("oauth: JWT_SECRET is required to sign state tokens")
	}
	if cfg.Auth.OAuthRedirectBaseURL == "" {
		return nil, errors.New("oauth: AUTH_OAUTH_REDIRECT_BASE_URL is required when providers are enabled")
	}
	if cfg.Auth.FrontendReturnURL == "" {
		return nil, errors.New("oauth: AUTH_FRONTEND_RETURN_URL is required when providers are enabled")
	}

	signer, err := NewStateSigner([]byte(cfg.LocalAuth.JWTSecret))
	if err != nil {
		return nil, err
	}

	verifiers := make(map[string]*oidc.IDTokenVerifier, len(cfg.Providers))
	oauthConfigs := make(map[string]*oauth2.Config, len(cfg.Providers))
	mappers := make(map[string]ClaimMapper, len(cfg.Providers))

	for id, p := range cfg.Providers {
		provider, err := oidc.NewProvider(ctx, p.IssuerURL)
		if err != nil {
			return nil, fmt.Errorf("oauth: provider %q discovery: %w", id, err)
		}
		verifiers[id] = provider.Verifier(&oidc.Config{ClientID: p.ClientID})

		mapper, err := NewClaimMapper(p.ClaimStyle, MapperOptions{AdminRole: cfg.Auth.AdminRole})
		if err != nil {
			return nil, fmt.Errorf("oauth: provider %q claim mapper: %w", id, err)
		}
		mappers[id] = mapper

		oauthConfigs[id] = &oauth2.Config{
			ClientID:     p.ClientID,
			ClientSecret: p.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  buildCallbackURL(cfg.Auth.OAuthRedirectBaseURL, id),
			Scopes:       p.Scopes,
		}
	}

	return &OAuth{
		Registry:          cfg.Providers,
		Verifiers:         verifiers,
		OAuthConfigs:      oauthConfigs,
		Mappers:           mappers,
		StateSigner:       signer,
		RedirectBase:      cfg.Auth.OAuthRedirectBaseURL,
		FrontendReturnURL: cfg.Auth.FrontendReturnURL,
	}, nil
}

// buildCallbackURL joins a base URL with the callback path for a given
// provider. It tolerates a base URL with or without a trailing slash.
func buildCallbackURL(base, providerID string) string {
	return strings.TrimRight(base, "/") + "/v1/auth/oidc/" + providerID + "/callback"
}

// ErrUnknownProvider is returned when the caller references a provider that
// is not in the registry.
var ErrUnknownProvider = errors.New("oauth: unknown provider")

// AuthorizeURL builds the OIDC authorization URL the browser should be
// redirected to, along with the signed state token that must be stored in
// the oidc_state cookie so the callback can verify it.
func (o *OAuth) AuthorizeURL(providerID, returnPath string) (authorizeURL, stateToken string, err error) {
	cfg, ok := o.OAuthConfigs[providerID]
	if !ok {
		return "", "", ErrUnknownProvider
	}

	returnPath, err = validateReturnPath(returnPath)
	if err != nil {
		return "", "", err
	}

	nonce, err := randomBase64(32)
	if err != nil {
		return "", "", err
	}

	payload := StatePayload{
		Provider:   providerID,
		ReturnPath: returnPath,
		Nonce:      nonce,
		Expires:    time.Now().Add(stateTTL).Unix(),
	}
	token, err := o.StateSigner.Sign(payload)
	if err != nil {
		return "", "", err
	}

	// The OIDC nonce must match between the auth URL and the id_token claim.
	authURL := cfg.AuthCodeURL(token, oidc.Nonce(nonce))
	return authURL, token, nil
}

// Exchange verifies the state parameter, trades the authorization code for
// tokens at the provider's token endpoint, validates the resulting id_token,
// and returns a normalized Principal plus the recovered state payload (so
// the caller can redirect to the return path).
func (o *OAuth) Exchange(ctx context.Context, providerID, code, rawState, cookieState string) (Principal, StatePayload, error) {
	var empty Principal
	var emptyPayload StatePayload

	cfg, ok := o.OAuthConfigs[providerID]
	if !ok {
		return empty, emptyPayload, ErrUnknownProvider
	}
	verifier, ok := o.Verifiers[providerID]
	if !ok {
		return empty, emptyPayload, ErrUnknownProvider
	}
	mapper, ok := o.Mappers[providerID]
	if !ok {
		return empty, emptyPayload, ErrUnknownProvider
	}

	// State must be present in both the query string and the cookie, and the
	// two values must be byte-identical. Cookie tampering or a missing cookie
	// (e.g., browser blocked it, or the callback was hit without /start) is
	// treated as a mismatch.
	if rawState == "" || cookieState == "" {
		return empty, emptyPayload, errors.New("oauth: missing state")
	}
	if rawState != cookieState {
		return empty, emptyPayload, errors.New("oauth: state cookie mismatch")
	}

	payload, err := o.StateSigner.Verify(rawState)
	if err != nil {
		return empty, emptyPayload, fmt.Errorf("oauth: %w", err)
	}
	if payload.Provider != providerID {
		return empty, emptyPayload, errors.New("oauth: state provider mismatch")
	}

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return empty, emptyPayload, fmt.Errorf("oauth: token exchange: %w", err)
	}

	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return empty, emptyPayload, errors.New("oauth: provider did not return an id_token")
	}

	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return empty, emptyPayload, fmt.Errorf("oauth: verify id_token: %w", err)
	}

	if idToken.Nonce != payload.Nonce {
		return empty, emptyPayload, errors.New("oauth: nonce mismatch")
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return empty, emptyPayload, fmt.Errorf("oauth: parse id_token claims: %w", err)
	}

	principal, err := mapper.Map(claims)
	if err != nil {
		return empty, emptyPayload, fmt.Errorf("oauth: map claims: %w", err)
	}

	return principal, payload, nil
}

// validateReturnPath ensures the return-path parameter is a site-relative
// path. Anything with a scheme, authority, or that doesn't start with '/'
// is rejected to prevent open-redirect attacks.
func validateReturnPath(raw string) (string, error) {
	if raw == "" {
		return "/", nil
	}
	if !strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("oauth: return path must begin with '/', got %q", raw)
	}
	// A protocol-relative path like "//example.com/evil" would pass the
	// leading-slash check but url.Parse would give it a Host. Explicitly reject.
	if strings.HasPrefix(raw, "//") {
		return "", fmt.Errorf("oauth: return path must not be protocol-relative")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("oauth: invalid return path %q: %w", raw, err)
	}
	if u.Scheme != "" || u.Host != "" {
		return "", fmt.Errorf("oauth: return path must not include scheme or host: %q", raw)
	}
	return raw, nil
}

// randomBase64 returns a cryptographically random byte string encoded as
// url-safe base64 without padding.
func randomBase64(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("oauth: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
