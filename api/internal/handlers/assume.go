package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
)

// assumeServicer is the narrow interface AssumeHandler requires from
// UserAccountService. Using an interface here (rather than the concrete type)
// keeps the handler testable without wiring a full service.
type assumeServicer interface {
	Assume(ctx context.Context, targetUUID uuid.UUID) (sudoUA db.UserAccount, actorUA db.UserAccount, err error)
}

// AssumeHandler handles identity assumption for admins.
type AssumeHandler struct {
	svc       assumeServicer
	jwtSecret string
	issuer    string
}

// NewAssumeHandler creates an AssumeHandler.
func NewAssumeHandler(svc assumeServicer, jwtSecret, issuer string) *AssumeHandler {
	return &AssumeHandler{svc: svc, jwtSecret: jwtSecret, issuer: issuer}
}

// Assume handles POST /v1/user-accounts/{uuid}/assume (admin).
// Returns a JWT where the bearer acts as the assumed user account while the
// audit trail preserves the original admin identity.
func (h *AssumeHandler) Assume(w http.ResponseWriter, r *http.Request) {
	rawUUID := chi.URLParam(r, "uuid")
	parsed, err := uuid.Parse(rawUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid uuid")
		return
	}

	sudoUA, actorUA, err := h.svc.Assume(r.Context(), parsed)
	if err != nil {
		if errors.Is(err, coreservice.ErrNotFound) {
			server.Error(w, http.StatusNotFound, "not_found", "user account not found")
			return
		}
		slog.ErrorContext(r.Context(), "assume: service error", "error", err)
		writeServiceError(w, err)
		return
	}

	token, err := auth.IssueAssumeJWT(sudoUA, actorUA, h.jwtSecret, h.issuer)
	if err != nil {
		slog.ErrorContext(r.Context(), "assume: issue jwt", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to issue token")
		return
	}

	slog.InfoContext(r.Context(), "admin assuming identity",
		"sudo_uuid", sudoUA.Uuid.String(),
		"actor_uuid", actorUA.Uuid.String(),
	)

	server.JSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"uuid":  actorUA.Uuid.String(),
			"email": actorUA.Email,
		},
	})
}

// EndAssume handles DELETE /v1/assume (auth).
// The client simply discards the assume token and uses the original admin token,
// so this endpoint returns 204 as an explicit "clear" signal.
func (h *AssumeHandler) EndAssume(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}
