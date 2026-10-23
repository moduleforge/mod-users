package auth

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// registerRequest is the body for POST /v1/auth/register.
type registerRequest struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	GivenName  string `json:"given_name"`
	FamilyName string `json:"family_name"`
	// App optionally names the app the new session signs in to.
	App string `json:"app"`
}

// Register handles POST /v1/auth/register: self-registration of a
// bring-your-own user. In one transaction it creates the user with its own
// person record and personal profile, the primary email identifier
// (unverified), the password credential bound to it and the session, and
// answers 201 with the token response. A verify_identifier code is emailed
// after commit.
//
// Order: 400 for malformed input, then 403 users.byo_identity_disabled when
// platform_settings.allow_byo_identity is off, then 409
// users.identifier_taken. NO_OIDC_ACCOUNTS governs OIDC just-in-time accounts
// (the OIDC resolver), not local registration.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !server.Decode(w, r, &req) {
		return
	}
	if details := required(
		[2]string{"email", strings.TrimSpace(req.Email)},
		[2]string{"password", req.Password},
		[2]string{"given_name", strings.TrimSpace(req.GivenName)},
		[2]string{"family_name", strings.TrimSpace(req.FamilyName)},
	); len(details) > 0 {
		apiresp.WriteError(w, r, apiresp.InvalidInput(details...))
		return
	}
	if err := svc.ValidateEmail(req.Email); err != nil {
		writeError(w, r, err)
		return
	}
	if err := svc.ValidatePassword(req.Password); err != nil {
		writeError(w, r, err)
		return
	}
	appID, err := h.sessions.ResolveApp(r.Context(), req.App)
	if err != nil {
		writeError(w, r, err)
		return
	}

	settings, err := h.coreQ.GetPlatformSettings(r.Context())
	if err != nil {
		writeError(w, r, fmt.Errorf("register: load platform settings: %w", err))
		return
	}
	if !settings.AllowByoIdentity {
		writeError(w, r, svc.ErrBYOIdentityDisabled)
		return
	}

	var (
		resp     svc.TokenResponse
		verify   svc.IssuedChallenge
		emailTo  string
		haveCode bool
	)
	err = h.inTx(r.Context(), func(tx pgx.Tx) error {
		user, err := h.users.CreateBYOUserInTx(r.Context(), tx, svc.CreateBYOUserInput{
			GivenName: req.GivenName, FamilyName: req.FamilyName,
		})
		if err != nil {
			return err
		}
		actCtx := user.ActingContext(r.Context())
		ident, err := h.users.AddIdentifierInTx(actCtx, tx, svc.AddIdentifierInput{
			UserID: user.User.EntityID, Kind: svc.IdentifierKindEmail, Value: req.Email, Primary: true,
		})
		if err != nil {
			return err
		}
		cred, err := h.creds.CreatePasswordInTx(actCtx, tx, svc.CreatePasswordInput{
			PrincipalID: user.User.EntityID, IdentifierID: &ident.ID, Password: req.Password,
		})
		if err != nil {
			return err
		}
		verify, err = h.challenges.IssueCodeInTx(actCtx, tx, svc.IssueCodeInput{
			Kind:    svc.ChallengeVerifyIdentifier,
			Subject: svc.Subject{PrincipalID: user.User.EntityID, IdentifierID: ident.ID},
		})
		if err != nil {
			return err
		}
		emailTo, haveCode = ident.Value, true
		resp, err = h.sessions.StartSessionInTx(r.Context(), tx, svc.StartSessionInput{
			PrincipalID: user.User.EntityID,
			AppID:       appID,
			Factors:     []svc.Factor{{CredentialID: cred.ID}},
		})
		return err
	})
	if err != nil {
		writeError(w, r, err)
		return
	}

	if haveCode {
		code := verify.Code
		h.dispatch(r, "verify_identifier", func(ctx context.Context) {
			if err := h.challenges.DeliverCode(ctx, emailTo, svc.ChallengeVerifyIdentifier, code); err != nil {
				slog.ErrorContext(ctx, "register: deliver verification code", "error", err)
			}
		})
	}
	apiresp.WriteJSON(w, http.StatusCreated, resp)
}
