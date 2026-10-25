// Package me provides the HTTP handlers of the /v1/me route group: the
// caller's own user record, profiles, identifiers, credentials and sessions,
// and the guest upgrade. Every route requires an authenticated interactive
// session (mount the group behind auth.NewRequireAuth).
//
// The handlers are thin: they parse and validate the request, call the
// self-service methods of service.UserService and render the result. Authority
// ("session principal = target") and the auth_time freshness gate of decision
// U8 live in those methods, not here, so no route can skip them.
//
// The /profiles/{uuid}/accept, /decline and /leave routes are the holder's own
// profile actions; they delegate to service.ProfileService and authorize on
// "session principal = profile holder". POST /credentials/oidc/{provider}/start
// is the OIDC handler's; the composition root mounts it next to these.
package me

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// MeHandler serves the /v1/me routes. Construct it with NewMeHandler.
type MeHandler struct {
	users    *svc.UserService
	profiles *svc.ProfileService
}

// NewMeHandler constructs the /v1/me handler over the user service, which must
// have been wired with UserService.WithSelfService, and the profile service
// that serves the holder's profile actions.
func NewMeHandler(users *svc.UserService, profiles *svc.ProfileService) *MeHandler {
	return &MeHandler{users: users, profiles: profiles}
}

// RegisterMeRoutes mounts the /v1/me routes onto r. The caller applies the
// authentication middleware (auth.NewRequireAuth) first.
func RegisterMeRoutes(r chi.Router, h *MeHandler) {
	r.Get("/", h.Get)
	r.Put("/", h.Put)
	r.Get("/profiles", h.Profiles)
	r.Post("/profiles/{uuid}/accept", h.AcceptProfile)
	r.Post("/profiles/{uuid}/decline", h.DeclineProfile)
	r.Post("/profiles/{uuid}/leave", h.LeaveProfile)

	r.Get("/identifiers", h.ListIdentifiers)
	r.Post("/identifiers", h.AddIdentifier)
	r.Post("/identifiers/{uuid}/verify", h.VerifyIdentifier)
	r.Post("/identifiers/{uuid}/verification", h.RequestVerification)
	r.Put("/identifiers/{uuid}/primary", h.SetPrimaryIdentifier)
	r.Delete("/identifiers/{uuid}", h.DeleteIdentifier)

	r.Get("/credentials", h.ListCredentials)
	r.Post("/credentials/password", h.AddPassword)
	r.Post("/credentials/ssh-keys", h.AddSSHKey)
	r.Patch("/credentials/{uuid}", h.PatchCredential)
	r.Delete("/credentials/{uuid}", h.DeleteCredential)

	r.Get("/sessions", h.ListSessions)
	r.Delete("/sessions/{uuid}", h.DeleteSession)

	r.Post("/upgrade", h.Upgrade)
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

// requireIdentity writes the refusal and returns false when the request has no
// authenticated identity (500: the group was mounted outside RequireAuth) or
// the caller is not a user in an interactive session (403: a client-credentials
// service account). The self-service methods repeat the session check against
// the AuthContext; this keeps a misconfigured mount from reaching them.
func requireIdentity(w http.ResponseWriter, r *http.Request) bool {
	id, ok := localauth.IdentityFromContext(r.Context())
	if !ok {
		apiresp.WriteError(w, r, errors.New("me routes mounted outside RequireAuth"))
		return false
	}
	if id.SessionID == 0 || id.PrincipalKind != localauth.PrincipalKindUser {
		apiresp.WriteError(w, r, apiresp.ErrForbidden)
		return false
	}
	return true
}

// pathUUID parses the {uuid} URL parameter.
func pathUUID(r *http.Request) (uuid.UUID, error) {
	return apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
}

// optionalRef is a JSON field that is absent, null or a uuid string. The
// decoder records which; Validate on the request parses it.
type optionalRef struct {
	set   bool
	value *string
}

// UnmarshalJSON implements json.Unmarshaler. It is called for a present field,
// including an explicit null.
func (o *optionalRef) UnmarshalJSON(b []byte) error {
	o.set = true
	if string(b) == "null" {
		o.value = nil
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	o.value = &s
	return nil
}

// parse converts the field to the service's OptionalUUID, reporting a field
// error for a value that is not a uuid.
func (o optionalRef) parse(field string) (svc.OptionalUUID, []apiresp.FieldError) {
	if !o.set {
		return svc.OptionalUUID{}, nil
	}
	if o.value == nil {
		return svc.OptionalUUID{Set: true}, nil
	}
	id, err := uuid.Parse(*o.value)
	if err != nil || id == uuid.Nil {
		return svc.OptionalUUID{}, []apiresp.FieldError{{Field: field, Code: "invalid_uuid", Message: "must be a valid UUID"}}
	}
	return svc.OptionalUUID{Set: true, Value: id}, nil
}

// ---------------------------------------------------------------------------
// GET /v1/me, PUT /v1/me, GET /v1/me/profiles
// ---------------------------------------------------------------------------

// Get handles GET /v1/me: the caller's own user record.
func (h *MeHandler) Get(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	view, err := h.users.SelfGet(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// putRequest is the body of PUT /v1/me. Absent fields are left unchanged;
// default_profile_uuid and default_app_uuid accept null to clear.
type putRequest struct {
	GivenName          *string     `json:"given_name"`
	FamilyName         *string     `json:"family_name"`
	DefaultProfileUUID optionalRef `json:"default_profile_uuid"`
	DefaultAppUUID     optionalRef `json:"default_app_uuid"`
}

// Validate runs the stateless shape checks: a present name is not blank, a
// present reference is a uuid.
func (p *putRequest) Validate() []apiresp.FieldError {
	var out []apiresp.FieldError
	if p.GivenName != nil && isBlank(*p.GivenName) {
		out = append(out, apiresp.FieldError{Field: "given_name", Code: "users.given_name_required", Message: "given_name is required"})
	}
	if p.FamilyName != nil && isBlank(*p.FamilyName) {
		out = append(out, apiresp.FieldError{Field: "family_name", Code: "users.family_name_required", Message: "family_name is required"})
	}
	_, fe := p.DefaultProfileUUID.parse("default_profile_uuid")
	out = append(out, fe...)
	_, fe = p.DefaultAppUUID.parse("default_app_uuid")
	return append(out, fe...)
}

// Put handles PUT /v1/me: the caller's names (on their own person record) and
// default profile and app. Responds with the updated user record.
func (h *MeHandler) Put(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req putRequest
	if !server.Decode(w, r, &req) {
		return
	}
	defProfile, _ := req.DefaultProfileUUID.parse("default_profile_uuid")
	defApp, _ := req.DefaultAppUUID.parse("default_app_uuid")
	view, err := h.users.SelfUpdate(r.Context(), svc.UpdateSelfInput{
		GivenName: req.GivenName, FamilyName: req.FamilyName,
		DefaultProfile: defProfile, DefaultApp: defApp,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// Profiles handles GET /v1/me/profiles: every non-ended profile of the caller
// with its availability to the caller's session and, when locked, the lock
// reason and remedy (the ProfileView of the token response).
func (h *MeHandler) Profiles(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	views, err := h.users.SelfProfiles(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, map[string]any{"profiles": views})
}

// AcceptProfile handles POST /v1/me/profiles/{uuid}/accept: accepts an
// invitation to a party (invited to active). 200 with the profile as the
// caller's session sees it (a profile the party's policy locks for this session
// carries its lock reason and remedy); 404 for a profile the caller does not
// hold; 409 users.profile_transition_invalid when it is not an invitation.
func (h *MeHandler) AcceptProfile(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.profiles.AcceptInvitation(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// DeclineProfile handles POST /v1/me/profiles/{uuid}/decline: declines an
// invitation (invited to ended). 204; 404 for a profile the caller does not
// hold; 409 users.profile_transition_invalid when it is not an invitation.
func (h *MeHandler) DeclineProfile(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.profiles.DeclineInvitation(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// LeaveProfile handles POST /v1/me/profiles/{uuid}/leave: leaves a party
// (active or suspended to ended) and removes what the profile granted the
// caller there. 204; 404 for a profile the caller does not hold; 409
// users.profile_transition_invalid when it is not open.
func (h *MeHandler) LeaveProfile(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.profiles.LeaveProfile(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
