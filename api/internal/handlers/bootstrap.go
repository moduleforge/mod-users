package handlers

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/mod-users/api/internal/server"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

// setupTokenHeader carries the setup token on the bootstrap route.
const setupTokenHeader = "X-Setup-Token"

// BootstrapHandler serves the public /v1/bootstrap routes: the first-run
// bootstrap of the deployment (decisions U9 and U10) and its status. It
// verifies the setup token against the OIDC config handler, which owns the
// token's lifecycle, and tells that handler when a bootstrap committed so it
// can clear the token once OIDC is confirmed too.
type BootstrapHandler struct {
	boot  *svc.BootstrapService
	setup *OIDCConfigHandler
}

// NewBootstrapHandler constructs the bootstrap handler over the bootstrap
// service and the OIDC config handler that owns the setup token.
func NewBootstrapHandler(boot *svc.BootstrapService, setup *OIDCConfigHandler) *BootstrapHandler {
	return &BootstrapHandler{boot: boot, setup: setup}
}

// RegisterBootstrapRoutes mounts the /v1/bootstrap routes on r:
//
//	GET  /status
//	POST /
//
// Both are public: the caller must not apply the authentication middleware
// or the OIDC-confirmed gate (the bootstrap may precede the OIDC
// configuration).
func RegisterBootstrapRoutes(r chi.Router, h *BootstrapHandler) {
	r.Get("/status", h.Status)
	r.Post("/", h.Bootstrap)
}

// bootstrapStatusResponse is the body of GET /v1/bootstrap/status.
type bootstrapStatusResponse struct {
	Bootstrapped  bool `json:"bootstrapped"`
	OIDCConfirmed bool `json:"oidc_confirmed"`
}

// Status handles GET /v1/bootstrap/status: whether the deployment has an
// operator party and whether OIDC onboarding is confirmed. No secrets.
func (h *BootstrapHandler) Status(w http.ResponseWriter, r *http.Request) {
	bootstrapped, err := h.boot.Bootstrapped(r.Context())
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("bootstrap status: %w", err))
		return
	}
	server.JSON(w, http.StatusOK, bootstrapStatusResponse{
		Bootstrapped:  bootstrapped,
		OIDCConfirmed: h.setup.CurrentState().Confirmed(),
	})
}

// bootstrapRequest is the body of POST /v1/bootstrap (API surface section 7).
type bootstrapRequest struct {
	Operator struct {
		Kind         string `json:"kind"`
		LegalName    string `json:"legal_name"`
		LegalForm    string `json:"legal_form"`
		Jurisdiction string `json:"jurisdiction"`
	} `json:"operator"`
	Admin struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		GivenName  string `json:"given_name"`
		FamilyName string `json:"family_name"`
	} `json:"admin"`
}

// Bootstrap handles POST /v1/bootstrap. Error order (credential-in-header
// route, as POST /v1/oidc-config/confirm with a header token): a missing or
// wrong X-Setup-Token is 401 users.setup_token_invalid before the body is
// read; then 400 for a malformed or invalid body; then 409
// users.already_bootstrapped. Success is 201 with the standard token response
// of a new password session landing on the admin profile.
func (h *BootstrapHandler) Bootstrap(w http.ResponseWriter, r *http.Request) {
	ok, err := h.setup.VerifySetupTokenFresh(r.Context(), r.Header.Get(setupTokenHeader))
	if err != nil {
		apiresp.WriteError(w, r, fmt.Errorf("bootstrap: verify setup token: %w", err))
		return
	}
	if !ok {
		writeServiceError(w, r, svc.ErrSetupTokenInvalid)
		return
	}

	var req bootstrapRequest
	if !server.Decode(w, r, &req) {
		return
	}
	in := svc.BootstrapInput{
		Operator: svc.BootstrapOperator{
			Kind:         req.Operator.Kind,
			LegalName:    req.Operator.LegalName,
			LegalForm:    req.Operator.LegalForm,
			Jurisdiction: req.Operator.Jurisdiction,
		},
		Admin: svc.BootstrapAdmin{
			Email:      req.Admin.Email,
			Password:   req.Admin.Password,
			GivenName:  req.Admin.GivenName,
			FamilyName: req.Admin.FamilyName,
		},
	}
	if err := svc.ValidateBootstrapInput(in); err != nil {
		apiresp.WriteError(w, r, err)
		return
	}

	res, err := h.boot.Bootstrap(r.Context(), in)
	if err != nil {
		writeServiceError(w, r, err)
		return
	}
	// The bootstrap committed. Clearing the token (when OIDC is already
	// confirmed) is best-effort: a failure leaves a hash that the next boot
	// clears; meanwhile the OIDC routes refuse it once the token is no
	// longer active, and a second bootstrap with it still gets 409.
	if err := h.setup.AfterBootstrap(r.Context()); err != nil {
		slog.WarnContext(r.Context(), "bootstrap: refresh setup-token state", "error", err)
	}
	apiresp.WriteJSON(w, http.StatusCreated, res.Token)
}

// writeServiceError renders a service.AuthError with its own code and status,
// and everything else through apiresp.WriteError.
func writeServiceError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *svc.AuthError
	if errors.As(err, &ae) {
		apiresp.WriteJSON(w, ae.Status, ae.Envelope())
		return
	}
	apiresp.WriteError(w, r, err)
}
