package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"golang.org/x/crypto/bcrypt"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	db "github.com/moduleforge/mod-users/model/db"
)

// MaxEmailCodeAttempts bounds the guesses allowed against one email code. The
// step-up handlers in the parent handlers package keep an equal constant
// (maxEmailCodeAttempts); a test there asserts the two stay in sync.
const MaxEmailCodeAttempts = 5

// EmailCodeRequestCooldown is the minimum interval between minted codes for
// the same (user, purpose), measured from the newest code of any state
// (live, exhausted, or consumed). A request inside the window is skipped
// silently (the endpoint still answers 204).
const EmailCodeRequestCooldown = 60 * time.Second

// dummyCodeHash is a bcrypt hash of a throwaway value, generated once at init
// at bcrypt.DefaultCost, so the unknown-email, no-active-code, and exhausted
// paths of EmailCodeVerify spend the same bcrypt time as a wrong guess.
var dummyCodeHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("dummy-email-code-hash"), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Sprintf("auth: generate dummy email code hash: %v", err))
	}
	return h
}()

// dummyCodeCompare runs the constant-time stand-in comparison. It is a
// variable so tests can assert the dummy path is exercised.
var dummyCodeCompare = bcrypt.CompareHashAndPassword

// burnCodeCompare performs the dummy bcrypt comparison and discards the result.
func burnCodeCompare(code string) {
	_ = dummyCodeCompare(dummyCodeHash, []byte(code))
}

// publicEmailCodePurpose reports whether purpose may be used on the
// unauthenticated email-code endpoints. credential_change codes are issued and
// verified only through the authenticated step-up endpoints.
func publicEmailCodePurpose(purpose string) bool {
	return purpose == "login" || purpose == "verify_email"
}

// emailCodeRequestBody is the body for POST /v1/auth/email-code/request.
type emailCodeRequestBody struct {
	Email   string `json:"email"`
	Purpose string `json:"purpose"` // "login" or "verify_email"
}

// emailCodeVerifyBody is the body for POST /v1/auth/email-code/verify.
type emailCodeVerifyBody struct {
	Email   string `json:"email"`
	Code    string `json:"code"`
	Purpose string `json:"purpose"`
}

