package auth

import (
	"net/http"
	"strings"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// loginRequest is the body for POST /v1/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// App optionally names the app the session signs in to.
	App string `json:"app"`
	// ProfileUUID optionally names the profile to land on.
	ProfileUUID string `json:"profile_uuid"`
}

// Login handles POST /v1/auth/login: password sign-in by email. It answers
// 200 with the token response; access_token is null and selection_required
// true when the landing chain finds no profile. Every credential failure is
// 401 users.invalid_credentials.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !server.Decode(w, r, &req) {
		return
	}
	if details := required(
		[2]string{"email", strings.TrimSpace(req.Email)},
		[2]string{"password", req.Password},
	); len(details) > 0 {
		apiresp.WriteError(w, r, apiresp.InvalidInput(details...))
		return
	}
	profileUUID, err := parseOptionalUUID("profile_uuid", req.ProfileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp, err := h.sessions.LoginWithPassword(r.Context(), svc.LoginInput{
		Email: req.Email, Password: req.Password, AppSlug: req.App, ProfileUUID: profileUUID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}
