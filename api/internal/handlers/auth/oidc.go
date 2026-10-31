package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/observer"
	coredb "github.com/moduleforge/core-model/db"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// normalizeProviderID lowercases the provider URL param so the lookup matches
// the registry keys (which are lowercased at load time). Without this,
// /v1/auth/oidc/Google/start would 404.
func normalizeProviderID(r *http.Request) string {
	return strings.ToLower(chi.URLParam(r, "provider"))
}

// stateCookieName is the name of the cookie that carries the signed state
// token between /start and /callback. It is the only cookie the module sets.
const stateCookieName = "oidc_state"

// stateCookieMaxAge mirrors the TTL baked into the state token itself.
const stateCookieMaxAge = 300

// handoffChallengeParam is the /start query parameter carrying the browser's
// handoff challenge: the unpadded base64url SHA-256 of a client-generated
// random verifier, which the browser presents again at POST /v1/auth/handoff.
const handoffChallengeParam = "handoff_challenge"

// OIDCHandler serves the OIDC authorization-code round trip: the public
// start and callback of GET /v1/auth/oidc/{provider}/..., and the starts of
// the two flows that need an authenticated session (link a credential, step
// up). The callback resolves the verified external identity against
// credentials (never by email), creates a session and a one-time
// login_handoff challenge, and redirects with `#handoff=<code>`; it puts no
// token in the redirect.
type OIDCHandler struct {
	pool       *pgxpool.Pool
	q          usersdb.Querier
	coreQ      coredb.Querier
	oauth      *localauth.OAuth
	resolver   *localauth.OIDCResolver
	users      *svc.UserService
	creds      *svc.CredentialService
	challenges *svc.ChallengeService
	sessions   *svc.SessionService
	obs        *observer.ObserverGroup // nil-safe; may be nil in tests
	cfg        *config.Config
}

// OIDCRoutes (declared in routes.go) is satisfied by *OIDCHandler.
var _ OIDCRoutes = (*OIDCHandler)(nil)

// NewOIDCHandler wires up the handler. pool, q and coreQ are the module's
// connection pool and its queriers over it; resolver decides what a verified
// identity maps to; the services create the sessions, users and credentials a
// decision calls for. obs may be nil.
func NewOIDCHandler(
	pool *pgxpool.Pool,
	q usersdb.Querier,
	coreQ coredb.Querier,
	oauth *localauth.OAuth,
	resolver *localauth.OIDCResolver,
	users *svc.UserService,
	creds *svc.CredentialService,
	challenges *svc.ChallengeService,
	sessions *svc.SessionService,
	obs *observer.ObserverGroup,
	cfg *config.Config,
) *OIDCHandler {
	return &OIDCHandler{
		pool: pool, q: q, coreQ: coreQ, oauth: oauth, resolver: resolver,
		users: users, creds: creds, challenges: challenges, sessions: sessions,
		obs: obs, cfg: cfg,
	}
}

// inTx runs fn in a transaction on the pool and commits when fn succeeds.
func (h *OIDCHandler) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	if h.pool == nil {
		return errors.New("oidc: no database pool")
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("oidc: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("oidc: commit: %w", err)
	}
	return nil
}

// Start handles GET /v1/auth/oidc/{provider}/start. It validates the provider
// id and return path, writes the signed state cookie, and 302s the browser
// to the provider's authorization URL.
func (h *OIDCHandler) Start(w http.ResponseWriter, r *http.Request) {
	providerID := normalizeProviderID(r)

	returnPath := r.URL.Query().Get("return")
	// ?mode=test is the "Test configuration" affordance from the Edit
	// modal — the browser runs the full authorize/callback loop but
	// the callback skips the resolver and session creation so the admin's
	// session is untouched. Any other mode value is ignored (fall through to
	// normal login), matching the "be generous in what you accept"
	// spirit of OAuth start.
	testMode := r.URL.Query().Get("mode") == "test"
	authURL, state, err := h.oauth.AuthorizeURL(providerID, returnPath, testMode, r.URL.Query().Get(handoffChallengeParam))
	if err != nil {
		h.writeStartError(w, r, providerID, err)
		return
	}

	http.SetCookie(w, h.newStateCookie(state, stateCookieMaxAge, r))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// writeStartError maps an authorize-URL failure to its response. Unknown IDs
// and init-failed providers both surface as 404 — an operator-observable
// failure (slog.Warn at boot) that the user has no agency to fix mid-request.
func (h *OIDCHandler) writeStartError(w http.ResponseWriter, r *http.Request, providerID string, err error) {
	if errors.Is(err, localauth.ErrUnknownProvider) || errors.Is(err, localauth.ErrProviderNotAvailable) {
		apiresp.WriteError(w, r, apiresp.ErrNotFound)
		return
	}
	if errors.Is(err, localauth.ErrInvalidReturnPath) {
		slog.WarnContext(r.Context(), "oidc start: bad request", "error", err, "provider", providerID)
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{Field: "return", Code: "users.oidc_start_invalid", Message: "the start request is invalid"}))
		return
	}
	if errors.Is(err, localauth.ErrInvalidHandoffChallenge) {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: handoffChallengeParam, Code: "users.handoff_challenge_invalid",
			Message: "handoff_challenge must be the base64url SHA-256 of the client verifier",
		}))
		return
	}
	// A sign or random-source failure is ours, not the client's.
	apiresp.WriteError(w, r, fmt.Errorf("oidc start %q: build authorize url: %w", providerID, err))
}

