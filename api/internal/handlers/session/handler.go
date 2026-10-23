// Package session provides the HTTP handlers of the /v1/session route group:
// the caller's own session, profile switch, fork (a refresh lineage for a new
// tab), step-up and logout. Every route requires an authenticated session
// (mount the group behind auth.NewRequireAuth).
package session

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// SessionHandler serves the /v1/session routes. Construct it with
// NewSessionHandler.
type SessionHandler struct {
	sessions *svc.SessionService
}

// NewSessionHandler constructs the /v1/session handler over the session
// service.
func NewSessionHandler(sessions *svc.SessionService) *SessionHandler {
	return &SessionHandler{sessions: sessions}
}

// RegisterSessionRoutes mounts the /v1/session routes onto r. The caller
// applies the authentication middleware (auth.NewRequireAuth) first. The
// OIDC step-up start (POST /step-up/oidc/{provider}/start) is provided by the
// OIDC handler and mounted by the composition root.
func RegisterSessionRoutes(r chi.Router, h *SessionHandler) {
	r.Get("/", h.Current)
	r.Post("/switch", h.Switch)
	r.Post("/fork", h.Fork)
	r.Post("/step-up/password", h.StepUpPassword)
	r.Post("/step-up/email-code/request", h.StepUpEmailCodeRequest)
	r.Post("/step-up/email-code/verify", h.StepUpEmailCodeVerify)
	r.Post("/logout", h.Logout)
}

// writeError renders err: a service.AuthError with its own code and status,
// anything else through apiresp.WriteError (sentinels, field errors and the
// users.step_up_required action-required envelope).
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *svc.AuthError
	if errors.As(err, &ae) {
		apiresp.WriteJSON(w, ae.Status, ae.Envelope())
		return
	}
	apiresp.WriteError(w, r, err)
}

// identity returns the caller's Identity, writing the refusal and returning
// false when there is none (500: mounted outside the authentication
// middleware) or it carries no session (403).
func identity(w http.ResponseWriter, r *http.Request) (*localauth.Identity, bool) {
	id, ok := localauth.IdentityFromContext(r.Context())
	if !ok {
		apiresp.WriteError(w, r, errors.New("session routes mounted outside RequireAuth"))
		return nil, false
	}
	if id.SessionID == 0 {
		// A client-credentials caller: the session routes act on an
		// interactive session only.
		apiresp.WriteError(w, r, apiresp.ErrForbidden)
		return nil, false
	}
	return id, true
}

// decodeOptional decodes an optional JSON body: an empty body leaves v as
// is. It reports false after writing the 400 for a malformed body.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) bool {
	if r.Body == nil || r.ContentLength == 0 {
		return true
	}
	return server.Decode(w, r, v)
}

// Current handles GET /v1/session: the caller's session, the profile its
// token acts as, and the principal's profiles with their availability.
func (h *SessionHandler) Current(w http.ResponseWriter, r *http.Request) {
	id, ok := identity(w, r)
	if !ok {
		return
	}
	info, err := h.sessions.Current(r.Context(), id.SessionID, id.ProfileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, info)
}

// switchRequest is the body for POST /v1/session/switch.
type switchRequest struct {
	ProfileUUID string `json:"profile_uuid"`
}

// Switch handles POST /v1/session/switch: a new access token acting as
// profile_uuid for the same session, with no session write and no refresh
// token. An unavailable profile is 401 users.profile_unavailable (403
// users.app_login_not_granted for the invite-only app gate).
func (h *SessionHandler) Switch(w http.ResponseWriter, r *http.Request) {
	id, ok := identity(w, r)
	if !ok {
		return
	}
	var req switchRequest
	if !server.Decode(w, r, &req) {
		return
	}
	profileUUID, err := requiredUUID("profile_uuid", req.ProfileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	resp, err := h.sessions.Switch(r.Context(), id.SessionID, profileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}

// Fork handles POST /v1/session/fork: a new refresh lineage for the caller's
// session (a new tab), returned with an access token acting as the caller's
// current profile. The body is ignored.
func (h *SessionHandler) Fork(w http.ResponseWriter, r *http.Request) {
	id, ok := identity(w, r)
	if !ok {
		return
	}
	resp, err := h.sessions.Fork(r.Context(), id.SessionID, id.ProfileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}

// stepUpPasswordRequest is the body for POST /v1/session/step-up/password.
type stepUpPasswordRequest struct {
	Password string `json:"password"`
}

// StepUpPassword handles POST /v1/session/step-up/password: re-verifies a
// password of the caller, raises the session's acr as the factors allow, sets
// auth_time to now and returns a fresh access token (no refresh token). Any
// refusal is the 409 users.step_up_required action.
func (h *SessionHandler) StepUpPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := identity(w, r)
	if !ok {
		return
	}
	var req stepUpPasswordRequest
	if !server.Decode(w, r, &req) {
		return
	}
	if req.Password == "" {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "password", Code: "users.password_required", Message: "password is required",
		}))
		return
	}
	resp, err := h.sessions.StepUpPassword(r.Context(), id.SessionID, id.ProfileUUID, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}

// stepUpCodeRequest is the body for POST /v1/session/step-up/email-code/request.
type stepUpCodeRequest struct {
	// Email optionally selects one of the caller's email identifiers; the
	// primary email otherwise.
	Email string `json:"email"`
}

// StepUpEmailCodeRequest handles POST /v1/session/step-up/email-code/request:
// emails a step-up code bound to the session (204; a request inside the
// cooldown sends nothing). No email credential to send to is the 409
// users.step_up_required action.
func (h *SessionHandler) StepUpEmailCodeRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := identity(w, r)
	if !ok {
		return
	}
	var req stepUpCodeRequest
	if !decodeOptional(w, r, &req) {
		return
	}
	if err := h.sessions.StepUpEmailCodeRequest(r.Context(), id.SessionID, req.Email); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// stepUpCodeVerifyRequest is the body for POST /v1/session/step-up/email-code/verify.
type stepUpCodeVerifyRequest struct {
	Code  string `json:"code"`
	Email string `json:"email"`
}

// StepUpEmailCodeVerify handles POST /v1/session/step-up/email-code/verify:
// verifies the step-up code, adds the email credential as a factor and
// returns a fresh access token, as StepUpPassword does.
func (h *SessionHandler) StepUpEmailCodeVerify(w http.ResponseWriter, r *http.Request) {
	id, ok := identity(w, r)
	if !ok {
		return
	}
	var req stepUpCodeVerifyRequest
	if !server.Decode(w, r, &req) {
		return
	}
	if req.Code == "" {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "code", Code: "users.code_required", Message: "code is required",
		}))
		return
	}
	resp, err := h.sessions.StepUpEmailCodeVerify(r.Context(), id.SessionID, id.ProfileUUID, req.Email, req.Code)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}

// Logout handles POST /v1/session/logout: revokes the caller's session
// (revoke_reason logout) and its refresh lineages. 204. The body is ignored.
func (h *SessionHandler) Logout(w http.ResponseWriter, r *http.Request) {
	id, ok := identity(w, r)
	if !ok {
		return
	}
	if err := h.sessions.Logout(r.Context(), id.SessionID); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// requiredUUID parses a required uuid field.
func requiredUUID(field, value string) (uuid.UUID, error) {
	if value == "" {
		return uuid.Nil, apiresp.InvalidInput(apiresp.FieldError{
			Field: field, Code: "users." + field + "_required", Message: field + " is required",
		})
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, apiresp.InvalidInput(apiresp.FieldError{
			Field: field, Code: "users." + field + "_invalid", Message: field + " must be a uuid",
		})
	}
	return id, nil
}
