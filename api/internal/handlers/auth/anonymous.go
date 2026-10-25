package auth

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// anonymousRequest is the body for POST /v1/auth/anonymous.
type anonymousRequest struct {
	// DeviceID is a stable device fingerprint; required.
	DeviceID string `json:"device_id"`
	// DeviceToken, when set, signs an existing guest back in with the token
	// its anon credential was issued with, instead of creating a guest.
	DeviceToken string `json:"device_token"`
	// App optionally names the app the session signs in to.
	App string `json:"app"`
}

// anonymousResponse is the token response plus, for a new guest, the device
// token of its anon credential. The device token is returned only once and
// signs the guest back in later (device_token in the request).
type anonymousResponse struct {
	svc.TokenResponse
	DeviceToken string `json:"device_token,omitempty"`
}

// Anonymous handles POST /v1/auth/anonymous: guest sign-in. Without a
// device_token it creates a guest user (personal profile, no person, one
// anon credential) and its session, answering 201 with the token response
// and the new device_token. With a device_token it signs that guest back in
// (200); both are 403 users.byo_identity_disabled when the platform does not
// allow bring-your-own identities. An unknown token, or one registered for another device, is 401
// users.invalid_credentials.
func (h *Handler) Anonymous(w http.ResponseWriter, r *http.Request) {
	var req anonymousRequest
	if !server.Decode(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.DeviceID) == "" {
		apiresp.WriteError(w, r, apiresp.InvalidInput(apiresp.FieldError{
			Field: "device_id", Code: "users.device_id_required", Message: "device_id is required",
		}))
		return
	}

	// Guests are bring-your-own users with a personal profile, so both creating
	// a guest and signing an existing one back in are refused when the platform
	// does not allow bring-your-own identities (tenant-strict).
	settings, err := h.coreQ.GetPlatformSettings(r.Context())
	if err != nil {
		writeError(w, r, fmt.Errorf("anonymous: load platform settings: %w", err))
		return
	}
	if !settings.AllowByoIdentity {
		writeError(w, r, svc.ErrBYOIdentityDisabled)
		return
	}

	if req.DeviceToken != "" {
		resp, err := h.sessions.LoginWithDeviceToken(r.Context(), req.DeviceID, req.DeviceToken, req.App)
		if err != nil {
			writeError(w, r, err)
			return
		}
		apiresp.WriteJSON(w, http.StatusOK, anonymousResponse{TokenResponse: resp})
		return
	}

	appID, err := h.sessions.ResolveApp(r.Context(), req.App)
	if err != nil {
		writeError(w, r, err)
		return
	}
	var out anonymousResponse
	err = h.inTx(r.Context(), func(tx pgx.Tx) error {
		guest, err := h.users.CreateGuestUserInTx(r.Context(), tx, svc.CreateGuestUserInput{DeviceID: req.DeviceID})
		if err != nil {
			return err
		}
		out.DeviceToken = guest.Anon.Token
		out.TokenResponse, err = h.sessions.StartSessionInTx(r.Context(), tx, svc.StartSessionInput{
			PrincipalID: guest.User.EntityID,
			AppID:       appID,
			Factors:     []svc.Factor{{CredentialID: guest.Anon.Credential.ID}},
		})
		return err
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	apiresp.WriteJSON(w, http.StatusCreated, out)
}
