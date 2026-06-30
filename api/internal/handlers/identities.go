package handlers

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"

	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/txhelper"
	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/server"
	usersservice "github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
)

// IdentitiesHandler serves identity-management endpoints under /v1/self.
//
// All endpoints are gated by RequireAuth + RequireVerifiedEmail in main.go.
// The handler never performs authorization checks itself — the authentication
// middleware guarantees the caller owns the UserContext in the request context.
type IdentitiesHandler struct {
	pool      *pgxpool.Pool
	queries   *db.Queries
	oauth     *localauth.OAuth
	obs       *observer.ObserverGroup
	sender    emailSender
	jwtSecret string
	// consumed is the process-local single-use cache for step-up token JTIs.
	// Cross-process consistency is not required: tokens are short-lived (5 min)
	// and tied to a single session. A restart clears the cache, leaving a small
	// replay window — tolerable per the spec.
	consumed *sync.Map
	// requireStepUp controls whether credential-mutating endpoints require a
	// valid X-Step-Up-Token header. Reflects cfg.Auth.RequireStepUpForCredentialChange.
	stepUpRequired bool
}

// emailSender is the minimal interface for sending email. It matches
// handlers/auth.Sender to avoid a direct package dependency.
type emailSender interface {
	Send(ctx context.Context, to, subject, textBody string) error
}

// IdentitiesHandlerDeps carries optional dependencies for step-up challenge
// support. When Sender and JWTSecret are empty/nil, step-up endpoints are
// mounted but will return errors; in practice they are only mounted when
// step-up is configured.
type IdentitiesHandlerDeps struct {
	Pool           *pgxpool.Pool
	Queries        *db.Queries
	OAuth          *localauth.OAuth
	Obs            *observer.ObserverGroup
	Sender         emailSender
	JWTSecret      string
	Consumed       *sync.Map
	StepUpRequired bool
}

// NewIdentitiesHandler constructs an IdentitiesHandler.
func NewIdentitiesHandler(pool *pgxpool.Pool, queries *db.Queries, oauth *localauth.OAuth, obs *observer.ObserverGroup) *IdentitiesHandler {
	return &IdentitiesHandler{
		pool:     pool,
		queries:  queries,
		oauth:    oauth,
		obs:      obs,
		consumed: &sync.Map{},
	}
}

// NewIdentitiesHandlerWithDeps constructs an IdentitiesHandler with step-up
// dependencies wired in. Used from main.go when step-up is configured.
func NewIdentitiesHandlerWithDeps(deps IdentitiesHandlerDeps) *IdentitiesHandler {
	consumed := deps.Consumed
	if consumed == nil {
		consumed = &sync.Map{}
	}
	return &IdentitiesHandler{
		pool:           deps.Pool,
		queries:        deps.Queries,
		oauth:          deps.OAuth,
		obs:            deps.Obs,
		sender:         deps.Sender,
		jwtSecret:      deps.JWTSecret,
		consumed:       consumed,
		stepUpRequired: deps.StepUpRequired,
	}
}

// ---------------------------------------------------------------------------
// Response DTOs (no internal IDs)
// ---------------------------------------------------------------------------

// localIdentityDTO describes whether a local password credential is set.
type localIdentityDTO struct {
	Set       bool       `json:"set"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

// oidcIdentityDTO is the public view of one auth_oidc_identities row.
type oidcIdentityDTO struct {
	UUID       string     `json:"uuid"`
	Issuer     string     `json:"issuer"`
	Subject    string     `json:"subject"`
	Email      string     `json:"email,omitempty"`
	LinkedAt   *time.Time `json:"linked_at,omitempty"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
}

// identitiesResponse is the body for GET /v1/self/identities.
type identitiesResponse struct {
	Local *localIdentityDTO `json:"local"`
	OIDC  []oidcIdentityDTO `json:"oidc"`
}

// ---------------------------------------------------------------------------
// GET /v1/self/identities
// ---------------------------------------------------------------------------

