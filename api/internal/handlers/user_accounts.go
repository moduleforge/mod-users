package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	localAuthz "github.com/moduleforge/users-module/api/internal/authz"
	"github.com/moduleforge/users-module/api/internal/server"
	svc "github.com/moduleforge/users-module/api/internal/service"
)

// GrantAdminFn creates or removes a wildcard manage grant for the given user account UUID.
// It is injected from the composition root to avoid a direct peer dependency on authz-module.
// The function is responsible for both authorization and the grant write/delete.
type GrantAdminFn func(ctx context.Context, userAccountUUID uuid.UUID) error

// UserAccountsHandler serves the /v1/user-accounts endpoints.
// It is a thin parse-call-render layer; all authorization, transaction
// management, and observer dispatch live in UserAccountService.
type UserAccountsHandler struct {
	svc         *svc.UserAccountService
	grantAdmin  GrantAdminFn // creates wildcard manage grant
	revokeAdmin GrantAdminFn // deletes wildcard manage grant
}

// NewUserAccountsHandler creates a UserAccountsHandler backed by the given service.
// grantAdmin and revokeAdmin are injected from the composition root to call
// authz-module's CreateWildcardGrant / DeleteWildcardGrant without a direct peer import.
func NewUserAccountsHandler(service *svc.UserAccountService, grantAdmin, revokeAdmin GrantAdminFn) *UserAccountsHandler {
	return &UserAccountsHandler{svc: service, grantAdmin: grantAdmin, revokeAdmin: revokeAdmin}
}

// writeServiceError maps a service error to the appropriate HTTP response.
func writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, localAuthz.ErrUnauthenticated):
		server.Error(w, http.StatusUnauthorized, "unauthorized", "authentication required")
	case errors.Is(err, localAuthz.ErrForbidden):
		server.Error(w, http.StatusForbidden, "forbidden", "access denied")
	case errors.Is(err, svc.ErrEmailTaken):
		server.Error(w, http.StatusConflict, "email_taken", "an account with that email already exists")
	case errors.Is(err, svc.ErrInvalidInput):
		server.Error(w, http.StatusBadRequest, "validation_error", err.Error())
	default:
		server.Error(w, http.StatusInternalServerError, "internal_error", "an unexpected error occurred")
	}
}

// createUserAccountRequest is the body for POST /v1/user-accounts (admin).
type createUserAccountRequest struct {
	Email      string  `json:"email"`
	Password   *string `json:"password"`
	GivenName  string  `json:"given_name"`
	FamilyName string  `json:"family_name"`
}

// Create handles POST /v1/user-accounts (admin).
func (h *UserAccountsHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createUserAccountRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	ua, err := h.svc.Create(r.Context(), svc.CreateUserAccountInput{
		Email:      req.Email,
		Password:   req.Password,
		GivenName:  req.GivenName,
		FamilyName: req.FamilyName,
	})
	if err != nil {
		if !errors.Is(err, svc.ErrInvalidInput) {
			slog.ErrorContext(r.Context(), "user_accounts.create", "error", err)
		}
		writeServiceError(w, err)
		return
	}

	server.JSON(w, http.StatusCreated, userAccountResponse(ua))
}

