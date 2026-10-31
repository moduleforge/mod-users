package principals

import (
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// The credential routes of /v1/principals authorize the `credentials`
// operation on the principal (mod-authz; max_auth_age 10 minutes, enforced by
// the engine's assurance gate as 409 users.step_up_required). The error order
// is 401, 400, 404 (unknown or unreadable principal), 403, 409. Secrets are
// never logged; an API key's secret is in the issuing response only.

// credentialPathUUIDs parses the {uuid} (and, when withCredential is set, the
// {credential_uuid}) URL parameters.
func credentialPathUUIDs(r *http.Request, withCredential bool) (principal, credential uuid.UUID, err error) {
	principal, err = apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		return uuid.Nil, uuid.Nil, err
	}
	if withCredential {
		credential, err = apiresp.ParseUUIDParam("credential_uuid", chi.URLParam(r, "credential_uuid"))
		if err != nil {
			return uuid.Nil, uuid.Nil, err
		}
	}
	return principal, credential, nil
}

// ListCredentials handles GET /v1/principals/{uuid}/credentials: the
// principal's active credentials, newest first. 200 with {"credentials": [...]}.
func (h *PrincipalsHandler) ListCredentials(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, _, err := credentialPathUUIDs(r, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	views, err := h.users.ListPrincipalCredentials(r.Context(), id)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if views == nil {
		views = []svc.CredentialView{}
	}
	apiresp.WriteJSON(w, http.StatusOK, map[string]any{"credentials": views})
}

// issueAPIKeyRequest is the optional body of POST
// /v1/principals/{uuid}/credentials/api-keys.
type issueAPIKeyRequest struct {
	Label              string     `json:"label"`
	ExpiresAt          *time.Time `json:"expires_at"`
	DefaultProfileUUID *string    `json:"default_profile_uuid"`
}

// Validate parses the optional default profile uuid.
func (q *issueAPIKeyRequest) Validate() []apiresp.FieldError {
	if q.DefaultProfileUUID == nil {
		return nil
	}
	if _, err := uuid.Parse(*q.DefaultProfileUUID); err != nil {
		return []apiresp.FieldError{{Field: "default_profile_uuid", Code: "invalid_uuid", Message: "must be a valid UUID"}}
	}
	return nil
}

// IssueAPIKey handles POST /v1/principals/{uuid}/credentials/api-keys: issues
// an API key to a service account. 201 with the credential and the one-time
// client_secret; a principal that is not a service account is a 400.
func (h *PrincipalsHandler) IssueAPIKey(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, _, err := credentialPathUUIDs(r, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req issueAPIKeyRequest
	if r.Body != nil && r.ContentLength != 0 {
		if !server.Decode(w, r, &req) {
			return
		}
	}
	in := svc.IssuePrincipalAPIKeyInput{Label: req.Label, ExpiresAt: req.ExpiresAt}
	if req.DefaultProfileUUID != nil {
		p, err := uuid.Parse(*req.DefaultProfileUUID) // checked by Validate
		if err != nil {
			writeError(w, r, apiresp.InvalidInput(apiresp.FieldError{Field: "default_profile_uuid", Code: "invalid_uuid", Message: "must be a valid UUID"}))
			return
		}
		in.DefaultProfile = &p
	}
	view, err := h.users.IssuePrincipalAPIKey(r.Context(), id, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	apiresp.WriteJSON(w, http.StatusCreated, view)
}

// installSSHKeyRequest is the body of POST
// /v1/principals/{uuid}/credentials/ssh-keys.
type installSSHKeyRequest struct {
	// PublicKey is one authorized_keys line.
	PublicKey string     `json:"public_key"`
	Label     *string    `json:"label"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// Validate requires the key and runs the stateless key and label checks.
func (q *installSSHKeyRequest) Validate() []apiresp.FieldError {
	if strings.TrimSpace(q.PublicKey) == "" {
		return []apiresp.FieldError{{Field: "public_key", Code: "users.public_key_required", Message: "public_key is required"}}
	}
	return svc.ValidateSSHKeyShape(q.PublicKey, q.Label)
}

// InstallSSHKey handles POST /v1/principals/{uuid}/credentials/ssh-keys:
// registers an SSH public key for the principal. 201 with the credential; a
// fingerprint that is active anywhere is 409 users.ssh_key_in_use.
func (h *PrincipalsHandler) InstallSSHKey(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, _, err := credentialPathUUIDs(r, false)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var req installSSHKeyRequest
	if !server.Decode(w, r, &req) {
		return
	}
	view, err := h.users.InstallPrincipalSSHKey(r.Context(), id, svc.InstallPrincipalSSHKeyInput{
		PublicKey: req.PublicKey, Label: req.Label, ExpiresAt: req.ExpiresAt,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusCreated, view)
}

// RevokeCredential handles DELETE
// /v1/principals/{uuid}/credentials/{credential_uuid}: revokes the credential
// with reason `admin`, ending the sessions that hold it. 204; an unknown,
// foreign or already revoked credential is 404.
func (h *PrincipalsHandler) RevokeCredential(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	id, credID, err := credentialPathUUIDs(r, true)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if err := h.users.RevokePrincipalCredential(r.Context(), id, credID); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