// List returns the caller's current identities: one optional local credential
// and zero-or-more OIDC identities.
func (h *IdentitiesHandler) List(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())

	// auth_local
	var localDTO *localIdentityDTO
	al, err := h.queries.GetAuthLocal(r.Context(), uc.UserAccountID)
	if err == nil {
		ts := al.PasswordUpdatedAt.Time
		localDTO = &localIdentityDTO{Set: true, UpdatedAt: &ts}
	} else if errors.Is(err, pgx.ErrNoRows) {
		localDTO = &localIdentityDTO{Set: false}
	} else {
		slog.ErrorContext(r.Context(), "identities.List: get auth_local", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load credentials")
		return
	}

	// OIDC identities
	rows, err := h.queries.ListOIDCIdentitiesByUserAccount(r.Context(), uc.UserAccountID)
	if err != nil {
		slog.ErrorContext(r.Context(), "identities.List: list oidc identities", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load identities")
		return
	}

	oidcDTOs := make([]oidcIdentityDTO, 0, len(rows))
	for _, row := range rows {
		dto := oidcIdentityDTO{
			UUID:    row.Uuid.String(),
			Issuer:  row.Issuer,
			Subject: row.Subject,
		}
		if row.Email.Valid {
			dto.Email = row.Email.String
		}
		if row.LinkedAt.Valid {
			t := row.LinkedAt.Time
			dto.LinkedAt = &t
		}
		if row.LastSeenAt.Valid {
			t := row.LastSeenAt.Time
			dto.LastSeenAt = &t
		}
		oidcDTOs = append(oidcDTOs, dto)
	}

	server.JSON(w, http.StatusOK, identitiesResponse{
		Local: localDTO,
		OIDC:  oidcDTOs,
	})
}

// ---------------------------------------------------------------------------
// POST /v1/self/identities/oidc/{provider}/start
// ---------------------------------------------------------------------------

// startLinkResponse is the body returned by the link-mode start endpoint.
type startLinkResponse struct {
	AuthorizeURL string `json:"authorize_url"`
	State        string `json:"state"`
}

// StartLink mints a link-mode OIDC authorize URL for the caller. The returned
// state token, when presented at /auth/oidc/{provider}/callback, will insert
// a new identity row instead of resolving/creating a user account.
func (h *IdentitiesHandler) StartLink(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())

	if err := h.requireStepUp(r, uc.UserAccountID); err != nil {
		writeStepUpRequired(w)
		return
	}

	providerID := normalizeIdentityProviderID(r)

	returnPath := r.URL.Query().Get("return")

	authURL, state, err := h.oauth.LinkAuthorizeURL(providerID, uc.UserUUID, returnPath)
	if err != nil {
		if errors.Is(err, localauth.ErrUnknownProvider) || errors.Is(err, localauth.ErrProviderNotAvailable) {
			server.Error(w, http.StatusNotFound, "not_found", "unknown provider")
			return
		}
		slog.WarnContext(r.Context(), "identities.StartLink: bad request", "error", err, "provider", providerID)
		server.Error(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}

	// Set the same oidc_state cookie the existing /start uses so the callback
	// can validate the state parameter.
	http.SetCookie(w, newOIDCStateCookie(state, 300, r))
	server.JSON(w, http.StatusOK, startLinkResponse{AuthorizeURL: authURL, State: state})
}

// ---------------------------------------------------------------------------
// DELETE /v1/self/identities/{identity_uuid}
// ---------------------------------------------------------------------------

// Unlink removes an OIDC identity from the caller's account. Rejected with 409
// if it would leave the account with no remaining auth method.
func (h *IdentitiesHandler) Unlink(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())

	if err := h.requireStepUp(r, uc.UserAccountID); err != nil {
		writeStepUpRequired(w)
		return
	}
	stepUpUsed := h.stepUpRequired

	rawUUID := chi.URLParam(r, "identity_uuid")
	identUUID, err := uuid.Parse(rawUUID)
	if err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid identity UUID")
		return
	}

	entityID := uc.EntityID

	txErr := txhelper.RunSerializable(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.queries.WithTx(tx)

		// Verify ownership and get the identity row. DeleteOIDCIdentityByUUID
		// enforces ownership at the query layer (AND user_account_id = $2), but
		// we need the row data first for the safety check and observer.
		oidcCount, hasLocal, err := usersservice.IdentityCounts(ctx, qtx, uc.UserAccountID)
		if err != nil {
			return fmt.Errorf("identities.Unlink: counts: %w", err)
		}

		// Post-delete totals: subtract the one we're about to remove.
		remaining := (oidcCount - 1)
		var hasLocalInt int64
		if hasLocal {
			hasLocalInt = 1
		}
		if remaining+hasLocalInt == 0 {
			return errLastIdentity
		}

		rowsDeleted, err := qtx.DeleteOIDCIdentityByUUID(ctx, db.DeleteOIDCIdentityByUUIDParams{
			Uuid:          identUUID,
			UserAccountID: uc.UserAccountID,
		})
		if err != nil {
			return fmt.Errorf("identities.Unlink: delete: %w", err)
		}
		if rowsDeleted == 0 {
			return errIdentityNotFound
		}

		after := map[string]any{
			"uuid":    identUUID.String(),
			"step_up": stepUpUsed,
		}
		return h.safeObserve(ctx, tx, "delete", "auth_oidc_identity", &entityID, nil, after)
	})

	if txErr != nil {
		if errors.Is(txErr, errLastIdentity) {
			writeLastIdentityError(w)
			return
		}
		if errors.Is(txErr, errIdentityNotFound) {
			server.Error(w, http.StatusNotFound, "identity_not_found", "identity not found")
			return
		}
		slog.ErrorContext(r.Context(), "identities.Unlink: transaction", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to unlink identity")
		return
	}

	h.safeObserveAfterCommit(r.Context(), "delete", "auth_oidc_identity", &entityID, map[string]any{
		"uuid":    identUUID.String(),
		"step_up": stepUpUsed,
	})

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// POST /v1/self/credential/password
// ---------------------------------------------------------------------------

// setPasswordRequest is the body for POST /v1/self/credential/password.
type setPasswordRequest struct {
	CurrentPassword *string `json:"current_password"`
	NewPassword     string  `json:"new_password"`
}

// SetPassword sets or changes the caller's local password.
// If the account already has an auth_local row, current_password is required
// and verified before proceeding. If no row exists, this is a first-time attach
// and current_password is ignored.
func (h *IdentitiesHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())

	if err := h.requireStepUp(r, uc.UserAccountID); err != nil {
		writeStepUpRequired(w)
		return
	}
	stepUpUsed := h.stepUpRequired

	var req setPasswordRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}

	if req.NewPassword == "" {
		server.Error(w, http.StatusBadRequest, "bad_request", "new_password is required")
		return
	}
	if len(req.NewPassword) < 12 {
		server.Error(w, http.StatusBadRequest, "validation_error", "password must be at least 12 characters")
		return
	}

	entityID := uc.EntityID

	// Check for existing auth_local row.
	existing, err := h.queries.GetAuthLocal(r.Context(), uc.UserAccountID)
	hasExisting := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(r.Context(), "identities.SetPassword: get auth_local", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to load credentials")
		return
	}

	operation := "create"
	if hasExisting {
		operation = "update"
		// Require and verify current_password.
		if req.CurrentPassword == nil || *req.CurrentPassword == "" {
			server.Error(w, http.StatusUnauthorized, "bad_credentials", "current_password is required to change an existing password")
			return
		}
		ok, verifyErr := localauth.VerifyPassword(*req.CurrentPassword, existing.PasswordHash)
		if verifyErr != nil {
			slog.ErrorContext(r.Context(), "identities.SetPassword: verify password", "error", verifyErr)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to verify current password")
			return
		}
		if !ok {
			server.Error(w, http.StatusUnauthorized, "bad_credentials", "current password is incorrect")
			return
		}
	}

	hash, err := localauth.HashPassword(req.NewPassword)
	if err != nil {
		slog.ErrorContext(r.Context(), "identities.SetPassword: hash password", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to process password")
		return
	}

	txErr := txhelper.Run(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.queries.WithTx(tx)
		if err := qtx.UpsertAuthLocal(ctx, db.UpsertAuthLocalParams{
			UserAccountID: uc.UserAccountID,
			PasswordHash:  hash,
		}); err != nil {
			return fmt.Errorf("identities.SetPassword: upsert: %w", err)
		}
		after := map[string]any{"operation": operation, "step_up": stepUpUsed}
		return h.safeObserve(ctx, tx, operation, "auth_local", &entityID, nil, after)
	})
	if txErr != nil {
		slog.ErrorContext(r.Context(), "identities.SetPassword: transaction", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to save password")
		return
	}

	h.safeObserveAfterCommit(r.Context(), operation, "auth_local", &entityID, map[string]any{"operation": operation, "step_up": stepUpUsed})

	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// DELETE /v1/self/credential/password
// ---------------------------------------------------------------------------

// RemovePassword removes the caller's local password credential. Rejected with
// 409 if it would leave the account with no remaining auth method.
func (h *IdentitiesHandler) RemovePassword(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())

	if err := h.requireStepUp(r, uc.UserAccountID); err != nil {
		writeStepUpRequired(w)
		return
	}
	stepUpUsed := h.stepUpRequired
	entityID := uc.EntityID

	txErr := txhelper.RunSerializable(r.Context(), h.pool, func(ctx context.Context, tx pgx.Tx) error {
		qtx := h.queries.WithTx(tx)

		oidcCount, hasLocal, err := usersservice.IdentityCounts(ctx, qtx, uc.UserAccountID)
		if err != nil {
			return fmt.Errorf("identities.RemovePassword: counts: %w", err)
		}
		if !hasLocal {
			// Nothing to delete — treat as success (idempotent).
			return nil
		}

		// Post-delete totals: remove the local credential.
		if oidcCount == 0 {
			return errLastIdentity
		}

		if err := qtx.DeleteAuthLocal(ctx, uc.UserAccountID); err != nil {
			return fmt.Errorf("identities.RemovePassword: delete: %w", err)
		}
		return h.safeObserve(ctx, tx, "delete", "auth_local", &entityID, nil, map[string]any{"step_up": stepUpUsed})
	})

	if txErr != nil {
		if errors.Is(txErr, errLastIdentity) {
			writeLastIdentityError(w)
			return
		}
		slog.ErrorContext(r.Context(), "identities.RemovePassword: transaction", "error", txErr)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to remove password")
		return
	}

	h.safeObserveAfterCommit(r.Context(), "delete", "auth_local", &entityID, map[string]any{"step_up": stepUpUsed})
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------------
// POST /v1/self/credential/step-up
// ---------------------------------------------------------------------------

// StepUpRequest enqueues an email code with purpose='credential_change' for the
// caller. It mirrors the anti-enumeration pattern of EmailCodeRequest: the real
// work runs in a goroutine while the handler always waits ~200 ms before
// responding. Returns 204.
func (h *IdentitiesHandler) StepUpRequest(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())

	done := make(chan struct{}, 1)
	go func() {
		defer func() { done <- struct{}{} }()
		h.sendStepUpCode(r, uc)
	}()

	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		<-done
	case <-done:
		<-timer.C
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *IdentitiesHandler) sendStepUpCode(r *http.Request, uc *localauth.UserContext) {
	ctx := r.Context()

	if h.queries == nil || h.sender == nil {
		slog.ErrorContext(ctx, "stepup_request: handler not fully configured (missing queries or sender)")
		return
	}

	// Guard: anonymous accounts have no email address. RequireVerifiedEmail
	// already blocks them from reaching this endpoint, but we defend in depth
	// to avoid creating a dangling code row or sending to an empty address if
	// the middleware chain is ever mis-ordered.
	if uc.Email == "" {
		slog.WarnContext(ctx, "stepup_request: account has no email address (anonymous account)", "user_account_id", uc.UserAccountID)
		return
	}

	code, hash, err := generateEmailCode()
	if err != nil {
		slog.ErrorContext(ctx, "stepup_request: generate code", "error", err)
		return
	}

	expiresAt := pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true}
	_, err = h.queries.CreateEmailCode(ctx, db.CreateEmailCodeParams{
		UserAccountID: uc.UserAccountID,
		CodeHash:      hash,
		Purpose:       "credential_change",
		ExpiresAt:     expiresAt,
	})
	if err != nil {
		slog.ErrorContext(ctx, "stepup_request: insert email code", "error", err)
		return
	}

	subject := "Credential change verification code"
	body := fmt.Sprintf("Your verification code is: %s\n\nThis code expires in 5 minutes.", code)
	if err := h.sender.Send(ctx, uc.Email, subject, body); err != nil {
		slog.ErrorContext(ctx, "stepup_request: send email", "error", err)
	}
}

