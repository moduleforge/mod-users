package auth

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moduleforge/core-api/apiresp"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	db "github.com/moduleforge/mod-users/model/db"
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
	if !server.Decode(w, r, &req) {
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Password == "" {
		var details []apiresp.FieldError
		if req.Email == "" {
			details = append(details, apiresp.FieldError{Field: "email", Code: "users.email_required", Message: "email is required"})
		}
		if req.Password == "" {
			details = append(details, apiresp.FieldError{Field: "password", Code: "users.password_required", Message: "password is required"})
		}
		apiresp.WriteError(w, r, apiresp.InvalidInput(details...))
		return
	}

	ua, err := h.queries.GetUserAccountByEmail(r.Context(), req.Email)
	if err != nil && err != pgx.ErrNoRows {
		slog.ErrorContext(r.Context(), "login: get user account by email", "error", err)
		apiresp.WriteError(w, r, fmt.Errorf("login: get user account by email: %w", err))
		return
	}

	notFound := err == pgx.ErrNoRows

	var storedHash string
	if !notFound {
		al, err2 := h.queries.GetAuthLocal(r.Context(), ua.ID)
		if err2 != nil {
			// User account exists but has no local credentials — treat as not found.
			notFound = true
		} else {
			storedHash = al.PasswordHash
		}
	}

	if notFound {
		// Run a dummy verify to consume constant time even when user is absent.
		_, _ = localauth.VerifyPassword(req.Password, dummyHash)
		// literal-keep: credential-failure 401 whose message the GUI documents as wrong credentials.
		server.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid email or password")
		return
	}

	ok, err := localauth.VerifyPassword(req.Password, storedHash)
	if err != nil {
		slog.ErrorContext(r.Context(), "login: verify password", "error", err)
		apiresp.WriteError(w, r, fmt.Errorf("login: verify password: %w", err))
		return
	}
	if !ok {
		// literal-keep: credential-failure 401 whose message the GUI documents as wrong credentials.
		server.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid email or password")
		return
	}

	token, err := h.issueLocalJWT(ua)
	if err != nil {
		slog.ErrorContext(r.Context(), "login: issue jwt", "error", err)
		apiresp.WriteError(w, r, fmt.Errorf("login: issue jwt: %w", err))
		return
	}

	slog.InfoContext(r.Context(), "user logged in", "user_account_uuid", ua.Uuid.String())

	server.JSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user":  sessionUser(ua),
	})
}

// sessionUser builds the "user" object returned alongside a freshly issued
// token (login and email-code sign-in). It carries email_verified_at with the
// same semantics as GET /v1/self: always present, an RFC 3339 timestamp when
// the email is verified and an explicit JSON null otherwise, so clients read
// the correct verification state without a follow-up /v1/self call.
func sessionUser(ua db.UserAccount) map[string]any {
	var emailVerifiedAt any
	if ua.EmailVerifiedAt != nil {
		emailVerifiedAt = *ua.EmailVerifiedAt
	}
	return map[string]any{
		"uuid":              ua.Uuid.String(),
		"email":             ua.Email.String,
		"email_verified_at": emailVerifiedAt,
	}
}
