package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/moduleforge/users-module/api/internal/server"
)

// RequireAuth returns middleware that validates the Authorization header,
// maps claims to a Principal, resolves/creates the user, and stores
// *UserContext on the request context.
func RequireAuth(verifier *Verifier, mapper ClaimMapper, resolver *UserResolver) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Extract bearer token.
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				server.Error(w, http.StatusUnauthorized, "unauthorized", "missing Authorization header")
				return
			}
			if !strings.HasPrefix(authHeader, "Bearer ") {
				server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid Authorization header format")
				return
			}
			rawToken := strings.TrimPrefix(authHeader, "Bearer ")

			// 2. Verify token.
			claims, err := verifier.Verify(r.Context(), rawToken)
			if err != nil {
				server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired token")
				return
			}

			// 3. Map claims to Principal.
			principal, err := mapper.Map(claims)
			if err != nil {
				slog.ErrorContext(r.Context(), "claim mapper error", "error", err)
				server.Error(w, http.StatusInternalServerError, "internal_error", "failed to process authentication claims")
				return
			}

			// 4. Resolve user.
			uc, err := resolver.Resolve(r.Context(), principal)
			if err != nil {
				// A deleted user presenting a still-valid locally-issued JWT
				// is an auth failure, not a server fault. Surface 401 so the
				// caller re-authenticates rather than retrying.
				if errors.Is(err, ErrUserGone) {
					server.Error(w, http.StatusUnauthorized, "unauthorized", "user no longer exists")
					return
				}
				slog.ErrorContext(r.Context(), "user resolve error", "error", err)
				server.Error(w, http.StatusInternalServerError, "internal_error", "failed to resolve user")
				return
			}

			// 5. Stash on context.
			ctx := WithUserContext(r.Context(), uc)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin checks that the current user is an admin. 403 otherwise.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uc, ok := FromContext(r.Context())
		if !ok || !uc.IsAdmin {
			server.Error(w, http.StatusForbidden, "forbidden", "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
