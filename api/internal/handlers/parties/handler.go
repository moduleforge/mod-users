// Package parties provides the HTTP handlers of the party-administration
// route groups: /v1/parties/{party_uuid}/... (the party's profiles and managed
// users) and /v1/profiles/{uuid} (a party profile's status). Every route
// requires an authenticated caller (mount the groups behind
// auth.NewRequireAuth).
//
// The handlers are thin: they parse and validate the request, call
// service.ProfileService and render the result. Authority and the HTTP error
// order (401, 400, 404, 403, 409) live in the service, so no route can skip
// them: a party or profile the caller may not read is a 404, the same as an
// unknown one.
//
// The party gating routes (authentication policy, credential authorizations
// and confinements) live in policy_routes.go and are served by
// service.PartyPolicyService; RegisterPartyRoutes mounts them with the rest.
package parties

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// PartiesHandler serves the party-administration routes. Construct it with
// NewPartiesHandler.
type PartiesHandler struct {
	profiles *svc.ProfileService
	policy   *svc.PartyPolicyService
}

// NewPartiesHandler constructs the party-administration handler over the
// profile service and the party gating service.
func NewPartiesHandler(profiles *svc.ProfileService, policy *svc.PartyPolicyService) *PartiesHandler {
	return &PartiesHandler{profiles: profiles, policy: policy}
}

// RegisterPartyRoutes mounts the /v1/parties routes onto r. The caller applies
// the authentication middleware (auth.NewRequireAuth) first.
func RegisterPartyRoutes(r chi.Router, h *PartiesHandler) {
	r.Get("/{party_uuid}/profiles", h.ListProfiles)
	r.Post("/{party_uuid}/profiles", h.InviteProfile)
	r.Post("/{party_uuid}/managed-users", h.CreateManagedUser)

	r.Get("/{party_uuid}/authn-policy", h.GetAuthnPolicy)
	r.Put("/{party_uuid}/authn-policy", h.PutAuthnPolicy)
	r.Get("/{party_uuid}/credential-authorizations", h.ListCredentialAuthorizations)
	r.Put("/{party_uuid}/credential-authorizations/{credential_uuid}", h.AuthorizeCredential)
	r.Delete("/{party_uuid}/credential-authorizations/{credential_uuid}", h.DeauthorizeCredential)
	r.Put("/{party_uuid}/confinements/{credential_uuid}", h.ConfineCredential)
	r.Delete("/{party_uuid}/confinements/{credential_uuid}", h.UnconfineCredential)
}

