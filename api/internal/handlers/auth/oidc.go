package auth

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/config"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// normalizeProviderID lowercases the provider URL param so the lookup matches
// the registry keys (which are lowercased at load time). Without this,
// /v1/auth/oidc/Google/start would 404.
func normalizeProviderID(r *http.Request) string {
	return strings.ToLower(chi.URLParam(r, "provider"))
}

// stateCookieName is the name of the cookie that carries the signed state
// token between /start and /callback.
const stateCookieName = "oidc_state"

// stateCookiePath scopes the cookie to the OIDC callback route tree so it
// isn't broadcast on unrelated requests.
const stateCookiePath = "/v1/auth/oidc/"

// stateCookieMaxAge mirrors the TTL baked into the state token itself.
const stateCookieMaxAge = 300

// OIDCHandler serves the provider discovery endpoint and the authorization
// code start/callback round trip. It holds its own copies of the resolver
// and the OAuth orchestrator so the main router wiring stays simple.
type OIDCHandler struct {
	queries  *db.Queries
	oauth    *localauth.OAuth
	resolver *localauth.UserResolver
	cfg      *config.Config
}

// NewOIDCHandler wires up the handler with everything it needs. All fields
// must be non-nil (except oauth, which may be a shell with zero providers).
func NewOIDCHandler(queries *db.Queries, oauth *localauth.OAuth, resolver *localauth.UserResolver, cfg *config.Config) *OIDCHandler {
	return &OIDCHandler{
		queries:  queries,
		oauth:    oauth,
		resolver: resolver,
		cfg:      cfg,
	}
}

// providerEntry is the browser-safe view of a Provider — only public fields.
type providerEntry struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// ListProviders handles GET /v1/auth/providers. It is unauthenticated so the
// login page can render provider buttons before the user has a session.
// Never include client_secret, issuer_url, or scopes in the response.
func (h *OIDCHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	out := make([]providerEntry, 0, len(h.oauth.Registry))
	for _, id := range h.oauth.Registry.IDs() {
		p := h.oauth.Registry[id]
		out = append(out, providerEntry{ID: p.ID, DisplayName: p.DisplayName})
	}
	server.JSON(w, http.StatusOK, out)
}

// Start handles GET /v1/auth/oidc/{provider}/start. It validates the provider
// id and return path, writes the signed state cookie, and 302s the browser
// to the provider's authorization URL.
func (h *OIDCHandler) Start(w http.ResponseWriter, r *http.Request) {
	providerID := normalizeProviderID(r)
	if _, ok := h.oauth.Registry[providerID]; !ok {
		server.Error(w, http.StatusNotFound, "not_found", "unknown provider")
		return
	}

	returnPath := r.URL.Query().Get("return")
	authURL, state, err := h.oauth.AuthorizeURL(providerID, returnPath)
	if err != nil {
		if errors.Is(err, localauth.ErrUnknownProvider) {
			server.Error(w, http.StatusNotFound, "not_found", "unknown provider")
			return
		}
		slog.WarnContext(r.Context(), "oidc start: bad request", "error", err, "provider", providerID)
		server.Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	http.SetCookie(w, h.newStateCookie(state, stateCookieMaxAge, r))
	http.Redirect(w, r, authURL, http.StatusFound)
}

// Callback handles GET /v1/auth/oidc/{provider}/callback. It trades the
// authorization code for an id_token, resolves the user, mints a local JWT,
// writes an audit row, and 302s to the GUI return page with the JWT in the
// URL fragment so it never hits any server log.
func (h *OIDCHandler) Callback(w http.ResponseWriter, r *http.Request) {
	providerID := normalizeProviderID(r)
	if _, ok := h.oauth.Registry[providerID]; !ok {
		server.Error(w, http.StatusNotFound, "not_found", "unknown provider")
		return
	}

	// Clear the state cookie regardless of outcome — one-shot usage.
	h.clearStateCookie(w, r)

	q := r.URL.Query()
	if providerErr := q.Get("error"); providerErr != "" {
		h.redirectToFrontendError(w, r, providerErr)
		return
	}

	code := q.Get("code")
	rawState := q.Get("state")
	if code == "" || rawState == "" {
		server.Error(w, http.StatusBadRequest, "bad_request", "missing code or state")
		return
	}

	cookie, err := r.Cookie(stateCookieName)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "missing state cookie")
		return
	}

	principal, statePayload, err := h.oauth.Exchange(r.Context(), providerID, code, rawState, cookie.Value)
	if err != nil {
		slog.WarnContext(r.Context(), "oidc callback: exchange failed", "error", err, "provider", providerID)
		// State/cookie problems are client-fixable → 400. Everything downstream
		// (token endpoint, id_token verify) reports a generic error via redirect
		// so the GUI can surface it and the operator can inspect logs.
		if errors.Is(err, localauth.ErrStateValidation) {
			server.Error(w, http.StatusBadRequest, "bad_request", "invalid or expired state")
			return
		}
		h.redirectToFrontendError(w, r, "authentication_failed")
		return
	}

	if principal.Email == "" {
		// Without an email we can't link to or create a user record. This is
		// almost always a scope misconfiguration on the IdP side.
		slog.WarnContext(r.Context(), "oidc callback: principal missing email", "provider", providerID)
		h.redirectToFrontendError(w, r, "missing_email")
		return
	}

	uc, err := h.resolver.Resolve(r.Context(), principal)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc callback: resolve user", "error", err, "provider", providerID)
		h.redirectToFrontendError(w, r, "authentication_failed")
		return
	}

	user, err := h.queries.GetUserByID(r.Context(), uc.UserID)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc callback: reload user", "error", err)
		h.redirectToFrontendError(w, r, "authentication_failed")
		return
	}

	token, err := localauth.IssueLocalJWT(user, uc.IsAdmin, h.cfg.LocalAuth.JWTSecret, h.cfg.LocalAuth.LocalIssuer)
	if err != nil {
		slog.ErrorContext(r.Context(), "oidc callback: issue jwt", "error", err)
		h.redirectToFrontendError(w, r, "authentication_failed")
		return
	}

	// Audit the login — best-effort; a failed audit write must not break login.
	h.writeLoginAudit(r.Context(), uc, providerID, principal)

	slog.InfoContext(r.Context(), "oidc login succeeded",
		"provider", providerID,
		"user_uuid", user.Uuid.String(),
	)

	redirectURL := h.buildSuccessRedirect(token, statePayload.ReturnPath)
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

