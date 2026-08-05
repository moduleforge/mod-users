package auth

import (
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
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
		server.Error(w, http.StatusBadRequest, "invalid_input", "invalid JSON body")
		return
	}

	if req.DeviceID == "" {
		server.ErrorWithDetails(w, http.StatusBadRequest, "invalid_input", "device_id is required",
			[]apiresp.FieldError{{Field: "device_id", Code: "users.device_id_required", Message: "device_id is required"}})
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

	// Issue a JWT for the new guest account. The token carries is_anonymous=true
	// for forward-compatibility, but the claim is not read by any middleware
	// today — RequireVerifiedEmail (and everything else in the claim-mapping /
	// resolve pipeline) decides purely from the account's EmailVerifiedAt
	// timestamp, not this claim. This is unrelated to the separate, shared
	// anonymous system actor (api/internal/auth/anonymous_actor.go), which has
	// no JWT at all.
	//
	// IssueAnonymousJWT only reads ua.Uuid, so we build a minimal db.UserAccount
	// from the service result UUID — no additional DB round-trip is required.
	dbUA := db.UserAccount{Uuid: uuid.UUID(result.UserAccount.UUID)}

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
