package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/moduleforge/users-module/api/internal/config"
)

// microsoftTenantIssuerRe matches the tenant-specific issuer URL Microsoft
// puts in id_tokens issued from the multi-tenant (/common, /organizations,
// /consumers) v2.0 endpoints. Tenant IDs are UUIDs (36 hex-and-dash chars);
// the personal-MSA tenant 9188040d-6c67-4c5b-b112-36a304b66dad matches the
// same shape so no special case is needed.
var microsoftTenantIssuerRe = regexp.MustCompile(`^https://login\.microsoftonline\.com/[0-9a-f-]{36}/v2\.0$`)

// isValidMicrosoftIssuer verifies that an id_token's `iss` claim points to a
// real tenant under login.microsoftonline.com. Callers must only invoke this
// for providers flagged as MultiTenantIssuer, since strict go-oidc issuer
// matching is disabled in that mode and this is the replacement check.
func isValidMicrosoftIssuer(iss string) bool {
	return microsoftTenantIssuerRe.MatchString(iss)
}

// multiTenantDiscoveryIssuer is the literal placeholder Microsoft returns
// in the `issuer` field of /common, /organizations, and /consumers discovery
// documents. Passed to oidc.InsecureIssuerURLContext so go-oidc accepts the
// discovery response without treating the placeholder as a real issuer.
const multiTenantDiscoveryIssuer = "https://login.microsoftonline.com/{tenantid}/v2.0"

// stateTTL is how long a state token (and its cookie) remain valid. Five
// minutes matches the spec and is comfortably longer than a typical IdP
// round-trip while staying short enough to resist replay.
const stateTTL = 5 * time.Minute

// ProviderState is the per-provider slot in the registry. It tracks whether
// the provider's OIDC discovery + claim-mapper construction succeeded so that
// one broken provider can't take down the whole API. When InitOK is true the
// Verifier / OAuthCfg / Mapper fields are populated; when InitOK is false
// Err carries the reason and the three init-only fields are nil.
//
// Keep this struct extensible — phase 9.7 adds MultiTenantIssuer to
// config.Provider (already embedded below), and future phases may add their
// own per-provider state without needing a wider refactor.
type ProviderState struct {
	ID       string
	Provider config.Provider
	InitOK   bool
	Err      error

	// Populated only when InitOK == true. Callers must check InitOK before
	// dereferencing; the helper OAuth.stateByID does this centrally.
	Verifier *oidc.IDTokenVerifier
	OAuthCfg *oauth2.Config
	Mapper   ClaimMapper
}

// OverallStatus describes the OAuth subsystem's boot state independent of any
// persisted (DB) configuration. Phase 9.9a layers a DB-aware state machine on
// top of this; at the oauth-registry layer we can only observe what env said
// and whether per-provider init succeeded.
type OverallStatus string

const (
	// StatusOK means at least one provider initialized successfully. OIDC
	// login is usable even if some providers individually failed.
	StatusOK OverallStatus = "ok"

	// StatusInitFailed means providers were configured via env but every one
	// of them failed to initialize. The API is still up (local auth works)
	// but no OIDC buttons will render.
	StatusInitFailed OverallStatus = "init_failed"

	// StatusNoEnvNoFlag means no providers were configured via env AND the
	// NO_OIDC_ACCOUNTS opt-out flag was not set. This is the "needs
	// onboarding" state: the operator hasn't said yes or no to OIDC.
	StatusNoEnvNoFlag OverallStatus = "no_env_no_flag"

	// StatusEmptyNoConsent means no providers were configured via env but
	// the operator explicitly opted out via NO_OIDC_ACCOUNTS. Local-auth-only
	// mode, intentional.
	StatusEmptyNoConsent OverallStatus = "empty_no_consent"
)