// buildSuccessRedirect composes the GUI return URL with the JWT and return
// path in the URL fragment so they never appear in server logs.
func (h *OIDCHandler) buildSuccessRedirect(token, returnPath string) string {
	base := h.oauth.FrontendReturnURL
	frag := url.Values{}
	frag.Set("token", token)
	frag.Set("return", returnPath)
	// Using url.Values encoding with '#' instead of '?' because the spec
	// wants these in the fragment.
	return base + "#" + frag.Encode()
}

// redirectToFrontendError 302s to the frontend return URL with a query
// parameter describing a generic error code. We intentionally do not leak
// internal errors; operators can correlate via slog.
func (h *OIDCHandler) redirectToFrontendError(w http.ResponseWriter, r *http.Request, code string) {
	u, err := url.Parse(h.oauth.FrontendReturnURL)
	if err != nil {
		// Fall back to a plain error response if the configured URL is bad.
		server.Error(w, http.StatusInternalServerError, "internal_error", "authentication failed")
		return
	}
	q := u.Query()
	q.Set("error", code)
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
		Path:     stateCookiePath,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   requestIsHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	}
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

// loginAuditMeta is the structured after-payload for the login audit row.
// Email deliberately omitted — auditors reach the user record via the joined
// users row referenced by resource_id / target_entity_id. Avoiding a denorm'd
// copy here keeps the audit log consistent if the user later changes email.
type loginAuditMeta struct {
	Provider string `json:"provider"`
	Linked   bool   `json:"linked"`
}

// writeLoginAudit records the login event directly via queries (not the
// audit.Writer abstraction, which requires UserContext on ctx and is scoped
// to admin-mutation handlers). Best-effort — failures log but do not abort.
func (h *OIDCHandler) writeLoginAudit(ctx context.Context, uc *localauth.UserContext, providerID string, p localauth.Principal) {
	meta := loginAuditMeta{
		Provider: providerID,
		Linked:   p.Subject != "" && p.Issuer != "",
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		slog.ErrorContext(ctx, "oidc audit: marshal meta", "error", err)
		return
	}

	err = h.queries.WriteAudit(ctx, db.WriteAuditParams{
		ActorUserID:    uc.UserID,
		AssumedUserID:  pgtype.Int8{},
		TargetEntityID: pgtype.Int8{Int64: uc.EntityID, Valid: true},
		Op:             "login",
		Resource:       "users",
		Before:         nil,
		After:          metaJSON,
	})
	if err != nil {
		slog.ErrorContext(ctx, "oidc audit: write", "error", err)
	}
}
