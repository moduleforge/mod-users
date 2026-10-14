package handlers

// SSHKeysHandler serves the SSH public-key lifecycle endpoints (see
// docs/architecture/ssh-keys.md for the design rationale): self-service
// register/list/revoke under /v1/self/ssh-keys, and operator-on-behalf-of
// register/list/revoke under /v1/user-accounts/{uuid}/ssh-keys. Kept thin
// (AGENTS.md "Handlers are thin"): parse input, call one service method,
// shape the response.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	usersservice "github.com/moduleforge/mod-users/api/internal/service"
)

// sshKeyRegisterBodyMaxBytes caps the POST body.
const sshKeyRegisterBodyMaxBytes = 32 * 1024

// sshKeyService is the subset of *usersservice.SSHKeyService SSHKeysHandler
// calls, declared as an interface so tests can substitute a stub service
// without a real database or transaction.
type sshKeyService interface {
	Register(ctx context.Context, accountUUID uuid.UUID, publicKeyLine string, label *string, stepUpUsed bool) (usersservice.SSHKey, error)
	List(ctx context.Context, accountUUID uuid.UUID, limit, offset int32) ([]usersservice.SSHKey, int64, error)
	Revoke(ctx context.Context, accountUUID, keyUUID uuid.UUID, stepUpUsed bool) error
}

// SSHKeysHandler serves the SSH public-key endpoints. It is a thin
// parse-call-render layer; all authorization, transaction management, and
// observer dispatch live in SSHKeyService.
type SSHKeysHandler struct {
	svc sshKeyService
	// jwtSecret, consumed, and stepUpRequired are the same step-up
	// dependencies NewIdentitiesHandler takes (identities.go) -- see
	// checkStepUp for the shared gate.
	jwtSecret      string
	consumed       *sync.Map
	stepUpRequired bool
}

// NewSSHKeysHandler constructs an SSHKeysHandler. Argument order matches the
// module manifest's sshKeysHandler entry.
func NewSSHKeysHandler(svc sshKeyService, jwtSecret string, consumed *sync.Map, stepUpRequired bool) *SSHKeysHandler {
	return &SSHKeysHandler{
		svc:            svc,
		jwtSecret:      jwtSecret,
		consumed:       consumed,
		stepUpRequired: stepUpRequired,
	}
}

// ---------------------------------------------------------------------------
// Response / request DTOs (matches api/openapi.yaml)
// ---------------------------------------------------------------------------

// sshKeyDTO is the public view of one registered SSH key. No internal ids --
// only the UUID (AGENTS.md "Internal IDs are never exposed").
type sshKeyDTO struct {
	UUID        string    `json:"uuid"`
	KeyType     string    `json:"key_type"`
	Fingerprint string    `json:"fingerprint"`
	PublicKey   string    `json:"public_key"`
	Label       string    `json:"label"`
	CreatedAt   time.Time `json:"created_at"`
}

// toSSHKeyDTO converts a service-layer SSHKey to its public DTO.
func toSSHKeyDTO(k usersservice.SSHKey) sshKeyDTO {
	return sshKeyDTO{
		UUID:        k.UUID.String(),
		KeyType:     k.KeyType,
		Fingerprint: k.Fingerprint,
		PublicKey:   k.PublicKey,
		Label:       k.Label,
		CreatedAt:   k.CreatedAt.Time,
	}
}

// sshKeyListResponse is the GET list envelope -- matches api/openapi.yaml's
// PaginatedSSHKeys schema (items + total).
type sshKeyListResponse struct {
	Items []sshKeyDTO `json:"items"`
	Total int64       `json:"total"`
}

// registerSSHKeyRequest is the body for POST .../ssh-keys.
type registerSSHKeyRequest struct {
	PublicKey string  `json:"public_key"`
	Label     *string `json:"label"`
}

// Validate implements apiresp.Validator: public_key is required, and the key
// and label must pass the stateless sshkey checks (sshkey.Parse,
// sshkey.NormalizeLabel; no database). They run here, ahead of the step-up
// gate, so a malformed key or label is a 400 and never consumes a step-up
// token. The service repeats the same checks for in-process callers.
func (r *registerSSHKeyRequest) Validate() []apiresp.FieldError {
	if r.PublicKey == "" {
		return []apiresp.FieldError{{
			Field: "public_key", Code: "users.public_key_required", Message: "public_key is required",
		}}
	}
	return usersservice.ValidateSSHKeyShape(r.PublicKey, r.Label)
}

// ---------------------------------------------------------------------------
// Self-service handlers -- account UUID from localauth.UserContext
// ---------------------------------------------------------------------------