// RegisterProfileRoutes mounts the /v1/profiles routes onto r. The caller
// applies the authentication middleware (auth.NewRequireAuth) first.
func RegisterProfileRoutes(r chi.Router, h *PartiesHandler) {
	r.Patch("/{uuid}", h.PatchProfile)
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

// requireIdentity writes a 500 and returns false when the request has no
// authenticated identity (the group was mounted outside RequireAuth).
func requireIdentity(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := localauth.IdentityFromContext(r.Context()); !ok {
		apiresp.WriteError(w, r, errors.New("party routes mounted outside RequireAuth"))
		return false
	}
	return true
}

// queryInt32 parses an optional non-negative integer query parameter. An
// absent parameter is 0 (the service applies its default); a malformed or
// negative one is a 400 field error.
func queryInt32(r *http.Request, name string) (int32, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 0 {
		return 0, apiresp.InvalidInput(apiresp.FieldError{
			Field: name, Code: "users." + name + "_invalid", Message: name + " must be a non-negative integer",
		})
	}
	return int32(n), nil
}

// ListProfiles handles GET /v1/parties/{party_uuid}/profiles?status=&limit=&offset=.
// status is invited, active, suspended or ended (the profiles that are not
// ended when omitted). 200 with {"profiles", "total", "limit", "offset"}; 404
// for an unknown party and for one whose profiles the caller may not read.
func (h *PartiesHandler) ListProfiles(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	limit, err := queryInt32(r, "limit")
	if err != nil {
		writeError(w, r, err)
		return
	}
	offset, err := queryInt32(r, "offset")
	if err != nil {
		writeError(w, r, err)
		return
	}
	partyUUID, err := apiresp.ParseUUIDParam("party_uuid", chi.URLParam(r, "party_uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	page, err := h.profiles.ListPartyProfiles(r.Context(), partyUUID, svc.ListPartyProfilesInput{
		Status: r.URL.Query().Get("status"), Limit: limit, Offset: offset,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, page)
}

// inviteRequest is the body of POST /v1/parties/{party_uuid}/profiles. Exactly
// one of holder_email and holder_user_uuid names the invitee.
type inviteRequest struct {
	HolderEmail           string `json:"holder_email"`
	HolderUserUUID        string `json:"holder_user_uuid"`
	ConfirmPolicyMismatch bool   `json:"confirm_policy_mismatch"`
}

// holderUUID parses holder_user_uuid; uuid.Nil when absent.
func (b *inviteRequest) holderUUID() (uuid.UUID, bool) {
	if b.HolderUserUUID == "" {
		return uuid.Nil, true
	}
	id, err := uuid.Parse(b.HolderUserUUID)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// Validate runs the stateless shape checks: exactly one holder, an email that
// is an address, a uuid that is a uuid.
func (b *inviteRequest) Validate() []apiresp.FieldError {
	var out []apiresp.FieldError
	if _, ok := b.holderUUID(); !ok {
		out = append(out, apiresp.FieldError{Field: "holder_user_uuid", Code: "invalid_uuid", Message: "must be a valid UUID"})
	}
	hasEmail, hasUUID := b.HolderEmail != "", b.HolderUserUUID != ""
	switch {
	case hasEmail && hasUUID:
		out = append(out, apiresp.FieldError{Field: "holder", Code: "users.holder_ambiguous", Message: "give either holder_email or holder_user_uuid, not both"})
	case !hasEmail && !hasUUID:
		out = append(out, apiresp.FieldError{Field: "holder", Code: "users.holder_required", Message: "holder_email or holder_user_uuid is required"})
	case hasEmail:
		if err := svc.ValidateEmail(b.HolderEmail); err != nil {
			out = append(out, apiresp.FieldError{Field: "holder_email", Code: "users.email_invalid", Message: "holder_email is not a valid address"})
		}
	}
	return out
}

// InviteProfile handles POST /v1/parties/{party_uuid}/profiles: invites a user
// to the party. 201 with the new `invited` profile; 404 when the party is
// unreadable or the invitee unknown; 409 for an existing profile, a guest
// invitee (users.guest_upgrade_required), an inadmissible invitee
// (users.holder_not_admissible) or a policy mismatch needing confirmation.
func (h *PartiesHandler) InviteProfile(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req inviteRequest
	if !server.Decode(w, r, &req) {
		return
	}
	partyUUID, err := apiresp.ParseUUIDParam("party_uuid", chi.URLParam(r, "party_uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	holder, _ := req.holderUUID()
	view, err := h.profiles.InviteProfile(r.Context(), partyUUID, svc.InviteProfileInput{
		HolderEmail: req.HolderEmail, HolderUserUUID: holder, ConfirmPolicyMismatch: req.ConfirmPolicyMismatch,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusCreated, view)
}

// managedUserRequest is the body of POST /v1/parties/{party_uuid}/managed-users.
type managedUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Validate runs the stateless shape checks.
func (b *managedUserRequest) Validate() []apiresp.FieldError {
	var out []apiresp.FieldError
	switch {
	case b.Email == "":
		out = append(out, apiresp.FieldError{Field: "email", Code: "users.email_required", Message: "email is required"})
	default:
		if err := svc.ValidateEmail(b.Email); err != nil {
			out = append(out, apiresp.FieldError{Field: "email", Code: "users.email_invalid", Message: "email is not a valid address"})
		}
	}
	switch {
	case b.Password == "":
		out = append(out, apiresp.FieldError{Field: "password", Code: "users.password_required", Message: "password is required"})
	default:
		if err := svc.ValidatePassword(b.Password); err != nil {
			code, msg := "users.password_too_short", "password must be at least 12 characters"
			if len(b.Password) >= 12 {
				code, msg = "users.password_too_long", "password is too long"
			}
			out = append(out, apiresp.FieldError{Field: "password", Code: code, Message: msg})
		}
	}
	return out
}

// CreateManagedUser handles POST /v1/parties/{party_uuid}/managed-users:
// creates a user owned and managed by the party, with an active party profile
// and an initial password. 201 with {"user", "profile"}; 409
// users.identifier_taken when the address is registered.
func (h *PartiesHandler) CreateManagedUser(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req managedUserRequest
	if !server.Decode(w, r, &req) {
		return
	}
	partyUUID, err := apiresp.ParseUUIDParam("party_uuid", chi.URLParam(r, "party_uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.profiles.CreateManagedUser(r.Context(), partyUUID, req.Email, req.Password)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusCreated, view)
}

// patchRequest is the body of PATCH /v1/profiles/{uuid}.
type patchRequest struct {
	Status    string `json:"status"`
	EndReason string `json:"end_reason"`
}

// Validate runs the stateless shape checks: a known target status, an end
// reason with `ended` only.
func (b *patchRequest) Validate() []apiresp.FieldError {
	switch b.Status {
	case svc.ProfileStatusActive, svc.ProfileStatusSuspended:
		if b.EndReason != "" {
			return []apiresp.FieldError{{Field: "end_reason", Code: "users.end_reason_unexpected", Message: "end_reason applies only when ending a profile"}}
		}
	case svc.ProfileStatusEnded:
		if b.EndReason == "" {
			return []apiresp.FieldError{{Field: "end_reason", Code: svc.CodeProfileEndReason, Message: "end_reason is required"}}
		}
	case "":
		return []apiresp.FieldError{{Field: "status", Code: "users.status_required", Message: "status is required"}}
	default:
		return []apiresp.FieldError{{Field: "status", Code: "users.profile_status_invalid", Message: "status must be active, suspended or ended"}}
	}
	return nil
}

// PatchProfile handles PATCH /v1/profiles/{uuid}: suspends (active to
// suspended), resumes (suspended to active) or ends a party profile. Needs
// `update` on the profile and a fresh authentication. 200 with the updated
// profile; 404 for an unknown profile and one the caller may not read; 409 for
// a status the profile cannot move to (users.profile_transition_invalid) and
// for a stale authentication (users.step_up_required).
func (h *PartiesHandler) PatchProfile(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req patchRequest
	if !server.Decode(w, r, &req) {
		return
	}
	id, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.profiles.UpdateProfileStatus(r.Context(), id, svc.UpdateProfileStatusInput{
		Status: req.Status, EndReason: req.EndReason,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}
