package auth

import (
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// Grant types of POST /v1/auth/token.
const (
	grantRefreshToken      = "refresh_token"
	grantClientCredentials = "client_credentials"
)

// tokenRequest is the JSON body for POST /v1/auth/token.
type tokenRequest struct {
	GrantType string `json:"grant_type"`
	// refresh_token grant.
	RefreshToken string `json:"refresh_token"`
	// ProfileUUID is required for refresh_token and optional for
	// client_credentials.
	ProfileUUID string `json:"profile_uuid"`
	// client_credentials grant.
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	// App optionally names a client-credentials token's audience app.
	App string `json:"app"`
}

// Token handles POST /v1/auth/token.
//
// grant_type=refresh_token (refresh_token, profile_uuid): rotates the tab's
// refresh token and issues an access token for the named profile after
// re-running its availability. A reused token revokes the whole session (401
// users.refresh_reuse); an unknown or expired token, or a dead session, is
// 401 users.session_revoked; an unavailable profile is 401
// users.profile_unavailable (the token stays usable).
//
// grant_type=client_credentials (client_id, client_secret, optional
// profile_uuid and app): issues a cid token for an API key, with no session
// and no refresh token. Bad credentials are 401 users.invalid_credentials.
func (h *Handler) Token(w http.ResponseWriter, r *http.Request) {
	var req tokenRequest
	if !server.Decode(w, r, &req) {
		return
	}
	switch req.GrantType {
	case grantRefreshToken:
		h.refreshGrant(w, r, req)
	case grantClientCredentials:
		h.clientCredentialsGrant(w, r, req)
	case "":
		apiresp.WriteError(w, r, apiresp.InvalidInput(required([2]string{"grant_type", ""})...))
	default:
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "grant_type", Code: "users.grant_type_unsupported", Message: "unsupported grant_type",
		}))
	}
}

func (h *Handler) refreshGrant(w http.ResponseWriter, r *http.Request, req tokenRequest) {
	if details := required(
		[2]string{"refresh_token", req.RefreshToken},
		[2]string{"profile_uuid", req.ProfileUUID},
	); len(details) > 0 {
		apiresp.WriteError(w, r, apiresp.InvalidInput(details...))
		return
	}
	profileUUID, err := parseOptionalUUID("profile_uuid", req.ProfileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := h.sessions.Refresh(r.Context(), req.RefreshToken, profileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}

func (h *Handler) clientCredentialsGrant(w http.ResponseWriter, r *http.Request, req tokenRequest) {
	if details := required(
		[2]string{"client_id", req.ClientID},
		[2]string{"client_secret", req.ClientSecret},
	); len(details) > 0 {
		apiresp.WriteError(w, r, apiresp.InvalidInput(details...))
		return
	}
	profileUUID, err := parseOptionalUUID("profile_uuid", req.ProfileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := h.sessions.ClientCredentials(r.Context(), svc.ClientCredentialsInput{
		ClientID: req.ClientID, ClientSecret: req.ClientSecret, ProfileUUID: profileUUID, AppSlug: req.App,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}

// handoffRequest is the body for POST /v1/auth/handoff.
type handoffRequest struct {
	Code string `json:"code"`
}

// Handoff handles POST /v1/auth/handoff: it exchanges the one-time
// login_handoff code an OIDC callback redirect carried (#handoff=<code>) for
// the token response, once. A spent, expired or unknown code is 401
// users.invalid_credentials.
func (h *Handler) Handoff(w http.ResponseWriter, r *http.Request) {
	var req handoffRequest
	if !server.Decode(w, r, &req) {
		return
	}
	if details := required([2]string{"code", req.Code}); len(details) > 0 {
		apiresp.WriteError(w, r, apiresp.InvalidInput(details...))
		return
	}
	resp, err := h.sessions.ExchangeHandoff(r.Context(), req.Code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}
