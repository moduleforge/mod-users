package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	usersdb "github.com/moduleforge/mod-users/model/db"
)

// passwordResetRequestBody is the body for POST /v1/auth/password-reset/request.
type passwordResetRequestBody struct {
	Email string `json:"email"`
}

// passwordResetConfirmBody is the body for POST /v1/auth/password-reset/confirm.
type passwordResetConfirmBody struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

// errInvalidResetToken is the refusal of an unknown, expired or spent reset
// token.
var errInvalidResetToken = &svc.AuthError{
	Status: http.StatusUnauthorized, Code: svc.CodeInvalidCredentials, Message: "invalid or expired reset token",
}

// PasswordResetRequest handles POST /v1/auth/password-reset/request. It
// always answers 204 after the same floor latency; for a registered email the
// reset token (password_reset challenge, one hour, subject to the challenge
// cooldown) is minted and the link emailed in the background.
func (h *Handler) PasswordResetRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	var req passwordResetRequestBody
	if !server.Decode(w, r, &req) {
		return
	}
	if canon := svc.CanonicalizeEmail(req.Email); canon != "" {
		h.dispatch(r, "password_reset", func(ctx context.Context) {
			if err := h.sendPasswordReset(ctx, canon); err != nil {
				slog.ErrorContext(ctx, "password_reset: request", "error", err)
			}
		})
	}
	answerAfterFloor(w, r, start)
}

// sendPasswordReset mints and emails a reset link for the email identifier.
// An unknown email, a guest and a request inside the cooldown are skipped.
func (h *Handler) sendPasswordReset(ctx context.Context, canon string) error {
	ident, err := h.q.GetIdentifierByKindValue(ctx, usersdb.GetIdentifierByKindValueParams{
		Kind: svc.IdentifierKindEmail, ValueCanon: canon,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look up identifier: %w", err)
	}
	user, err := h.q.GetUserByEntityID(ctx, ident.UserID)
	if err != nil {
		return fmt.Errorf("load user: %w", err)
	}
	if user.IsGuest {
		return nil
	}

	var issued svc.IssuedChallenge
	err = h.inTx(ctx, func(tx pgx.Tx) error {
		var err error
		issued, err = h.challenges.IssueTokenInTx(ctx, tx, svc.IssueTokenInput{
			Kind:    svc.ChallengePasswordReset,
			Subject: svc.Subject{PrincipalID: ident.UserID, IdentifierID: ident.ID},
		})
		return err
	})
	if errors.Is(err, svc.ErrChallengeCooldown) {
		return nil
	}
	if err != nil {
		return err
	}
	if h.sender == nil {
		return errors.New("no email sender configured")
	}
	resetURL := strings.TrimRight(h.guiBase, "/") + "/reset-password?token=" + url.QueryEscape(issued.Code)
	body := "Click the link below to reset your password:\n\n" + resetURL +
		"\n\nThis link expires in 60 minutes. If you did not request a password reset, ignore this email."
	if err := h.sender.Send(ctx, ident.ValueCanon, "Reset your password", body); err != nil {
		return fmt.Errorf("send email: %w", err)
	}
	return nil
}

// PasswordResetConfirm handles POST /v1/auth/password-reset/confirm. In one
// transaction it consumes the reset token (single use), supersedes the
// password bound to the token's email identifier (revoked with reason
// superseded, which also revokes every session holding it) with a new one
// that keeps its label, landing profile and confinement, and consumes the
// identifier's other open reset tokens. It answers 204 and does not sign in.
// A bad token is 401 users.invalid_credentials.
func (h *Handler) PasswordResetConfirm(w http.ResponseWriter, r *http.Request) {
	var req passwordResetConfirmBody
	if !server.Decode(w, r, &req) {
		return
	}
	if req.Token == "" {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "token", Code: "users.token_required", Message: "token is required",
		}))
		return
	}
	if err := svc.ValidatePassword(req.Password); err != nil {
		writeError(w, r, err)
		return
	}

	err := h.inTx(r.Context(), func(tx pgx.Tx) error {
		ch, err := h.challenges.ConsumeTokenInTx(r.Context(), tx, svc.ChallengePasswordReset, req.Token)
		if err != nil {
			if errors.Is(err, svc.ErrInvalidCode) {
				return errInvalidResetToken
			}
			return err
		}
		if ch.PrincipalID == 0 || ch.IdentifierID == 0 {
			return errInvalidResetToken
		}
		return h.replacePassword(r.Context(), tx, ch.PrincipalID, ch.IdentifierID, req.Password)
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// replacePassword supersedes the identifier's active password (if any) with
// a new password credential bound to the identifier. Observations (`revoke`
// and `create` `credential`, `revoke` `session`) run as the principal's
// personal profile, else its oldest non-ended profile (API surface section
// 10.1); a principal without any non-ended profile cannot reset.
func (h *Handler) replacePassword(ctx context.Context, tx pgx.Tx, principalID, identifierID int64, password string) error {
	actCtx, ok, err := svc.UnauthenticatedActingContext(ctx, tx, principalID)
	if err != nil {
		return err
	}
	if !ok {
		return errInvalidResetToken
	}
	q := usersdb.New(tx)
	passwords, err := q.ListActivePasswordsByPrincipal(ctx, principalID)
	if err != nil {
		return fmt.Errorf("list passwords: %w", err)
	}
	in := svc.CreatePasswordInput{PrincipalID: principalID, IdentifierID: &identifierID, Password: password}
	var oldIDs []int64
	for _, p := range passwords {
		if !p.IdentifierID.Valid || p.IdentifierID.Int64 != identifierID {
			continue
		}
		in.Label = p.Credential.Label
		if p.Credential.DefaultProfileID.Valid {
			id := p.Credential.DefaultProfileID.Int64
			in.DefaultProfileID = &id
		}
		if p.Credential.ConfinedToPartyID.Valid {
			id := p.Credential.ConfinedToPartyID.Int64
			in.ConfinedToPartyID = &id
		}
		oldIDs = append(oldIDs, p.Credential.ID)
	}
	if len(oldIDs) == 0 {
		if _, err := h.creds.CreatePasswordInTx(actCtx, tx, in); err != nil {
			return err
		}
	}
	// At most one active password exists per identifier, so oldIDs holds at
	// most one entry; the supersede copies its party authorizations.
	for _, oldID := range oldIDs {
		if _, err := h.creds.SupersedePasswordInTx(actCtx, tx, oldID, in); err != nil {
			return err
		}
	}
	if _, err := q.ConsumeOpenChallengesBySubject(ctx, usersdb.ConsumeOpenChallengesBySubjectParams{
		Kind:         svc.ChallengePasswordReset,
		PrincipalID:  pgtype.Int8{Int64: principalID, Valid: true},
		IdentifierID: pgtype.Int8{Int64: identifierID, Valid: true},
	}); err != nil {
		return fmt.Errorf("consume other reset tokens: %w", err)
	}
	return nil
}