// ListSelf handles GET /v1/self/ssh-keys. Not step-up-gated.
func (h *SSHKeysHandler) ListSelf(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())
	accountUUID, err := h.callerAccountUUID(uc)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	h.list(w, r, accountUUID)
}

// RegisterSelf handles POST /v1/self/ssh-keys. Step-up-gated when
// h.stepUpRequired is true.
func (h *SSHKeysHandler) RegisterSelf(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())
	req, ok := h.decodeRegister(w, r)
	if !ok {
		return
	}
	if err := checkStepUp(r, uc.UserAccountID, h.stepUpRequired, h.jwtSecret, h.consumed); err != nil {
		writeStepUpRequired(w, r)
		return
	}
	stepUpUsed := h.stepUpRequired

	accountUUID, err := h.callerAccountUUID(uc)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	h.register(w, r, accountUUID, req, stepUpUsed)
}

// RevokeSelf handles DELETE /v1/self/ssh-keys/{key_uuid}. Step-up-gated
// when h.stepUpRequired is true.
func (h *SSHKeysHandler) RevokeSelf(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())
	keyUUID, err := apiresp.ParseUUIDParam("key_uuid", chi.URLParam(r, "key_uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	if err := checkStepUp(r, uc.UserAccountID, h.stepUpRequired, h.jwtSecret, h.consumed); err != nil {
		writeStepUpRequired(w, r)
		return
	}
	stepUpUsed := h.stepUpRequired

	accountUUID, err := h.callerAccountUUID(uc)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	h.revoke(w, r, accountUUID, keyUUID, stepUpUsed)
}

// ---------------------------------------------------------------------------
// Operator handlers -- account UUID from the {uuid} path param. Not
// step-up-gated for a genuine operator-on-behalf-of-another-account call
// (docs/architecture/ssh-keys.md D9), matching every other
// /v1/user-accounts/* admin route. When the path {uuid} equals the
// caller's own account UUID,
// though, the caller is reaching a self-mutation through this route, so
// RegisterForAccount/RevokeForAccount apply the identical step-up gate the
// self routes use -- otherwise an ordinary authenticated user could bypass
// step-up entirely by calling the operator route with their own UUID
// (phase-1 security-001; see isSelfTargetingOperatorCall below).
// ---------------------------------------------------------------------------

// ListForAccount handles GET /v1/user-accounts/{uuid}/ssh-keys.
func (h *SSHKeysHandler) ListForAccount(w http.ResponseWriter, r *http.Request) {
	accountUUID, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	h.list(w, r, accountUUID)
}

// RegisterForAccount handles POST /v1/user-accounts/{uuid}/ssh-keys.
// Step-up-gated (phase-1 security-001; see docs/architecture/ssh-keys.md's
// D9), identically to RegisterSelf, only when the target account is the
// caller's own -- see isSelfTargetingOperatorCall.
func (h *SSHKeysHandler) RegisterForAccount(w http.ResponseWriter, r *http.Request) {
	accountUUID, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	req, ok := h.decodeRegister(w, r)
	if !ok {
		return
	}

	uc, isSelf, err := h.isSelfTargetingOperatorCall(r, accountUUID)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	stepUpUsed := false
	if isSelf {
		if err := checkStepUp(r, uc.UserAccountID, h.stepUpRequired, h.jwtSecret, h.consumed); err != nil {
			writeStepUpRequired(w, r)
			return
		}
		stepUpUsed = h.stepUpRequired
	}

	h.register(w, r, accountUUID, req, stepUpUsed)
}

