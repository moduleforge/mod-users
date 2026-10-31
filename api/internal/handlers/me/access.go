package me

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

// passwordFieldError reports the field error for a missing or out-of-range
// password, using the codes the credential service uses.
func passwordFieldError(password string) (apiresp.FieldError, bool) {
	if password == "" {
		return requiredField("password"), true
	}
	if svc.ValidatePassword(password) == nil {
		return apiresp.FieldError{}, false
	}
	if len(password) < 12 {
		return apiresp.FieldError{Field: "password", Code: "users.password_too_short", Message: "password must be at least 12 characters"}, true
	}
	return apiresp.FieldError{Field: "password", Code: "users.password_too_long", Message: "password is too long"}, true
}

// requiredField is the field error for a missing required string.
func requiredField(field string) apiresp.FieldError {
	return apiresp.FieldError{Field: field, Code: "users." + field + "_required", Message: field + " is required"}
}

// ---------------------------------------------------------------------------
// identifiers
// ---------------------------------------------------------------------------

// ListIdentifiers handles GET /v1/me/identifiers.
func (h *MeHandler) ListIdentifiers(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	views, err := h.users.SelfListIdentifiers(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, map[string]any{"identifiers": views})
}

// addIdentifierRequest is the body of POST /v1/me/identifiers.
type addIdentifierRequest struct {
	// Kind defaults to email, the only supported kind.
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Validate requires a value and a supported kind.
func (a *addIdentifierRequest) Validate() []apiresp.FieldError {
	var out []apiresp.FieldError
	if a.Kind != "" && a.Kind != svc.IdentifierKindEmail {
		out = append(out, apiresp.FieldError{
			Field: "kind", Code: "users.identifier_kind_unsupported", Message: "only email identifiers are supported",
		})
	}
	switch {
	case isBlank(a.Value):
		out = append(out, requiredField("value"))
	case a.Kind == "" || a.Kind == svc.IdentifierKindEmail:
		if err := svc.ValidateEmail(a.Value); err != nil {
			out = append(out, apiresp.FieldError{Field: "value", Code: "users.email_invalid", Message: "email is not a valid address"})
		}
	}
	return out
}

// AddIdentifier handles POST /v1/me/identifiers: adds an unverified email.
// 201 with the identifier.
func (h *MeHandler) AddIdentifier(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req addIdentifierRequest
	if !server.Decode(w, r, &req) {
		return
	}
	view, err := h.users.SelfAddIdentifier(r.Context(), svc.AddSelfIdentifierInput{Kind: req.Kind, Value: req.Value})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusCreated, view)
}

