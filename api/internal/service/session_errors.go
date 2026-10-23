package service

import (
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/useraction"
)

// Error codes of the sign-in and session flows (API surface section 7).
const (
	CodeInvalidCredentials  = "users.invalid_credentials"
	CodeSessionRevoked      = "users.session_revoked"
	CodeProfileUnavailable  = "users.profile_unavailable"
	CodeRefreshReuse        = "users.refresh_reuse"
	CodeAppLoginNotGranted  = "users.app_login_not_granted"
	CodeBYOIdentityDisabled = "users.byo_identity_disabled"
)

// LockAppLoginNotGranted is the lock reason a profile view carries when the
// session's invite-only app does not admit the profile (the app gate). It is
// not a profile_availability reason: the gate runs in Go.
const LockAppLoginNotGranted = "app_login_not_granted"

// AuthError is a sign-in or session refusal whose response carries its own
// error code (users.invalid_credentials, users.session_revoked, ...) rather
// than one of apiresp's reserved codes. It unwraps to the apiresp sentinel of
// its status (ErrUnauthenticated for 401, ErrForbidden for 403), so code that
// classifies with errors.Is keeps working; handlers render it with Status and
// Envelope. Messages are fixed strings and never echo request input.
type AuthError struct {
	Status  int
	Code    string
	Message string
	Details []apiresp.FieldError
}

func (e *AuthError) Error() string { return "auth: " + e.Code }

// Unwrap returns the apiresp sentinel of the error's status.
func (e *AuthError) Unwrap() error {
	if e.Status == http.StatusForbidden {
		return apiresp.ErrForbidden
	}
	return apiresp.ErrUnauthenticated
}

// Envelope returns the error envelope the HTTP layer writes with Status.
func (e *AuthError) Envelope() apiresp.Envelope {
	return apiresp.Envelope{Error: apiresp.ErrorBody{Code: e.Code, Message: e.Message, Details: e.Details}}
}

var (
	// ErrInvalidCredentials is every failed credential check of a sign-in:
	// an unknown identifier, a wrong password, an invalid or expired code,
	// unknown client credentials. The causes are indistinguishable.
	ErrInvalidCredentials = &AuthError{Status: http.StatusUnauthorized, Code: CodeInvalidCredentials, Message: "invalid credentials"}

	// ErrSessionRevoked is returned when the session behind a refresh token,
	// a handoff code or a session route is revoked, expired or unknown.
	ErrSessionRevoked = &AuthError{Status: http.StatusUnauthorized, Code: CodeSessionRevoked, Message: "the session is no longer active; sign in again"}

	// ErrRefreshReuse is returned when a spent refresh token is presented
	// again. The whole session has been revoked (revoke_reason refresh_reuse).
	ErrRefreshReuse = &AuthError{Status: http.StatusUnauthorized, Code: CodeRefreshReuse, Message: "the refresh token was already used; the session has been revoked"}

	// ErrAppLoginNotGranted is returned when the requested app is invite-only
	// and no candidate profile is admitted to it.
	ErrAppLoginNotGranted = &AuthError{Status: http.StatusForbidden, Code: CodeAppLoginNotGranted, Message: "this account is not granted sign-in to the app"}

	// ErrBYOIdentityDisabled is returned by self-registration when the
	// platform does not allow bring-your-own identities
	// (platform_settings.allow_byo_identity).
	ErrBYOIdentityDisabled = &AuthError{Status: http.StatusForbidden, Code: CodeBYOIdentityDisabled, Message: "self-registration is disabled"}
)

// ProfileUnavailable returns the 401 users.profile_unavailable refusal with
// the lock reason in its details, in the same shape the authentication
// middleware writes for a locked profile.
func ProfileUnavailable(reason string) error {
	return &AuthError{
		Status:  http.StatusUnauthorized,
		Code:    CodeProfileUnavailable,
		Message: "the profile is not available to this session",
		Details: []apiresp.FieldError{{Field: "profile", Code: reason, Message: "profile unavailable: " + reason}},
	}
}

// ErrStepUpRequired is every step-up refusal on the session routes: a wrong
// password or code, no credential of the requested kind, or a session that
// cannot step up (an impersonation). It is the shared apiresp action-required
// error (409 users.step_up_required), so apiresp.WriteError renders it.
var ErrStepUpRequired = apiresp.ActionRequired(useraction.StepUpRequired,
	"Confirm your identity to continue.", "/step-up", nil)
