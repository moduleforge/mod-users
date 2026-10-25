package parties

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// partyAndCredential parses the party_uuid and credential_uuid path
// parameters; it writes the 400 and returns false on a malformed one.
func partyAndCredential(w http.ResponseWriter, r *http.Request) (party, credential uuid.UUID, ok bool) {
	party, err := apiresp.ParseUUIDParam("party_uuid", chi.URLParam(r, "party_uuid"))
	if err != nil {
		writeError(w, r, err)
		return uuid.Nil, uuid.Nil, false
	}
	credential, err = apiresp.ParseUUIDParam("credential_uuid", chi.URLParam(r, "credential_uuid"))
	if err != nil {
		writeError(w, r, err)
		return uuid.Nil, uuid.Nil, false
	}
	return party, credential, true
}

// GetAuthnPolicy handles GET /v1/parties/{party_uuid}/authn-policy: the
// party's effective authentication policy (credential_rule `any`, min_acr 1,
// is_default true when it has none). 404 for an unknown party and for one
// whose profiles the caller may not read.
func (h *PartiesHandler) GetAuthnPolicy(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	party, err := apiresp.ParseUUIDParam("party_uuid", chi.URLParam(r, "party_uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	view, err := h.policy.GetAuthnPolicy(r.Context(), party)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// authnPolicyRequest is the body of PUT /v1/parties/{party_uuid}/authn-policy.
// min_acr defaults to 1 when omitted.
type authnPolicyRequest struct {
	CredentialRule         string  `json:"credential_rule"`
	RequiredOIDCProviderID *string `json:"required_oidc_provider_id"`
	MinACR                 *int    `json:"min_acr"`
}

// PutAuthnPolicy handles PUT /v1/parties/{party_uuid}/authn-policy: replaces
// the party's authentication policy. Needs `update` on the party and a fresh
// authentication. 200 with the policy; 400 for a malformed policy or an
// unknown provider; 404 for an unknown party and for one the caller may not
// read; 403 without `update`; 409 for a stale authentication
// (users.step_up_required).
func (h *PartiesHandler) PutAuthnPolicy(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	var req authnPolicyRequest
	if !server.Decode(w, r, &req) {
		return
	}
	party, err := apiresp.ParseUUIDParam("party_uuid", chi.URLParam(r, "party_uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	in := svc.PutPartyAuthnPolicyInput{CredentialRule: req.CredentialRule, MinACR: svc.DefaultMinACR}
	if req.RequiredOIDCProviderID != nil {
		in.RequiredOIDCProviderID = *req.RequiredOIDCProviderID
	}
	if req.MinACR != nil {
		in.MinACR = *req.MinACR
	}
	view, err := h.policy.PutAuthnPolicy(r.Context(), party, in)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// ListCredentialAuthorizations handles GET
// /v1/parties/{party_uuid}/credential-authorizations: the credentials
// authorized for the party. Needs `update` on the party.
func (h *PartiesHandler) ListCredentialAuthorizations(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	party, err := apiresp.ParseUUIDParam("party_uuid", chi.URLParam(r, "party_uuid"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	list, err := h.policy.ListCredentialAuthorizations(r.Context(), party)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, list)
}

// AuthorizeCredential handles PUT
// /v1/parties/{party_uuid}/credential-authorizations/{credential_uuid}:
// authorizes the credential for the party (idempotent). A party administrator,
// or the credential's holder through a session that satisfies the party's gate
// through another factor, may write it; everyone else gets 403 (404 when the
// party is unreadable). 404 when the credential is unknown or its holder has
// no open profile in the party; 409 for a stale authentication.
func (h *PartiesHandler) AuthorizeCredential(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	party, cred, ok := partyAndCredential(w, r)
	if !ok {
		return
	}
	view, err := h.policy.AuthorizeCredential(r.Context(), party, cred)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// DeauthorizeCredential handles DELETE
// /v1/parties/{party_uuid}/credential-authorizations/{credential_uuid}. The
// same callers, statuses and masking as AuthorizeCredential; 404 as well when
// the credential has no authorization. 204 on success.
func (h *PartiesHandler) DeauthorizeCredential(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	party, cred, ok := partyAndCredential(w, r)
	if !ok {
		return
	}
	if err := h.policy.DeauthorizeCredential(r.Context(), party, cred); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ConfineCredential handles PUT
// /v1/parties/{party_uuid}/confinements/{credential_uuid}: confines the
// credential to the party (idempotent). The same callers, statuses and masking
// as AuthorizeCredential; 409 users.credential_confined for a credential
// confined to another party.
func (h *PartiesHandler) ConfineCredential(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	party, cred, ok := partyAndCredential(w, r)
	if !ok {
		return
	}
	view, err := h.policy.ConfineCredential(r.Context(), party, cred)
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, view)
}

// UnconfineCredential handles DELETE
// /v1/parties/{party_uuid}/confinements/{credential_uuid}: clears the
// credential's confinement when it names this party. The same callers,
// statuses and masking as AuthorizeCredential; 404 as well when the credential
// is not confined; 409 users.credential_confined when it is confined to
// another party. 204 on success.
func (h *PartiesHandler) UnconfineCredential(w http.ResponseWriter, r *http.Request) {
	if !requireIdentity(w, r) {
		return
	}
	party, cred, ok := partyAndCredential(w, r)
	if !ok {
		return
	}
	if err := h.policy.UnconfineCredential(r.Context(), party, cred); err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
