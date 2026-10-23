package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// emailCodePurposeLogin is the only purpose the public email-code endpoints
// serve; email verification moved to the authenticated identifier routes.
const emailCodePurposeLogin = "login"

// emailCodeRequestBody is the body for POST /v1/auth/email-code/request.
type emailCodeRequestBody struct {
	Email string `json:"email"`
	// Purpose is optional and must be "login" when set.
	Purpose string `json:"purpose"`
}

// emailCodeVerifyBody is the body for POST /v1/auth/email-code/verify.
type emailCodeVerifyBody struct {
	Email   string `json:"email"`
	Code    string `json:"code"`
	Purpose string `json:"purpose"`
	// App optionally names the app the session signs in to.
	App string `json:"app"`
	// ProfileUUID optionally names the profile to land on.
	ProfileUUID string `json:"profile_uuid"`
}

// EmailCodeRequest handles POST /v1/auth/email-code/request. It always
// answers 204 after the same floor latency, whether or not the email is
// registered: the code is minted and sent in the background, and only for a
// verified email identifier holding an active email_otp credential.
func (h *Handler) EmailCodeRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req emailCodeRequestBody
	if !server.Decode(w, r, &req) {
		return
	}
	// A purpose other than login is skipped silently, indistinguishable from
	// an unknown email.
	if p := req.Purpose; p == "" || p == emailCodePurposeLogin {
		email := req.Email
		h.dispatch(r, "email_code", func(ctx context.Context) {
			if err := h.sessions.RequestLoginCode(ctx, email); err != nil {
				slog.ErrorContext(ctx, "email_code: request login code", "error", err)
			}
		})
	}
	answerAfterFloor(w, r, start)
}

// EmailCodeVerify handles POST /v1/auth/email-code/verify: sign-in with an
// emailed code. It answers 200 with the token response. Every failure (an
// unknown email, a wrong, expired or exhausted code) is 401
// users.invalid_credentials.
func (h *Handler) EmailCodeVerify(w http.ResponseWriter, r *http.Request) {
	var req emailCodeVerifyBody
	if !server.Decode(w, r, &req) {
		return
	}
	if details := required(
		[2]string{"email", strings.TrimSpace(req.Email)},
		[2]string{"code", req.Code},
	); len(details) > 0 {
		apiresp.WriteError(w, r, apiresp.InvalidInput(details...))
		return
	}
	if req.Purpose != "" && req.Purpose != emailCodePurposeLogin {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "purpose", Code: "users.purpose_invalid", Message: "purpose must be login",
		}))
		return
	}
	profileUUID, err := parseOptionalUUID("profile_uuid", req.ProfileUUID)
	if err != nil {
		writeError(w, r, err)
		return
	}

	resp, err := h.sessions.LoginWithEmailCode(r.Context(), svc.LoginInput{
		Email: req.Email, Code: req.Code, AppSlug: req.App, ProfileUUID: profileUUID,
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusOK, resp)
}