// RevokeForAccount handles DELETE /v1/user-accounts/{uuid}/ssh-keys/{key_uuid}.
// Step-up-gated (phase-1 security-001; see docs/architecture/ssh-keys.md's
// D9), identically to RevokeSelf, only when the target account is the
// caller's own -- see isSelfTargetingOperatorCall.
func (h *SSHKeysHandler) RevokeForAccount(w http.ResponseWriter, r *http.Request) {
	accountUUID, err := apiresp.ParseUUIDParam("uuid", chi.URLParam(r, "uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	keyUUID, err := apiresp.ParseUUIDParam("key_uuid", chi.URLParam(r, "key_uuid"))
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	uc, isSelf, err := h.isSelfTargetingOperatorCall(r, accountUUID)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	stepUpUsed := false
	if isSelf {
		if err := checkStepUp(r, uc.UserAccountID, h.stepUpRequired, h.jwtSecret, h.consumed); err != nil {
			writeStepUpRequired(w, r)
			return
		}
		stepUpUsed = h.stepUpRequired
	}

	h.revoke(w, r, accountUUID, keyUUID, stepUpUsed)
}

// ---------------------------------------------------------------------------
// Shared implementations -- both route families call these
// ---------------------------------------------------------------------------

// list loads and renders accountUUID's active SSH keys.
func (h *SSHKeysHandler) list(w http.ResponseWriter, r *http.Request, accountUUID uuid.UUID) {
	limit, offset := parseSSHKeyListPagination(r)
	keys, total, err := h.svc.List(r.Context(), accountUUID, limit, offset)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	items := make([]sshKeyDTO, 0, len(keys))
	for _, k := range keys {
		items = append(items, toSSHKeyDTO(k))
	}
	server.JSON(w, http.StatusOK, sshKeyListResponse{Items: items, Total: total})
}

// decodeRegister decodes and validates the register body (stateless: JSON
// shape, size cap, required public_key, key and label format). It runs before the step-up gate so a
// malformed request is a 400 and never consumes a step-up token. When it
// returns false it has already written the 400 response.
func (h *SSHKeysHandler) decodeRegister(w http.ResponseWriter, r *http.Request) (registerSSHKeyRequest, bool) {
	var req registerSSHKeyRequest
	if !server.Decode(w, r, &req, apiresp.WithMaxBytes(sshKeyRegisterBodyMaxBytes)) {
		return req, false
	}
	return req, true
}

// register registers the already-decoded key for accountUUID. Key-format and
// label validation already ran in registerSSHKeyRequest.Validate; the service
// repeats it for in-process callers (algorithm policy: docs/mod-users-spec.md's
// Security requirements).
func (h *SSHKeysHandler) register(w http.ResponseWriter, r *http.Request, accountUUID uuid.UUID, req registerSSHKeyRequest, stepUpUsed bool) {
	key, err := h.svc.Register(r.Context(), accountUUID, req.PublicKey, req.Label, stepUpUsed)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	server.JSON(w, http.StatusCreated, toSSHKeyDTO(key))
}

// revoke archives keyUUID for accountUUID.
func (h *SSHKeysHandler) revoke(w http.ResponseWriter, r *http.Request, accountUUID, keyUUID uuid.UUID, stepUpUsed bool) {
	if err := h.svc.Revoke(r.Context(), accountUUID, keyUUID, stepUpUsed); err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// callerAccountUUID parses uc.UserUUID, the caller's own account UUID as
// placed on the request context by RequireAuth. A parse failure indicates an
// internal invariant violation (the session/JWT layer always mints a
// well-formed UUID), not a client input error, so it is wrapped and returned
// for apiresp.WriteError to classify as an internal error rather than mapped
// to invalid_input.
func (h *SSHKeysHandler) callerAccountUUID(uc *localauth.UserContext) (uuid.UUID, error) {
	id, err := uuid.Parse(uc.UserUUID)
	if err != nil {
		return uuid.UUID{}, fmt.Errorf("ssh_keys: parse caller account uuid: %w", err)
	}
	return id, nil
}

// isSelfTargetingOperatorCall reports whether an operator-route call
// (RegisterForAccount/RevokeForAccount) targets the caller's own account --
// i.e. targetAccountUUID equals the UUID resolved from the caller's own
// UserContext. Closes the bypass where an ordinary authenticated caller
// could reach a credential-mutating self-operation through the un-gated
// operator route by passing their own UUID in the path (phase-1
// security-001): SSHKeyService authorizes both route families identically
// via the Authorizer's ownership arm, so without this check a
// self-targeting call to the operator route would skip step-up entirely.
// The caller applies the step-up gate itself, exactly as RegisterSelf/
// RevokeSelf do, when isSelf is true.
//
// A non-nil err indicates an internal invariant violation resolving the
// caller's own account UUID (see callerAccountUUID); the caller must write
// the error response and return without proceeding.
func (h *SSHKeysHandler) isSelfTargetingOperatorCall(r *http.Request, targetAccountUUID uuid.UUID) (uc *localauth.UserContext, isSelf bool, err error) {
	uc = localauth.MustFromContext(r.Context())
	callerUUID, err := h.callerAccountUUID(uc)
	if err != nil {
		return uc, false, err
	}
	return uc, callerUUID == targetAccountUUID, nil
}

// parseSSHKeyListPagination reads limit/offset query params, leniently
// parsing and leaving invalid or absent values (zero) to
// SSHKeyService.List's own default/clamp handling, rather than duplicating
// that clamp here.
func parseSSHKeyListPagination(r *http.Request) (limit, offset int32) {
	q := r.URL.Query()
	if l := q.Get("limit"); l != "" {
		if v, err := strconv.ParseInt(l, 10, 32); err == nil {
			limit = int32(v)
		}
	}
	if o := q.Get("offset"); o != "" {
		if v, err := strconv.ParseInt(o, 10, 32); err == nil {
			offset = int32(v)
		}
	}
	return limit, offset
}