// Callback handles GET /v1/auth/oidc/{provider}/callback. It trades the
// authorization code for a verified external identity and then, by the flow
// the state was minted for, signs in (creating the account the provider's
// jit_mode allows), links the identity to the authenticated session's
// principal, or steps the session up. Every outcome is a 302 to the GUI return
// page whose fragment carries `handoff=<code>` (sign-in), `linked=1`,
// `stepped_up=1` or `error=<code>`; no token or refresh token is ever placed in
// a redirect.
func (h *OIDCHandler) Callback(w http.ResponseWriter, r *http.Request) {
	providerID := normalizeProviderID(r)

	// Clear the state cookie regardless of outcome — one-shot usage.
	h.clearStateCookie(w, r)

	// Short-circuit on an unknown or init-failed provider so the browser
	// sees a 404 rather than a confusing "missing state cookie" 400 that it
	// cannot remediate. Enabled providers fall through to the full flow
	// below; init-failed providers are indistinguishable from unknown at
	// the wire.
	if !h.oauth.ProviderAvailable(providerID) {
		apiresp.WriteError(w, r, apiresp.ErrNotFound)
		return
	}

	q := r.URL.Query()
	if providerErr := q.Get("error"); providerErr != "" {
		// The provider's error text is untrusted; only the fact is passed on.
		slog.WarnContext(r.Context(), "oidc callback: provider returned an error", "provider", providerID)
		h.redirectError(w, r, localauth.RedirectErrAuthFailed)
		return
	}

	code := q.Get("code")
	rawState := q.Get("state")
	if code == "" || rawState == "" {
		apiresp.WriteError(w, r, apiresp.InvalidInput(
			apiresp.FieldError{Field: "code", Code: "users.oidc_code_or_state_required", Message: "code and state are required"},
		))
		return
	}

	cookie, err := r.Cookie(stateCookieName)
	if err != nil {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{Field: "state_cookie", Code: "users.oidc_state_cookie_required", Message: "the state cookie is required"}))
		return
	}

	identity, statePayload, err := h.oauth.Exchange(r.Context(), providerID, code, rawState, cookie.Value)
	if err != nil {
		slog.WarnContext(r.Context(), "oidc callback: exchange failed", "error", err, "provider", providerID)
		// Unknown ID or init-failed provider → 404, same policy as /start.
		if errors.Is(err, localauth.ErrUnknownProvider) || errors.Is(err, localauth.ErrProviderNotAvailable) {
			apiresp.WriteError(w, r, apiresp.ErrNotFound)
			return
		}
		// State/cookie problems are client-fixable → 400. Everything downstream
		// (token endpoint, id_token verify) reports a generic error via redirect
		// so the GUI can surface it and the operator can inspect logs.
		if errors.Is(err, localauth.ErrStateValidation) {
			apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{Field: "state", Code: "users.oidc_state_invalid", Message: "the state is invalid or expired"}))
			return
		}
		// Test mode: surface the exchange/verify error directly on the
		// /oidc-config banner instead of the generic "authentication_failed"
		// redirect, since diagnosing the IdP config is the entire point.
		if statePayload.TestMode {
			h.redirectToTestResult(w, r, providerID, "", "", "", err.Error())
			return
		}
		h.redirectError(w, r, localauth.RedirectErrAuthFailed)
		return
	}

	// Test path: id_token verified cleanly. Skip the resolver and session
	// creation (no session mutation) and bounce back to /oidc-config with the
	// verified identity details so the admin can confirm what the IdP
	// asserted.
	if statePayload.TestMode {
		slog.InfoContext(r.Context(), "oidc test succeeded",
			"provider", providerID,
			"email", identity.Email,
			"subject", identity.Subject,
		)
		h.redirectToTestResult(w, r, providerID, identity.Email, identity.Subject, identity.Issuer, "")
		return
	}

	switch statePayload.Flow {
	case localauth.FlowLink:
		h.callbackLink(w, r, providerID, identity, statePayload)
	case localauth.FlowStepUp:
		h.callbackStepUp(w, r, providerID, identity, statePayload)
	default:
		h.callbackLogin(w, r, providerID, identity, statePayload)
	}
}