// EmailCodeRequest handles POST /v1/auth/email-code/request.
// Always returns 204 within ~200ms to avoid email enumeration.
func (h *Handler) EmailCodeRequest(w http.ResponseWriter, r *http.Request) {
	var req emailCodeRequestBody
	if !server.Decode(w, r, &req) {
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Purpose == "" {
		req.Purpose = "login"
	}

	// Perform the real work in a goroutine on a context detached from request
	// cancellation, and answer at the 200ms floor regardless of whether the
	// work has finished. Waiting for the work would make latency reveal whether
	// the email exists or is inside its cooldown; the detached context lets the
	// email still be sent after the response.
	h.dispatchEmailCode(r, req.Email, req.Purpose)

	timer := time.NewTimer(200 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// emailCodeSendTimeout bounds one detached background send (lookup, hash,
// insert, and email delivery).
const emailCodeSendTimeout = 20 * time.Second

// defaultEmailCodeConcurrency bounds concurrent detached sends per Handler.
const defaultEmailCodeConcurrency = 16

// emailCodeSlotChan returns the per-Handler semaphore, creating it on first
// use so zero-value Handlers (tests) work. emailCodeConcurrency <= 0 selects
// the default.
func (h *Handler) emailCodeSlotChan() chan struct{} {
	h.emailCodeSlotsOnce.Do(func() {
		n := h.emailCodeConcurrency
		if n <= 0 {
			n = defaultEmailCodeConcurrency
		}
		h.emailCodeSlots = make(chan struct{}, n)
	})
	return h.emailCodeSlots
}

// dispatchEmailCode starts the detached background send when a slot is free,
// and otherwise drops it (logged) so the caller's response is unaffected.
func (h *Handler) dispatchEmailCode(r *http.Request, email, purpose string) {
	slots := h.emailCodeSlotChan()
	select {
	case slots <- struct{}{}:
	default:
		slog.WarnContext(r.Context(), "email_code: background send capacity saturated, dropping request")
		return
	}
	parent := context.WithoutCancel(r.Context())
	go func() {
		defer func() { <-slots }()
		runBoundedEmailWork(parent, emailCodeSendTimeout, func(ctx context.Context) {
			h.sendEmailCode(r.WithContext(ctx), email, purpose)
		})
	}()
}

// runBoundedEmailWork runs work under a deadline and recovers from a panic,
// logging only the panic type (never its value, which could carry the code or
// address) so the process survives.
func runBoundedEmailWork(parent context.Context, timeout time.Duration, work func(context.Context)) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	defer func() {
		if rec := recover(); rec != nil {
			slog.ErrorContext(ctx, "email_code: recovered panic in background send", "panic_type", fmt.Sprintf("%T", rec))
		}
	}()
	work(ctx)
}

func (h *Handler) sendEmailCode(r *http.Request, email, purpose string) {
	ctx := r.Context()

	// Refuse silently (the caller still answers 204) so a disallowed purpose
	// is indistinguishable from an unknown email and never mints a code.
	if !publicEmailCodePurpose(purpose) {
		return
	}

	ua, err := h.queries.GetUserAccountByEmail(ctx, email)
	if err == pgx.ErrNoRows {
		return // anti-enumeration: silently skip
	}
	if err != nil {
		slog.ErrorContext(ctx, "email_code: get user account", "error", err)
		return
	}

	// Cheap indexed pre-check: skip the code generation and bcrypt hash when the
	// cooldown already forbids minting. Advisory only; a pre-check error falls
	// through to CreateEmailCodeWithCooldown, the authoritative atomic guard.
	recent, err := h.queries.HasRecentEmailCode(ctx, db.HasRecentEmailCodeParams{
		UserAccountID: ua.ID,
		Purpose:       purpose,
		CooldownSecs:  int32(EmailCodeRequestCooldown / time.Second),
	})
	if err != nil {
		slog.ErrorContext(ctx, "email_code: cooldown pre-check", "error", err)
	} else if recent {
		slog.DebugContext(ctx, "email_code: request within cooldown, skipping", "user_account_id", ua.ID)
		return
	}

	// Generate 6-digit code.
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		slog.ErrorContext(ctx, "email_code: generate code", "error", err)
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())

	hash, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
	if err != nil {
		slog.ErrorContext(ctx, "email_code: hash code", "error", err)
		return
	}

	// Atomic cooldown + insert: no row is inserted when a code of any state
	// (live, exhausted, or consumed) was created for this (user, purpose)
	// within the cooldown, so exhausting a code cannot reset the guess budget
	// by minting a fresh one early. No row means no email.
	expiresAt := pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true}
	_, err = h.queries.CreateEmailCodeWithCooldown(ctx, db.CreateEmailCodeWithCooldownParams{
		UserAccountID: ua.ID,
		CodeHash:      string(hash),
		Purpose:       purpose,
		ExpiresAt:     expiresAt,
		CooldownSecs:  int32(EmailCodeRequestCooldown / time.Second),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		slog.DebugContext(ctx, "email_code: request within cooldown, skipping", "user_account_id", ua.ID)
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "email_code: insert", "error", err)
		return
	}

	subject := "Your login code"
	if purpose == "verify_email" {
		subject = "Verify your email address"
	}
	body := fmt.Sprintf("Your verification code is: %s\n\nThis code expires in 5 minutes.", code)

	if !ua.Email.Valid || ua.Email.String == "" {
		slog.WarnContext(ctx, "email_code: account has no email address", "user_account_id", ua.ID)
		return
	}
	if err := h.sender.Send(ctx, ua.Email.String, subject, body); err != nil {
		slog.ErrorContext(ctx, "email_code: send email", "error", err)
	}
}