// OAuth orchestrates the browser-facing OAuth 2.0 authorization-code flow
// across a set of configured OIDC providers. It is safe for concurrent use
// after NewOAuth returns.
//
// The States map holds one entry per configured provider regardless of
// whether init succeeded. Handlers that need only ready providers should
// use EnabledProviders(); the onboarding flow (phase 9.9a) uses
// AllProviders() to render the full toggle list.
type OAuth struct {
	// mu guards States (the only mutable field). Rebuild takes the write
	// lock; every read path (EnabledProviders, AllProviders, Status,
	// stateByID, ProviderAvailable) takes the read lock. The remaining
	// fields are set once at construction and never mutated.
	mu                sync.RWMutex
	States            map[string]*ProviderState
	StateSigner       *StateSigner
	RedirectBase      string
	FrontendReturnURL string

	// cfg is the parent config retained so Rebuild can pass admin role /
	// redirect base / frontend return URL into initProvider without the
	// caller having to re-plumb them. It's read-only after construction.
	cfg *config.Config

	// envNoOIDCAccounts captures the NO_OIDC_ACCOUNTS env value at
	// construction time so Status() returns a stable answer without
	// re-reading os.Environ() on every call.
	envNoOIDCAccounts bool

	// ExchangeFn, when non-nil, replaces the real token-exchange logic.
	// Used only in tests to simulate a successful provider exchange without
	// a live IdP. Production code must leave this nil.
	ExchangeFn func(ctx context.Context, providerID, code, rawState, cookieState string) (Principal, StatePayload, error)
}

// NewOAuth builds an OAuth for every provider in cfg.Providers. Unlike the
// previous fail-fast behavior, a per-provider discovery failure no longer
// aborts startup: the bad provider is recorded with InitOK=false and skipped
// for AuthorizeURL / Exchange, and the caller can render the rest.
//
// Fatal (nil, error) conditions are limited to construction-level problems
// that toggling a provider off cannot fix:
//   - missing state-signer key (JWT_SECRET)
//   - missing OAUTH_REDIRECT_BASE_URL when providers are configured
//   - missing AUTH_FRONTEND_RETURN_URL when providers are configured
//
// All other failures (bogus issuer URL, unreachable discovery endpoint,
// unknown claim style) are surfaced via ProviderState.Err and a slog.Warn.
// Callers should consult oauth.Status() to decide whether to mount OIDC
// routes or enter an onboarding flow.
func NewOAuth(ctx context.Context, cfg *config.Config) (*OAuth, error) {
	if cfg == nil {
		return nil, errors.New("oauth: nil config")
	}

	envOptOut := parseBoolEnv(os.Getenv("NO_OIDC_ACCOUNTS"))

	// State signer is unconditionally required — without it we can't sign
	// the state cookie for any flow, including a future /start once a
	// provider is toggled on via onboarding.
	if cfg.LocalAuth.JWTSecret == "" {
		return nil, errors.New("oauth: JWT_SECRET is required to sign state tokens")
	}
	signer, err := NewStateSigner([]byte(cfg.LocalAuth.JWTSecret))
	if err != nil {
		return nil, err
	}

	// When providers are configured we must know where to receive callbacks
	// and where to hand the browser back. These are deployment-level config
	// problems an operator must fix in .env, not per-provider issues.
	if len(cfg.Providers) > 0 {
		if cfg.Auth.OAuthRedirectBaseURL == "" {
			return nil, errors.New("oauth: AUTH_OAUTH_REDIRECT_BASE_URL is required when providers are enabled")
		}
		if cfg.Auth.FrontendReturnURL == "" {
			return nil, errors.New("oauth: AUTH_FRONTEND_RETURN_URL is required when providers are enabled")
		}
	}

	states := make(map[string]*ProviderState, len(cfg.Providers))
	for id, p := range cfg.Providers {
		state := initProvider(ctx, id, p, cfg)
		states[id] = state
		if !state.InitOK {
			slog.WarnContext(ctx, "oauth: provider init failed",
				"provider", id,
				"error", state.Err,
			)
		}
	}

	return &OAuth{
		States:            states,
		StateSigner:       signer,
		RedirectBase:      cfg.Auth.OAuthRedirectBaseURL,
		FrontendReturnURL: cfg.Auth.FrontendReturnURL,
		cfg:               cfg,
		envNoOIDCAccounts: envOptOut,
	}, nil
}

