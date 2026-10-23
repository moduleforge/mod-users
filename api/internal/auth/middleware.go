package auth

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
)

// writeAuthError maps an Authenticate error to its HTTP response:
//
//	ErrNoAuthHeader  401 unauthorized (missing or malformed header)
//	ErrInvalidToken  401 unauthorized
//	*NotLiveError    401 users.session_revoked, or 401
//	                 users.profile_unavailable with the lock reason in details
//	anything else    500 internal_error (logged)
func writeAuthError(w http.ResponseWriter, r *http.Request, err error) {
	var notLive *NotLiveError
	switch {
	case errors.Is(err, ErrNoAuthHeader):
		if r.Header.Get("Authorization") == "" {
			server.Error(w, http.StatusUnauthorized, "unauthorized", "missing Authorization header")
		} else {
			server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid Authorization header format")
		}
	case errors.Is(err, ErrInvalidToken):
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
	case errors.As(err, &notLive):
		if notLive.Code() == CodeSessionRevoked {
			server.Error(w, http.StatusUnauthorized, CodeSessionRevoked, "the session is no longer active; sign in again")
			return
		}
		server.ErrorWithDetails(w, http.StatusUnauthorized, CodeProfileUnavailable,
			"the profile is not available to this session",
			[]apiresp.FieldError{{
				Field:   "profile",
				Code:    notLive.LockReason(),
				Message: "profile unavailable: " + notLive.LockReason(),
			}})
	default:
		slog.ErrorContext(r.Context(), "authentication error", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to authenticate request")
	}
}

// NewRequireAuth returns middleware that authenticates the bearer token with
// a, answers 401 unless the session or credential and its profile are live,
// and otherwise installs the Identity and its opctx.AuthContext on the
// request context.
func NewRequireAuth(a *Authenticator) func(http.Handler) http.Handler {
	if a == nil {
		panic("auth: NewRequireAuth: authenticator must not be nil")
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, err := a.Authenticate(r)
			if err != nil {
				writeAuthError(w, r, err)
				return
			}
			next.ServeHTTP(w, r.WithContext(WithIdentity(r.Context(), id)))
		})
	}
}