// List handles GET /v1/user-accounts (admin).
func (h *UserAccountsHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	search := q.Get("q")
	if email := q.Get("email"); email != "" && search == "" {
		search = email
	}

	limit := int32(20)
	offset := int32(0)
	if l := q.Get("limit"); l != "" {
		v, err := strconv.ParseInt(l, 10, 32)
		if err == nil && v > 0 {
			limit = int32(v)
		}
	}
	if o := q.Get("offset"); o != "" {
		v, err := strconv.ParseInt(o, 10, 32)
		if err == nil && v >= 0 {
			offset = int32(v)
		}
	}

	accounts, err := h.svc.List(r.Context(), svc.ListUserAccountsInput{
		Search: search,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.list", "error", err)
		writeServiceError(w, err)
		return
	}

	resp := make([]map[string]any, 0, len(accounts))
	for _, ua := range accounts {
		resp = append(resp, userAccountResponse(ua))
	}

	server.JSON(w, http.StatusOK, map[string]any{
		"user_accounts": resp,
		"total":         len(resp),
	})
}

// Get handles GET /v1/user-accounts/{uuid} (admin).
func (h *UserAccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	ua, err := h.svc.Get(r.Context(), id)
	if err != nil {
		if err == pgx.ErrNoRows {
			server.Error(w, http.StatusNotFound, "not_found", "user account not found")
			return
		}
		slog.ErrorContext(r.Context(), "user_accounts.get", "error", err)
		writeServiceError(w, err)
		return
	}

	detail := userAccountResponse(ua)
	if ua.GivenName != "" || ua.FamilyName != "" {
		detail["given_name"] = ua.GivenName
		detail["family_name"] = ua.FamilyName
		detail["entity_kind"] = ua.EntityKind
		detail["display_name"] = strings.TrimSpace(ua.GivenName + " " + ua.FamilyName)
	}

	server.JSON(w, http.StatusOK, detail)
}

// updateUserAccountRequest is the body for PUT /v1/user-accounts/{uuid} (admin).
type updateUserAccountRequest struct {
	Email      *string `json:"email"`
	GivenName  *string `json:"given_name"`
	FamilyName *string `json:"family_name"`
}

// Update handles PUT /v1/user-accounts/{uuid} (admin).
func (h *UserAccountsHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	var req updateUserAccountRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	ua, err := h.svc.Update(r.Context(), id, svc.UpdateUserAccountInput{
		Email:      req.Email,
		GivenName:  req.GivenName,
		FamilyName: req.FamilyName,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.update", "error", err)
		writeServiceError(w, err)
		return
	}

	server.JSON(w, http.StatusOK, userAccountResponse(ua))
}

// Delete handles DELETE /v1/user-accounts/{uuid} (admin) — soft-deletes by archiving the entity.
func (h *UserAccountsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	if err := h.svc.Delete(r.Context(), id); err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.delete", "error", err)
		writeServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// GrantAdmin handles POST /v1/user-accounts/{uuid}/grant-admin (admin).
// Delegates to the injected grantAdmin function, which checks authorization and
// creates a wildcard manage grant for the target user.
func (h *UserAccountsHandler) GrantAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	if err := h.grantAdmin(r.Context(), id); err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.grant-admin", "error", err)
		writeServiceError(w, err)
		return
	}

	server.JSON(w, http.StatusOK, map[string]any{"uuid": id.String()})
}

// RevokeAdmin handles POST /v1/user-accounts/{uuid}/revoke-admin (admin).
// Delegates to the injected revokeAdmin function, which checks authorization and
// deletes the wildcard manage grant for the target user.
func (h *UserAccountsHandler) RevokeAdmin(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	if err := h.revokeAdmin(r.Context(), id); err != nil {
		slog.ErrorContext(r.Context(), "user_accounts.revoke-admin", "error", err)
		writeServiceError(w, err)
		return
	}

	server.JSON(w, http.StatusOK, map[string]any{"uuid": id.String()})
}

// parseUUIDParam extracts the {uuid} chi URL parameter, writing a 400 on parse failure.
func parseUUIDParam(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "uuid")
	id, err := uuid.Parse(raw)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid uuid")
		return uuid.UUID{}, false
	}
	return id, true
}

// userAccountResponse builds a public-facing map from a service UserAccount.
func userAccountResponse(ua svc.UserAccount) map[string]any {
	resp := map[string]any{
		"uuid":           ua.UUID.String(),
		"email":          ua.Email,
		"email_verified": ua.EmailVerified,
	}
	if ua.CreatedAt.Valid {
		resp["created_at"] = ua.CreatedAt.Time
	}
	return resp
}