// initProvider attempts to build the verifier/config/mapper for one provider.
// Any error is captured on the returned state with InitOK=false; callers
// never receive an error from this helper.
func initProvider(ctx context.Context, id string, p config.Provider, cfg *config.Config) *ProviderState {
	state := &ProviderState{ID: id, Provider: p}

	// Microsoft's multi-tenant discovery endpoints return a literal
	// "{tenantid}" placeholder in their `issuer` field, which go-oidc
	// would otherwise reject as a mismatch. Wrap the discovery context so
	// the library accepts the placeholder and defer real issuer validation
	// to Exchange, where we inspect idToken.Issuer directly.
	discoveryCtx := ctx
	if p.MultiTenantIssuer {
		discoveryCtx = oidc.InsecureIssuerURLContext(ctx, multiTenantDiscoveryIssuer)
	}
	provider, err := oidc.NewProvider(discoveryCtx, p.IssuerURL)
	if err != nil {
		state.Err = fmt.Errorf("provider %q discovery: %w", id, err)
		return state
	}

	mapper, err := NewClaimMapper(p.ClaimStyle, MapperOptions{AdminRole: cfg.Auth.AdminRole})
	if err != nil {
		state.Err = fmt.Errorf("provider %q claim mapper: %w", id, err)
		return state
	}

	state.Verifier = provider.Verifier(&oidc.Config{
		ClientID:        p.ClientID,
		SkipIssuerCheck: p.MultiTenantIssuer,
	})
	state.Mapper = mapper
	state.OAuthCfg = &oauth2.Config{
		ClientID:     p.ClientID,
		ClientSecret: p.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  buildCallbackURL(cfg.Auth.OAuthRedirectBaseURL, id),
		Scopes:       p.Scopes,
	}
	state.InitOK = true
	return state
}