// ---------------------------------------------------------------------------
// POST /v1/self/credential/step-up/verify
// ---------------------------------------------------------------------------

// stepUpVerifyRequest is the body for POST /v1/self/credential/step-up/verify.
type stepUpVerifyRequest struct {
	Code string `json:"code"`
}

// stepUpVerifyResponse is the body returned by the step-up verify endpoint.
type stepUpVerifyResponse struct {
	StepUpToken string `json:"step_up_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// StepUpVerify consumes an email code with purpose='credential_change' and
// returns a short-lived step-up token. Wrong or expired code → 401.
func (h *IdentitiesHandler) StepUpVerify(w http.ResponseWriter, r *http.Request) {
	uc := localauth.MustFromContext(r.Context())

	var req stepUpVerifyRequest
	if err := server.Decode(r, &req); err != nil {
		server.Error(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if req.Code == "" {
		server.Error(w, http.StatusBadRequest, "bad_request", "code is required")
		return
	}

	emailCode, err := h.queries.GetActiveEmailCode(r.Context(), db.GetActiveEmailCodeParams{
		UserAccountID: uc.UserAccountID,
		Purpose:       "credential_change",
	})
	if err == pgx.ErrNoRows {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired code")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "stepup_verify: get code", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to look up code")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(emailCode.CodeHash), []byte(req.Code)); err != nil {
		server.Error(w, http.StatusUnauthorized, "unauthorized", "invalid or expired code")
		return
	}

	// Consume the code before issuing the token.
	if err := h.queries.ConsumeEmailCode(r.Context(), emailCode.ID); err != nil {
		slog.ErrorContext(r.Context(), "stepup_verify: consume code", "error", err)
	}

	token, _, _, err := localauth.IssueStepUpToken([]byte(h.jwtSecret), uc.UserAccountID, localauth.StepUpTTL)
	if err != nil {
		slog.ErrorContext(r.Context(), "stepup_verify: issue token", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to issue step-up token")
		return
	}

	server.JSON(w, http.StatusOK, stepUpVerifyResponse{
		StepUpToken: token,
		ExpiresIn:   int(localauth.StepUpTTL.Seconds()),
	})
}

// ---------------------------------------------------------------------------
// Step-up gate helper
// ---------------------------------------------------------------------------

// requireStepUp enforces the step-up challenge when h.stepUpRequired is true.
// If the flag is false, it returns nil immediately (flag-off path). Otherwise
// it reads the X-Step-Up-Token header, verifies it, and returns
// localauth.ErrStepUpRequired on any failure.
func (h *IdentitiesHandler) requireStepUp(r *http.Request, userAccountID int64) error {
	if !h.stepUpRequired {
		return nil
	}
	token := r.Header.Get("X-Step-Up-Token")
	if token == "" {
		return localauth.ErrStepUpRequired
	}
	return localauth.VerifyStepUpToken([]byte(h.jwtSecret), token, userAccountID, h.consumed)
}

// writeStepUpRequired writes the 409 step_up_required response body. The wire
// format is stable — the Phase 5 GUI keys off the "error" field value.
func writeStepUpRequired(w http.ResponseWriter) {
	server.JSON(w, http.StatusConflict, map[string]any{
		"error":          "step_up_required",
		"challenge_path": "/v1/self/credential/step-up",
	})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// errLastIdentity is a sentinel returned inside transactions to signal the
// 409 last_identity safety check failure without leaking implementation details.
var errLastIdentity = errors.New("identities: last identity cannot be removed")

// errIdentityNotFound is a sentinel returned inside transactions when the
// identity UUID does not belong to the caller's account.
var errIdentityNotFound = errors.New("identities: identity not found")

// writeLastIdentityError writes the 409 response mandated by task 4.4.
func writeLastIdentityError(w http.ResponseWriter) {
	server.JSON(w, http.StatusConflict, map[string]any{
		"error":   "last_identity",
		"message": "You can't remove your last sign-in method. Add another first.",
	})
}

// safeObserve calls obs.Observe only when h.obs is non-nil.
func (h *IdentitiesHandler) safeObserve(ctx context.Context, tx pgx.Tx, op, resource string, targetEntityID *int64, before, after any) error {
	if h.obs == nil {
		return nil
	}
	return h.obs.Observe(ctx, tx, op, resource, targetEntityID, before, after)
}

// safeObserveAfterCommit calls obs.ObserveAfterCommit only when h.obs is non-nil.
func (h *IdentitiesHandler) safeObserveAfterCommit(ctx context.Context, op, resource string, targetEntityID *int64, after any) {
	if h.obs == nil {
		return
	}
	h.obs.ObserveAfterCommit(ctx, op, resource, targetEntityID, after)
}

// generateEmailCode produces a random 6-digit code and its bcrypt hash.
func generateEmailCode() (code, hash string, err error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", "", fmt.Errorf("generate email code: rand: %w", err)
	}
	code = fmt.Sprintf("%06d", n.Int64())
	hashed, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		return "", "", fmt.Errorf("generate email code: hash: %w", err)
	}
	return code, string(hashed), nil
}

// normalizeIdentityProviderID lowercases the {provider} URL param from chi.
func normalizeIdentityProviderID(r *http.Request) string {
	return strings.ToLower(chi.URLParam(r, "provider"))
}

// newOIDCStateCookie builds the oidc_state cookie mirroring the pattern in
// handlers/auth/oidc.go. Keeping a local copy avoids introducing a package
// dependency between handlers and handlers/auth.
func newOIDCStateCookie(value string, maxAge int, r *http.Request) *http.Cookie {
	secure := r.TLS != nil
	if r.TLS == nil {
		if xf := r.Header.Get("X-Forwarded-Proto"); xf == "https" {
			secure = true
		}
	}
	return &http.Cookie{
		Name:     "oidc_state",
		Value:    value,
		Path:     "/v1/auth/oidc/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}
