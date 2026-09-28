package handlers

// SSHKeysHandler serves the SSH public-key lifecycle endpoints (design note
// ../../plan/notes/ssh-key-design.md D8-D10): self-service register/list/
// revoke under /v1/self/ssh-keys, and operator-on-behalf-of register/list/
// revoke under /v1/user-accounts/{uuid}/ssh-keys. Kept thin (AGENTS.md
// "Handlers are thin"): parse input, call one service method, shape the
// response.

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

// sshKeyRegisterBodyMaxBytes caps the POST body (design note D6).
const sshKeyRegisterBodyMaxBytes = 32 * 1024

// sshKeyService is the subset of *usersservice.SSHKeyService (task 003)
// SSHKeysHandler calls, declared as an interface so tests can substitute a
// stub service without a real database or transaction.
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
// Response / request DTOs (design note D10; matches api/openapi.yaml)
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

// ---------------------------------------------------------------------------
// Self-service handlers -- account UUID from localauth.UserContext
// ---------------------------------------------------------------------------

// ListSelf handles GET /v1/self/ssh-keys. Not step-up-gated (design note D8).
func (h *SSHKeysHandler) ListSelf(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())
	accountUUID, err := h.callerAccountUUID(uc)
	if err != nil {
		apiresp.WriteError(w, r, err)
		return
	}
	h.list(w, r, accountUUID)
}

// RegisterSelf handles POST /v1/self/ssh-keys. Step-up-gated (design note D8)
// when h.stepUpRequired is true.
func (h *SSHKeysHandler) RegisterSelf(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())
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
	h.register(w, r, accountUUID, stepUpUsed)
}

// RevokeSelf handles DELETE /v1/self/ssh-keys/{key_uuid}. Step-up-gated
// (design note D8) when h.stepUpRequired is true.
func (h *SSHKeysHandler) RevokeSelf(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())
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
	keyUUID, ok := parseUUIDPathParam(w, r, "key_uuid")
	if !ok {
		return
	}
	h.revoke(w, r, accountUUID, keyUUID, stepUpUsed)
}

// ---------------------------------------------------------------------------
// Operator handlers -- account UUID from the {uuid} path param. Not
// step-up-gated for a genuine operator-on-behalf-of-another-account call
// (design note D8/D9), matching every other /v1/user-accounts/* admin
// route. When the path {uuid} equals the caller's own account UUID,
// though, the caller is reaching a self-mutation through this route, so
// RegisterForAccount/RevokeForAccount apply the identical step-up gate the
// self routes use -- otherwise an ordinary authenticated user could bypass
// step-up entirely by calling the operator route with their own UUID
// (phase-1 security-001; see isSelfTargetingOperatorCall below).
// ---------------------------------------------------------------------------

// ListForAccount handles GET /v1/user-accounts/{uuid}/ssh-keys.
func (h *SSHKeysHandler) ListForAccount(w http.ResponseWriter, r *http.Request) {
	accountUUID, ok := parseUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	h.list(w, r, accountUUID)
}

// RegisterForAccount handles POST /v1/user-accounts/{uuid}/ssh-keys.
// Step-up-gated (design note D8; phase-1 security-001), identically to
// RegisterSelf, only when the target account is the caller's own -- see
// isSelfTargetingOperatorCall.
func (h *SSHKeysHandler) RegisterForAccount(w http.ResponseWriter, r *http.Request) {
	accountUUID, ok := parseUUIDPathParam(w, r, "uuid")
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

	h.register(w, r, accountUUID, stepUpUsed)
}

// RevokeForAccount handles DELETE /v1/user-accounts/{uuid}/ssh-keys/{key_uuid}.
// Step-up-gated (design note D8; phase-1 security-001), identically to
// RevokeSelf, only when the target account is the caller's own -- see
// isSelfTargetingOperatorCall.
func (h *SSHKeysHandler) RevokeForAccount(w http.ResponseWriter, r *http.Request) {
	accountUUID, ok := parseUUIDPathParam(w, r, "uuid")
	if !ok {
		return
	}
	keyUUID, ok := parseUUIDPathParam(w, r, "key_uuid")
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

// register parses, caps, and validates the request body, then registers the
// key for accountUUID. All key-format/label validation is the service's job
// (via sshkey.Parse/NormalizeLabel, design note D6/D7) -- this only checks
// for well-formed JSON and a non-empty public_key.
func (h *SSHKeysHandler) register(w http.ResponseWriter, r *http.Request, accountUUID uuid.UUID, stepUpUsed bool) {
	r.Body = http.MaxBytesReader(w, r.Body, sshKeyRegisterBodyMaxBytes)

	var req registerSSHKeyRequest
	if err := server.Decode(r, &req); err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}
	if req.PublicKey == "" {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return
	}

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

// parseUUIDPathParam extracts the named chi URL parameter, writing 400
// invalid_input on parse failure.
func parseUUIDPathParam(w http.ResponseWriter, r *http.Request, name string) (uuid.UUID, bool) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		apiresp.WriteError(w, r, apiresp.ErrInvalidInput)
		return uuid.UUID{}, false
	}
	return id, true
}

// parseSSHKeyListPagination reads limit/offset query params, leniently
// parsing and leaving invalid or absent values (zero) to
// SSHKeyService.List's own default/clamp handling (design note D10), rather
// than duplicating that clamp here.
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
