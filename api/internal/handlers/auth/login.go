package auth

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	localauth "github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/server"
)

// dummyHash is a valid argon2id PHC string used for constant-time rejection
// when no user is found by email, preventing timing attacks that reveal
// whether an email is registered.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

// loginRequest is the body for POST /v1/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// Login handles POST /v1/auth/login.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		server.Error(w, http.StatusBadRequest, "bad_request", "email and password are required")
		return
	}

	user, err := h.queries.GetUserByEmail(r.Context(), req.Email)
	if err != nil && err != pgx.ErrNoRows {
		slog.ErrorContext(r.Context(), "login: get user by email", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to look up user")
		return
	}

	notFound := err == pgx.ErrNoRows

	var storedHash string
	if !notFound {
		al, err2 := h.queries.GetAuthLocal(r.Context(), user.ID)
		if err2 != nil {
			// User exists but has no local credentials — treat as not found.
			notFound = true
		} else {
			storedHash = al.PasswordHash
		}
	}

	if notFound {
		// Run a dummy verify to consume constant time even when user is absent.
		_, _ = localauth.VerifyPassword(req.Password, dummyHash)
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}

	ok, err := localauth.VerifyPassword(req.Password, storedHash)
	if err != nil {
		slog.ErrorContext(r.Context(), "login: verify password", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to verify credentials")
		return
	}
	if !ok {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid email or password")
		return
	}

	token, err := localauth.IssueLocalJWT(user, user.IsAdmin, h.jwtSecret, h.issuer)
	if err != nil {
		slog.ErrorContext(r.Context(), "login: issue jwt", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to issue token")
		return
	}

	slog.InfoContext(r.Context(), "user logged in", "user_uuid", user.Uuid.String())

	server.JSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"uuid":     user.Uuid.String(),
			"email":    user.Email,
			"is_admin": user.IsAdmin,
		},
	})
}