// EnabledProviders returns the providers whose init succeeded, sorted by ID
// for stable ordering in responses (primarily /v1/auth/providers). Callers
// that want the full set including failures — e.g. the onboarding endpoint —
// should use AllProviders instead.
func (o *OAuth) EnabledProviders() []*ProviderState {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]*ProviderState, 0, len(o.States))
	for _, s := range o.States {
		if s.InitOK {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// AllProviders returns every configured provider — both ready and failed —
// sorted by ID. Intended for the onboarding / status endpoint where the
// operator needs to see which providers failed and why.
func (o *OAuth) AllProviders() []*ProviderState {
	o.mu.RLock()
	defer o.mu.RUnlock()
	out := make([]*ProviderState, 0, len(o.States))
	for _, s := range o.States {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Status reports the OAuth subsystem's observed boot state based purely on
// env + init outcomes. Phase 9.9a's DetermineBootState layers DB-persisted
// state on top of this to produce the full confirmed/unconfirmed flag that
// gates the onboarding redirect.
func (o *OAuth) Status() OverallStatus {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if len(o.States) == 0 {
		if o.envNoOIDCAccounts {
			return StatusEmptyNoConsent
		}
		return StatusNoEnvNoFlag
	}
	for _, s := range o.States {
		if s.InitOK {
			return StatusOK
		}
	}
	return StatusInitFailed
}

// Rebuild re-runs per-provider init against a new filtered registry
// (produced from the /v1/oidc-config/confirm handler by applying
// DB-persisted provider_enabled toggles on top of the env registry).
// It is goroutine-safe: the write lock is held only long enough to swap
// the new States map in; the per-provider init (which may block on
// network discovery) runs outside the lock to avoid stalling concurrent
// reads.
//
// Errors returned here are limited to construction-level failures that
// no per-provider retry can fix; individual provider init failures are
// captured on the new ProviderState entries (InitOK=false, Err set) and
// do not propagate out of Rebuild — the caller inspects Status() to see
// the new verdict.
func (o *OAuth) Rebuild(ctx context.Context, newRegistry config.ProviderRegistry) error {
	if o.cfg == nil {
		return errors.New("oauth: rebuild requires a cfg (OAuth built with legacy initializer)")
	}

	// Build the new state map outside the lock. initProvider is the same
	// helper NewOAuth uses, so retry logic, error wrapping, and
	// MultiTenantIssuer handling stay in a single place.
	newStates := make(map[string]*ProviderState, len(newRegistry))
	for id, p := range newRegistry {
		s := initProvider(ctx, id, p, o.cfg)
		newStates[id] = s
		if !s.InitOK {
			slog.WarnContext(ctx, "oauth rebuild: provider init failed",
				"provider", id,
				"error", s.Err,
			)
		}
	}

	// Swap atomically. Old ProviderState values are referenced only
	// through transient stateByID results already held by in-flight
	// requests; after the swap no new reads can see them, and the Go
	// garbage collector reclaims them once those requests finish.
	o.mu.Lock()
	o.States = newStates
	o.mu.Unlock()
	return nil
}

// EnvNoOIDCAccounts reports the NO_OIDC_ACCOUNTS env value captured at
// construction time. Exposed so DetermineBootState callers in main.go
// don't have to re-parse the env; a stable snapshot is also what Status()
// uses internally so this is the same bit.
func (o *OAuth) EnvNoOIDCAccounts() bool {
	return o.envNoOIDCAccounts
}

// ProviderAvailable reports whether the given provider ID is present in the
// registry AND successfully initialized. Handlers use this for cheap
// existence checks before committing to a full Exchange; a false return is
// the wire equivalent of "404 not found" regardless of which reason applies.
func (o *OAuth) ProviderAvailable(id string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	s, ok := o.States[id]
	return ok && s.InitOK
}

// stateByID fetches a ProviderState by ID. Two error modes:
//   - ErrUnknownProvider: the ID is not in the registry at all.
//   - ErrProviderNotAvailable: the provider exists but failed to initialize.
//
// Handlers use these sentinels to distinguish "404 not found" from "this
// particular provider is down right now".
func (o *OAuth) stateByID(id string) (*ProviderState, error) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	s, ok := o.States[id]
	if !ok {
		return nil, ErrUnknownProvider
	}
	if !s.InitOK {
		return nil, fmt.Errorf("%w: %q: %v", ErrProviderNotAvailable, id, s.Err)
	}
	return s, nil
}

// buildCallbackURL joins a base URL with the callback path for a given
// provider. It tolerates a base URL with or without a trailing slash.
func buildCallbackURL(base, providerID string) string {
	return strings.TrimRight(base, "/") + "/v1/auth/oidc/" + providerID + "/callback"
}

// ErrUnknownProvider is returned when the caller references a provider that
// is not in the registry.
var ErrUnknownProvider = errors.New("oauth: unknown provider")

// ErrProviderNotAvailable is returned when the provider exists in the
// registry but its per-provider init failed (bad issuer, unreachable
// discovery, etc.). Handlers map this to 404 so misconfigured providers
// behave indistinguishably from unknown ones at the wire — the operator
// sees the detailed reason in the slog.Warn emitted at boot.
var ErrProviderNotAvailable = errors.New("oauth: provider not available")

// ErrStateValidation is the sentinel wrapped by every state-related failure
// in Exchange (missing state, cookie mismatch, signature/expiry failure,
// provider-id mismatch). Handlers use errors.Is to distinguish client-fixable
// state problems from downstream IdP failures.
var ErrStateValidation = errors.New("oauth: state validation failed")

// AuthorizeURL builds the OIDC authorization URL the browser should be
// redirected to, along with the signed state token that must be stored in
// the oidc_state cookie so the callback can verify it. testMode is
// passed through to the StatePayload so the callback knows to skip
// UserResolver + JWT issuance and redirect to a test-result URL; the
// authorize URL itself is identical for test vs real (the IdP has no
// notion of "test me").
func (o *OAuth) AuthorizeURL(providerID, returnPath string, testMode bool) (authorizeURL, stateToken string, err error) {
	s, err := o.stateByID(providerID)
	if err != nil {
		return "", "", err
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
		TestMode:   testMode,
	}
	token, err := o.StateSigner.Sign(payload)
	if err != nil {
		return "", "", err
	}

	// The OIDC nonce must match between the auth URL and the id_token claim.
	authURL := s.OAuthCfg.AuthCodeURL(token, oidc.Nonce(nonce))
	return authURL, token, nil
}

// LinkAuthorizeURL builds an OIDC authorization URL for a link-mode flow.
// It is identical to AuthorizeURL but sets LinkMode=true and
// LinkUserAccountID=userAccountUUID in the state payload so the callback can
// insert a new identity row for the already-authenticated user rather than
// resolving or creating an account.
func (o *OAuth) LinkAuthorizeURL(providerID, userAccountUUID, returnPath string) (authorizeURL, stateToken string, err error) {
	s, err := o.stateByID(providerID)
	if err != nil {
		return "", "", err
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
		Provider:          providerID,
		ReturnPath:        returnPath,
		Nonce:             nonce,
		Expires:           time.Now().Add(stateTTL).Unix(),
		LinkMode:          true,
		LinkUserAccountID: userAccountUUID,
	}
	token, err := o.StateSigner.Sign(payload)
	if err != nil {
		return "", "", err
	}

	authURL := s.OAuthCfg.AuthCodeURL(token, oidc.Nonce(nonce))
	return authURL, token, nil
}

// Exchange verifies the state parameter, trades the authorization code for
// tokens at the provider's token endpoint, validates the resulting id_token,
// and returns a normalized Principal plus the recovered state payload (so
// the caller can redirect to the return path).
func (o *OAuth) Exchange(ctx context.Context, providerID, code, rawState, cookieState string) (Principal, StatePayload, error) {
	if o.ExchangeFn != nil {
		return o.ExchangeFn(ctx, providerID, code, rawState, cookieState)
	}

	var empty Principal
	var emptyPayload StatePayload

	s, err := o.stateByID(providerID)
	if err != nil {
		return empty, emptyPayload, err
	}

	// State must be present in both the query string and the cookie, and the
	// two values must be byte-identical. Cookie tampering or a missing cookie
	// (e.g., browser blocked it, or the callback was hit without /start) is
	// treated as a mismatch. Every state-related failure wraps
	// ErrStateValidation so the handler can distinguish "retry your login"
	// from "the IdP borked the token exchange".
	if rawState == "" || cookieState == "" {
		return empty, emptyPayload, fmt.Errorf("missing state: %w", ErrStateValidation)
	}
	if rawState != cookieState {
		return empty, emptyPayload, fmt.Errorf("state cookie mismatch: %w", ErrStateValidation)
	}

	payload, err := o.StateSigner.Verify(rawState)
	if err != nil {
		return empty, emptyPayload, fmt.Errorf("verify state (%v): %w", err, ErrStateValidation)
	}
	if payload.Provider != providerID {
		return empty, emptyPayload, fmt.Errorf("state provider mismatch: %w", ErrStateValidation)
	}

	tok, err := s.OAuthCfg.Exchange(ctx, code)
	if err != nil {
		return empty, emptyPayload, fmt.Errorf("oauth: token exchange: %w", err)
	}

	rawIDToken, ok := tok.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return empty, emptyPayload, errors.New("oauth: provider did not return an id_token")
	}

	idToken, err := s.Verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return empty, emptyPayload, fmt.Errorf("oauth: verify id_token: %w", err)
	}

	// For multi-tenant Microsoft providers the verifier above was configured
	// with SkipIssuerCheck=true (so the discovery-document placeholder doesn't
	// trip it up); enforce the real tenant-issuer pattern here. Any id_token
	// whose `iss` is not a well-formed Microsoft tenant issuer is rejected.
	// We deliberately do NOT wrap ErrStateValidation here — non-sentinel
	// errors get mapped by the handler to redirectToFrontendError(
	// "authentication_failed"), which is the right UX for a spoofed token.
	if s.Provider.MultiTenantIssuer && !isValidMicrosoftIssuer(idToken.Issuer) {
		return empty, emptyPayload, fmt.Errorf("oauth: microsoft issuer not accepted: %q", idToken.Issuer)
	}

	if idToken.Nonce != payload.Nonce {
		return empty, emptyPayload, errors.New("oauth: nonce mismatch")
	}

	var claims map[string]any
	if err := idToken.Claims(&claims); err != nil {
		return empty, emptyPayload, fmt.Errorf("oauth: parse id_token claims: %w", err)
	}

	principal, err := s.Mapper.Map(claims)
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

// parseBoolEnv accepts "1", "true", "yes" (case-insensitive) as truthy.
// Anything else — including empty — is false. Matches the plan's "1 or true".
func parseBoolEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
