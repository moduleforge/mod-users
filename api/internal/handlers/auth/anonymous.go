package auth

import (
	"log/slog"
	"net/http"

	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
	svc "github.com/moduleforge/users-module/api/internal/service"
)

// anonymousRequest is the body for POST /v1/auth/anonymous.
type anonymousRequest struct {
	DeviceID string `json:"device_id"` // required; stable device fingerprint
}

// Anonymous handles POST /v1/auth/anonymous.
// It creates an anonymous user account and returns a JWT plus a session token.
// The endpoint is unauthenticated.
func (h *Handler) Anonymous(w http.ResponseWriter, r *http.Request) {
	var req anonymousRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.DeviceID == "" {
		server.Error(w, http.StatusBadRequest, "validation_error", "device_id is required")
		return
	}

	result, err := h.userSvc.CreateAnonymousUser(r.Context(), svc.CreateAnonymousUserInput{
		DeviceID: req.DeviceID,
	})
	if err != nil {
		slog.ErrorContext(r.Context(), "anonymous: create anonymous user", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to create anonymous account")
		return
	}

	// Issue a JWT for the new anonymous account. The token carries is_anonymous=true
	// so middleware (e.g. RequireVerifiedEmail) can distinguish anonymous sessions
	// without a database round-trip.
	//
	// We need a db.UserAccount for IssueAnonymousJWT. Build a minimal one from the
	// service result — only Uuid is consumed by the JWT function.
	dbUA, err := h.queries.GetUserAccountByUUID(r.Context(), result.UserAccount.UUID)
	if err != nil {
		slog.ErrorContext(r.Context(), "anonymous: load user account for jwt", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to issue token")
		return
	}

	token, err := localauth.IssueAnonymousJWT(dbUA, h.jwtSecret, h.issuer)
	if err != nil {
		slog.ErrorContext(r.Context(), "anonymous: issue jwt", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to issue token")
		return
	}

	slog.InfoContext(r.Context(), "anonymous user created",
		"user_account_uuid", result.UserAccount.UUID.String(),
		"device_id", req.DeviceID,
	)

	server.JSON(w, http.StatusCreated, map[string]any{
		"token":         token,
		"session_token": result.AnonToken.SessionToken,
		"user": map[string]any{
			"uuid":         result.UserAccount.UUID.String(),
			"is_anonymous": true,
		},
	})
}
