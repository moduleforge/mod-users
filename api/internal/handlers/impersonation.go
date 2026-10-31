package handlers

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

// ImpersonationHandler serves the /v1/impersonations routes: start an
// impersonation of a profile, and renew or end the caller's own impersonation
// session. Every route requires an authenticated session (mount the group
// behind auth.NewRequireAuth). Construct it with NewImpersonationHandler.
type ImpersonationHandler struct {
	impersonations *svc.ImpersonationService
}

// NewImpersonationHandler constructs the /v1/impersonations handler over the
// impersonation service.
func NewImpersonationHandler(impersonations *svc.ImpersonationService) *ImpersonationHandler {
	return &ImpersonationHandler{impersonations: impersonations}
}

// RegisterImpersonationRoutes mounts the /v1/impersonations routes on r:
//
//	POST   /               start an impersonation
//	POST   /current/renew  extend the caller's impersonation session
//	DELETE /current        end the caller's impersonation session
//
// The caller applies the authentication middleware (auth.NewRequireAuth)
// first.
func RegisterImpersonationRoutes(r chi.Router, h *ImpersonationHandler) {
	r.Post("/", h.Start)
	r.Post("/current/renew", h.Renew)
	r.Delete("/current", h.End)
}

// interactiveIdentity returns the caller's Identity, writing the refusal and
// returning false when there is none (500: mounted outside the authentication
// middleware) or the caller has no session (403: client credentials).
func interactiveIdentity(w http.ResponseWriter, r *http.Request) (*localauth.Identity, bool) {
	id, ok := localauth.IdentityFromContext(r.Context())
	if !ok {
		apiresp.WriteError(w, r, errors.New("impersonation routes mounted outside RequireAuth"))
		return nil, false
	}
	if id.SessionID == 0 {
		apiresp.WriteError(w, r, apiresp.ErrForbidden)
		return nil, false
	}
	return id, true
}

// startImpersonationRequest is the body of POST /v1/impersonations.
type startImpersonationRequest struct {
	ProfileUUID string `json:"profile_uuid"`
	Reason      string `json:"reason"`
}

// Start handles POST /v1/impersonations: it starts an impersonation of
// profile_uuid for the stated reason and answers 201 with the token response
// of the new child session (no refresh token). Order: 401, 400 (a malformed
// body, profile_uuid or reason), 403 (nesting, one's own profile, no
// `impersonate` authority), 404 (only for a caller allowed to impersonate any
// profile), then 409 (stale authentication: users.step_up_required, or a
// target that cannot be acted as).
func (h *ImpersonationHandler) Start(w http.ResponseWriter, r *http.Request) {
	id, ok := interactiveIdentity(w, r)
	if !ok {
		return
	}
	var req startImpersonationRequest
	if !server.Decode(w, r, &req) {
		return
	}
	if req.ProfileUUID == "" {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "profile_uuid", Code: "users.profile_uuid_required", Message: "profile_uuid is required",
		}))
		return
	}
	profileUUID, err := uuid.Parse(req.ProfileUUID)
	if err != nil {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "profile_uuid", Code: "users.profile_uuid_invalid", Message: "profile_uuid must be a uuid",
		}))
		return
	}
	reason, err := svc.ValidateImpersonationReason(req.Reason)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	resp, err := h.impersonations.Start(r.Context(), svc.StartImpersonationInput{
		CallerSessionID: id.SessionID, ProfileUUID: profileUUID, Reason: reason,
	})
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusCreated, resp)
}

// Renew handles POST /v1/impersonations/current/renew: it extends the
// caller's impersonation session by AUTH_IMPERSONATION_TTL (never beyond
// AUTH_IMPERSONATION_MAX from its start) and answers 200 with a fresh token
// response (no refresh token). 403 users.impersonation_not_allowed outside an
// impersonation session; 409 users.impersonation_limit_reached when the cap
// leaves no room. The body is ignored.
func (h *ImpersonationHandler) Renew(w http.ResponseWriter, r *http.Request) {
	id, ok := interactiveIdentity(w, r)
	if !ok {
		return
	}
	resp, err := h.impersonations.Renew(r.Context(), id.SessionID)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}

// End handles DELETE /v1/impersonations/current: it revokes the caller's
// impersonation session (revoke_reason impersonation_ended) and answers 204.
// 403 users.impersonation_not_allowed outside an impersonation session.
func (h *ImpersonationHandler) End(w http.ResponseWriter, r *http.Request) {
	id, ok := interactiveIdentity(w, r)
	if !ok {
		return
	}
	if err := h.impersonations.End(r.Context(), id.SessionID); err != nil {
		writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