// EmailCodeVerify handles POST /v1/auth/email-code/verify.
func (h *Handler) EmailCodeVerify(w http.ResponseWriter, r *http.Request) {
	var req emailCodeVerifyBody
	if !server.Decode(w, r, &req) {
		return
	}

	req.Email = strings.TrimSpace(strings.ToLower(req.Email))
	if req.Email == "" || req.Code == "" {
		apiresp.WriteError(w, r, apiresp.InvalidInput(emailCodeRequiredDetails(req.Email, req.Code)...))
		return
	}
	if req.Purpose == "" {
		req.Purpose = "login"
	}
	if !publicEmailCodePurpose(req.Purpose) {
		server.Error(w, http.StatusBadRequest, "invalid_input", "invalid purpose")
		return
	}

	ua, err := h.queries.GetUserAccountByEmail(r.Context(), req.Email)
	if err == pgx.ErrNoRows {
		burnCodeCompare(req.Code) // constant time: do not reveal unknown email
		// literal-keep: credential-failure 401 whose message the GUI tests assert.
		server.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired code")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "email_code_verify: get user account", "error", err)
		apiresp.WriteError(w, r, fmt.Errorf("email_code_verify: get user account: %w", err))
		return
	}

	emailCode, err := h.queries.GetActiveEmailCode(r.Context(), db.GetActiveEmailCodeParams{
		UserAccountID: ua.ID,
		Purpose:       req.Purpose,
	})
	if err == pgx.ErrNoRows {
		burnCodeCompare(req.Code) // constant time: do not reveal missing code
		// literal-keep: credential-failure 401 whose message the GUI tests assert.
		server.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired code")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "email_code_verify: get code", "error", err)
		apiresp.WriteError(w, r, fmt.Errorf("email_code_verify: get code: %w", err))
		return
	}

	// Claim an attempt atomically before comparing, so concurrent guesses can
	// never exceed MaxEmailCodeAttempts. No row means the code is exhausted or
	// was consumed concurrently; answer exactly like a wrong code.
	claimed, err := h.queries.ClaimEmailCodeAttempt(r.Context(), db.ClaimEmailCodeAttemptParams{
		ID:          emailCode.ID,
		MaxAttempts: MaxEmailCodeAttempts,
	})
	if err == pgx.ErrNoRows {
		burnCodeCompare(req.Code)
		// literal-keep: credential-failure 401 whose message the GUI tests assert.
		server.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired code")
		return
	}
	if err != nil {
		slog.ErrorContext(r.Context(), "email_code_verify: claim attempt", "error", err)
		server.Error(w, http.StatusInternalServerError, "internal_error", "failed to look up code")
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(claimed.CodeHash), []byte(req.Code)); err != nil {
		// literal-keep: credential-failure 401 whose message the GUI tests assert.
		server.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired code")
		return
	}

	// Grant success only to the request that actually consumes the code. When
	// the claim itself consumed the code (the final attempt), this claimer is
	// the sole consumer; otherwise the consume must affect a row, or a
	// concurrent request already used the code.
	// Fail-closed at the exhaustion boundary: a correct guess that holds an
	// earlier attempt (ConsumedAt == nil) is refused (n == 0) when a concurrent
	// final-attempt claim consumed the code first, even though its guess was
	// right. Single-use consumption takes precedence over that request.
	if claimed.ConsumedAt == nil {
		n, err := h.queries.ConsumeEmailCode(r.Context(), emailCode.ID)
		if err != nil {
			slog.ErrorContext(r.Context(), "email_code_verify: consume code", "error", err)
			server.Error(w, http.StatusInternalServerError, "internal_error", "failed to look up code")
			return
		}
		if n == 0 {
			server.Error(w, http.StatusUnauthorized, "unauthenticated", "invalid or expired code")
			return
		}
	}

	if req.Purpose == "login" {
		token, err := h.issueLocalJWT(ua)
		if err != nil {
			slog.ErrorContext(r.Context(), "email_code_verify: issue jwt", "error", err)
			apiresp.WriteError(w, r, fmt.Errorf("email_code_verify: issue jwt: %w", err))
			return
		}
		server.JSON(w, http.StatusOK, map[string]any{
			"token": token,
			"user":  sessionUser(ua),
		})
		return
	}

	// verify_email purpose: mark email as verified.
	now := time.Now()
	if err := h.queries.UpdateUserAccount(r.Context(), db.UpdateUserAccountParams{
		ID:              ua.ID,
		Email:           ua.Email, // pgtype.Text — pass through unchanged
		EmailVerifiedAt: &now,
	}); err != nil {
		slog.ErrorContext(r.Context(), "email_code_verify: mark verified", "error", err)
		apiresp.WriteError(w, r, fmt.Errorf("email_code_verify: mark verified: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// emailCodeRequiredDetails returns one detail per missing field of an
// email-code verify request.
func emailCodeRequiredDetails(email, code string) []apiresp.FieldError {
	var details []apiresp.FieldError
	if email == "" {
		details = append(details, apiresp.FieldError{Field: "email", Code: "users.email_required", Message: "email is required"})
	}
	if code == "" {
		details = append(details, apiresp.FieldError{Field: "code", Code: "users.code_required", Message: "code is required"})
	}
	return details
}