// RequestVerification handles POST /v1/me/identifiers/{uuid}/verification:
// emails a verification code. 204; a verified identifier or a request inside
// the cooldown sends nothing.
func (h *MeHandler) RequestVerification(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.users.SelfRequestVerification(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// verifyRequest is the body of POST /v1/me/identifiers/{uuid}/verify.
type verifyRequest struct {
	Code string `json:"code"`
}

// Validate requires the code.
func (v *verifyRequest) Validate() []apiresp.FieldError {
	if isBlank(v.Code) {
		return []apiresp.FieldError{requiredField("code")}
	}
	return nil
}

// VerifyIdentifier handles POST /v1/me/identifiers/{uuid}/verify: checks the
// emailed code and marks the identifier verified. 200 with the identifier.
func (h *MeHandler) VerifyIdentifier(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req verifyRequest
	if !server.Decode(w, r, &req) {
		return
	}
	view, err := h.users.SelfVerifyIdentifier(r.Context(), id, strings.TrimSpace(req.Code))
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// SetPrimaryIdentifier handles PUT /v1/me/identifiers/{uuid}/primary. The body
// is ignored. 200 with the identifier.
func (h *MeHandler) SetPrimaryIdentifier(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.users.SelfSetPrimaryIdentifier(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// DeleteIdentifier handles DELETE /v1/me/identifiers/{uuid}. 204.
func (h *MeHandler) DeleteIdentifier(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.users.SelfRemoveIdentifier(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// credentials
// ---------------------------------------------------------------------------

// ListCredentials handles GET /v1/me/credentials: the caller's active
// credentials.
func (h *MeHandler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	views, err := h.users.SelfListCredentials(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, map[string]any{"credentials": views})
}

// addPasswordRequest is the body of POST /v1/me/credentials/password.
type addPasswordRequest struct {
	Password string `json:"password"`
	Label    string `json:"label"`
	// IdentifierUUID binds the password to one of the caller's identifiers;
	// omitted binds it to the primary email.
	IdentifierUUID *string `json:"identifier_uuid"`
}

// Validate checks the password length, the label and the identifier uuid.
func (a *addPasswordRequest) Validate() []apiresp.FieldError {
	var out []apiresp.FieldError
	if fe, bad := passwordFieldError(a.Password); bad {
		out = append(out, fe)
	}
	if a.IdentifierUUID != nil {
		if id, err := uuid.Parse(*a.IdentifierUUID); err != nil || id == uuid.Nil {
			out = append(out, apiresp.FieldError{Field: "identifier_uuid", Code: "invalid_uuid", Message: "must be a valid UUID"})
		}
	}
	return out
}

// AddPassword handles POST /v1/me/credentials/password. 201 with the
// credential.
func (h *MeHandler) AddPassword(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req addPasswordRequest
	if !server.Decode(w, r, &req) {
		return
	}
	in := svc.AddSelfPasswordInput{Password: req.Password, Label: req.Label}
	if req.IdentifierUUID != nil {
		id, err := uuid.Parse(*req.IdentifierUUID) // checked by Validate
		if err != nil {
			writeError(w, r, apiresp.InvalidInput(apiresp.FieldError{Field: "identifier_uuid", Code: "invalid_uuid", Message: "must be a valid UUID"}))
			return
		}
		in.IdentifierUUID = &id
	}
	view, err := h.users.SelfAddPassword(r.Context(), in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusCreated, view)
}

// addSSHKeyRequest is the body of POST /v1/me/credentials/ssh-keys.
type addSSHKeyRequest struct {
	// PublicKey is one authorized_keys line.
	PublicKey string  `json:"public_key"`
	Label     *string `json:"label"`
}

// Validate requires the key and runs the stateless key and label checks.
func (a *addSSHKeyRequest) Validate() []apiresp.FieldError {
	if isBlank(a.PublicKey) {
		return []apiresp.FieldError{requiredField("public_key")}
	}
	return svc.ValidateSSHKeyShape(a.PublicKey, a.Label)
}

// AddSSHKey handles POST /v1/me/credentials/ssh-keys. 201 with the credential.
func (h *MeHandler) AddSSHKey(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req addSSHKeyRequest
	if !server.Decode(w, r, &req) {
		return
	}
	view, err := h.users.SelfAddSSHKey(r.Context(), svc.AddSelfSSHKeyInput{PublicKey: req.PublicKey, Label: req.Label})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusCreated, view)
}

// patchCredentialRequest is the body of PATCH /v1/me/credentials/{uuid}.
// Absent fields are left unchanged; default_profile_uuid accepts null to clear.
type patchCredentialRequest struct {
	Label              *string     `json:"label"`
	DefaultProfileUUID optionalRef `json:"default_profile_uuid"`
}

// Validate requires at least one field and a uuid default profile.
func (p *patchCredentialRequest) Validate() []apiresp.FieldError {
	if p.Label == nil && !p.DefaultProfileUUID.set {
		return []apiresp.FieldError{{Code: "users.nothing_to_update", Message: "label or default_profile_uuid is required"}}
	}
	_, fe := p.DefaultProfileUUID.parse("default_profile_uuid")
	return fe
}

// PatchCredential handles PATCH /v1/me/credentials/{uuid}: label and landing
// profile. 200 with the credential.
func (h *MeHandler) PatchCredential(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req patchCredentialRequest
	if !server.Decode(w, r, &req) {
		return
	}
	defProfile, _ := req.DefaultProfileUUID.parse("default_profile_uuid")
	view, err := h.users.SelfUpdateCredential(r.Context(), id, svc.UpdateSelfCredentialInput{
		Label: req.Label, DefaultProfile: defProfile,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// DeleteCredential handles DELETE /v1/me/credentials/{uuid}: revokes the
// credential. 204; the last interactive credential is 409
// users.last_credential.
func (h *MeHandler) DeleteCredential(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.users.SelfRevokeCredential(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// sessions
// ---------------------------------------------------------------------------

// ListSessions handles GET /v1/me/sessions: the caller's live sessions, the
// requesting one marked current.
func (h *MeHandler) ListSessions(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	views, err := h.users.SelfListSessions(r.Context())
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, map[string]any{"sessions": views})
}

// DeleteSession handles DELETE /v1/me/sessions/{uuid}. 204.
func (h *MeHandler) DeleteSession(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, err := pathUUID(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.users.SelfRevokeSession(r.Context(), id); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// guest upgrade
// ---------------------------------------------------------------------------

// upgradeRequest is the body of POST /v1/me/upgrade.
type upgradeRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
}

// Validate runs the stateless shape checks.
func (u *upgradeRequest) Validate() []apiresp.FieldError {
	var out []apiresp.FieldError
	if isBlank(u.Email) {
		out = append(out, requiredField("email"))
	} else if err := svc.ValidateEmail(u.Email); err != nil {
		out = append(out, apiresp.FieldError{Field: "email", Code: "users.email_invalid", Message: "email is not a valid address"})
	}
	if fe, bad := passwordFieldError(u.Password); bad {
		out = append(out, fe)
	}
	if isBlank(u.GivenName) {
		out = append(out, requiredField("given_name"))
	}
	if isBlank(u.FamilyName) {
		out = append(out, requiredField("family_name"))
	}
	return out
}

// Upgrade handles POST /v1/me/upgrade: turns the calling guest into a
// registered user. 200 with the user record. The guest's `anon` credential is
// revoked, which ends the guest's sessions, so the client signs in again with
// the email and password it just registered.
func (h *MeHandler) Upgrade(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req upgradeRequest
	if !server.Decode(w, r, &req) {
		return
	}
	view, err := h.users.SelfUpgradeGuest(r.Context(), svc.UpgradeGuestInput{
		Email: req.Email, Password: req.Password, GivenName: req.GivenName, FamilyName: req.FamilyName,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}