// redirectFragment 302s to the frontend return URL with frag as the URL
// fragment, so nothing in it reaches a server log. The no-store header keeps a
// one-time code out of shared caches.
func (h *OIDCHandler) redirectFragment(w http.ResponseWriter, r *http.Request, frag url.Values) {
	u, err := url.Parse(h.oauth.FrontendReturnURL)
	if err != nil {
		// Fall back to a plain error response if the configured URL is bad.
		apiresp.WriteError(w, r, fmt.Errorf("oidc redirect: parse frontend return URL: %w", err))
		return
	}
	u.Fragment, u.RawFragment = "", ""
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String()+"#"+frag.Encode(), http.StatusFound)
}

// redirectError 302s to the frontend with `#error=<code>`. We intentionally do
// not leak internal errors; operators can correlate via slog.
func (h *OIDCHandler) redirectError(w http.ResponseWriter, r *http.Request, code string) {
	h.redirectFragment(w, r, url.Values{"error": {code}})
}

// redirectOutcome 302s to the frontend with a success marker (`handoff=<code>`,
// `linked=1` or `stepped_up=1`) and the validated return path.
func (h *OIDCHandler) redirectOutcome(w http.ResponseWriter, r *http.Request, key, value, returnPath string) {
	frag := url.Values{key: {value}}
	if returnPath != "" {
		frag.Set("return", returnPath)
	}
	h.redirectFragment(w, r, frag)
}

// redirectToTestResult 302s back to /oidc-config (same origin as the
// configured FrontendReturnURL) with the test outcome as query params.
// Unlike the real-login path we send result fields in the QUERY string,
// not the fragment — there's no secret to hide and the GUI reads them
// via useSearchParams. testErr is the human-readable error when the
// test failed; pass "" on success.
func (h *OIDCHandler) redirectToTestResult(
	w http.ResponseWriter, r *http.Request,
	providerID, email, subject, issuer, testErr string,
) {
	u, err := url.Parse(h.oauth.FrontendReturnURL)
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("oidc redirect to test result: parse frontend return URL: %w", err))
		return
	}
	// FrontendReturnURL points at /auth/oidc/return; the test banner
	// lives on /oidc-config (same origin) so swap the path. When
	// USERS_COOKIE_PATH names a mount prefix (stripped by a proxy), keep it so
	// the browser lands on <prefix>/oidc-config. Only the validated configured
	// value is used; X-Forwarded-Prefix is never trusted.
	u.Path = h.testResultPath()
	u.RawPath = ""
	u.Fragment = ""
	q := url.Values{}
	if testErr != "" {
		q.Set("test_result", "fail")
		q.Set("test_error", testErr)
	} else {
		q.Set("test_result", "ok")
		q.Set("test_email", email)
		q.Set("test_sub", subject)
		q.Set("test_issuer", issuer)
	}
	q.Set("test_provider", providerID)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

// newStateCookie builds the oidc_state cookie with the security attributes
// described in the spec. "Secure" is set iff the inbound request came in
// over HTTPS or through a proxy that flagged it (X-Forwarded-Proto=https).
func (h *OIDCHandler) newStateCookie(value string, maxAge int, r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     stateCookieName,
		Value:    value,
		Path:     localauth.OIDCStateCookiePath(r, h.cookieBasePath()),
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	}
}

// cookieBasePath returns the configured USERS_COOKIE_PATH, or "" when unset
// (or when the handler carries no config), selecting request-derived Path.
func (h *OIDCHandler) cookieBasePath() string {
	if h == nil || h.cfg == nil {
		return ""
	}
	return h.cfg.Auth.CookiePath
}

// testResultPath returns the /oidc-config path, prefixed with the configured
// (and validated) base path when one is set.
func (h *OIDCHandler) testResultPath() string {
	base := h.cookieBasePath()
	if base != "" && config.ValidateCookieBasePath(base) == nil {
		return base + "/oidc-config"
	}
	return "/oidc-config"
}

// clearStateCookie tombstones the state cookie by setting MaxAge<0.
func (h *OIDCHandler) clearStateCookie(w http.ResponseWriter, r *http.Request) {
	c := h.newStateCookie("", -1, r)
	http.SetCookie(w, c)
}

// requestIsHTTPS decides whether to set the Secure cookie flag based on the
// inbound request. Handles the common reverse-proxy case via X-Forwarded-Proto.
func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if xf := r.Header.Get("X-Forwarded-Proto"); xf != "" {
		return xf == "https"
	}
	return false
}

// writeAuthorizeURL answers a session-flow start: the state cookie plus the
// authorize URL as JSON.
func (h *OIDCHandler) writeAuthorizeURL(w http.ResponseWriter, r *http.Request, authURL, state string) {
	http.SetCookie(w, h.newStateCookie(state, stateCookieMaxAge, r))
	server.JSON(w, http.StatusOK, authorizeURLResponse{AuthorizeURL: authURL})
}

// authorizeURLResponse is the body of the link and step-up starts.
type authorizeURLResponse struct {
	AuthorizeURL string `json:"authorize_url"`
}
