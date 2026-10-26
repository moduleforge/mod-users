// Package handlers is the public facade for users-module HTTP handlers.
// It re-exports handler types, constructors, and route-registration functions
// from internal/handlers so external modules (e.g. the generated wiring) can
// use them without accessing the internal package directly.
package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"

	"github.com/moduleforge/mod-users/api/auth"
	"github.com/moduleforge/mod-users/api/config"
	inner "github.com/moduleforge/mod-users/api/internal/handlers"
	innerme "github.com/moduleforge/mod-users/api/internal/handlers/me"
	innerparties "github.com/moduleforge/mod-users/api/internal/handlers/parties"
	innerprincipals "github.com/moduleforge/mod-users/api/internal/handlers/principals"
	innersession "github.com/moduleforge/mod-users/api/internal/handlers/session"
	"github.com/moduleforge/mod-users/api/usersservice"
)

// Handler type aliases.
type (
	// OIDCConfigHandler serves the /v1/oidc-config onboarding routes and owns
	// the OIDC boot state.
	OIDCConfigHandler = inner.OIDCConfigHandler
	// ProvidersHandler serves the /v1/oidc-config provider CRUD routes.
	ProvidersHandler = inner.ProvidersHandler
	// MeHandler serves the /v1/me routes.
	MeHandler = innerme.MeHandler
	// SessionHandler serves the /v1/session routes.
	SessionHandler = innersession.SessionHandler
	// PrincipalsHandler serves the /v1/principals routes.
	PrincipalsHandler = innerprincipals.PrincipalsHandler
	// PartiesHandler serves the /v1/parties/{party_uuid} routes and
	// PATCH /v1/profiles/{uuid}.
	PartiesHandler = innerparties.PartiesHandler
	// BootstrapHandler serves the public /v1/bootstrap routes (first-run
	// bootstrap of the operator account and its status).
	BootstrapHandler = inner.BootstrapHandler
)

// NewOIDCConfigHandler constructs the OIDC config handler from individual
// dependencies declared in the module manifest.
//
// envNoOIDCEnv and adminChecker were added after the original four params
// (Phase 02 onboarding-boot-helpers task), and platform after those, so
// every existing positional caller keeps working with only an append.
// adminChecker is the operator-administrator check (NewOperatorAdminChecker);
// it may be nil, in which case only the setup token authorizes the
// configuration routes (see OIDCConfigDeps.AdminChecker's doc comment).
// platform reads platform_settings so the setup token stays active while the
// bootstrap is pending (decision U9); a nil platform treats the deployment as
// bootstrapped.
//
// Callers MUST call RefreshState on the returned handler before serving: its
// cache starts unpopulated, and a handler whose cache was never populated
// rejects every token.
func NewOIDCConfigHandler(
	queries *usersdb.Queries,
	oauth *auth.OAuth,
	envRegistry config.ProviderRegistry,
	tokenDisplay config.TokenDisplay,
	envNoOIDCEnv bool,
	adminChecker func(r *http.Request) (bool, error),
	platform coredb.Querier,
) *OIDCConfigHandler {
	deps := inner.OIDCConfigDeps{
		Queries:      queries,
		OAuth:        oauth,
		EnvRegistry:  envRegistry,
		EnvNoOIDCEnv: envNoOIDCEnv,
		TokenDisplay: tokenDisplay,
		AdminChecker: adminChecker,
	}
	if platform != nil {
		deps.Platform = platform
	}
	return inner.NewOIDCConfigHandler(deps)
}

// OperatorAdminCheck is the admin check NewOIDCConfigHandler's adminChecker
// accepts: (true, nil) for an operator administrator, (false, nil) for any
// other or no caller, an error for an internal fault.
type OperatorAdminCheck func(r *http.Request) (bool, error)

// NewOperatorAdminChecker returns the admin check of the OIDC configuration
// routes: a bearer access token of a live, non-impersonating session whose
// acting profile is an operator profile holding wildcard manage.
func NewOperatorAdminChecker(authn *auth.Authenticator, boot *usersservice.BootstrapService) OperatorAdminCheck {
	return inner.NewOperatorAdminChecker(authn, boot)
}

// NewBootstrapHandler constructs the /v1/bootstrap handler over the bootstrap
// service and the OIDC config handler, which owns the setup token.
func NewBootstrapHandler(boot *usersservice.BootstrapService, setup *OIDCConfigHandler) *BootstrapHandler {
	return inner.NewBootstrapHandler(boot, setup)
}

// RegisterBootstrapRoutes mounts GET /status and POST / on r. The routes are
// public: apply neither auth.NewRequireAuth nor auth.RequireOIDCConfirmed.
func RegisterBootstrapRoutes(r chi.Router, h *BootstrapHandler) {
	inner.RegisterBootstrapRoutes(r, h)
}

// NewProvidersHandler constructs the OIDC providers handler from individual
// dependencies declared in the module manifest. parties resolves a provider's
// party_uuid; a nil parties refuses every non-null party_uuid with 400.
func NewProvidersHandler(
	queries *usersdb.Queries,
	oauth *auth.OAuth,
	envRegistry config.ProviderRegistry,
	redirectBase string,
	parties coredb.Querier,
	confirmer *OIDCConfigHandler,
) *ProvidersHandler {
	deps := inner.ProvidersDeps{
		Queries:      queries,
		EnvRegistry:  envRegistry,
		OAuth:        oauth,
		RedirectBase: redirectBase,
		Confirmer:    confirmer,
	}
	if parties != nil {
		deps.Parties = parties
	}
	return inner.NewProvidersHandler(deps)
}

// RegisterOIDCConfigRoutes mounts the OIDC-config endpoints on r.
func RegisterOIDCConfigRoutes(r chi.Router, h *OIDCConfigHandler, p *ProvidersHandler) {
	inner.RegisterOIDCConfigRoutes(r, h, p)
}

// NewMeHandler constructs the /v1/me handler over the user service, which
// must have been wired with UserService.WithSelfService, and the profile
// service that serves the holder's accept, decline and leave actions.
func NewMeHandler(users *usersservice.UserService, profiles *usersservice.ProfileService) *MeHandler {
	return innerme.NewMeHandler(users, profiles)
}

// OIDCLinkStarter is the OIDC handler's start of the link-a-credential flow,
// mounted on POST /credentials/oidc/{provider}/start of the /v1/me group.
// *authhandlers.OIDCHandler satisfies it.
type OIDCLinkStarter interface {
	LinkStart(w http.ResponseWriter, r *http.Request)
}

// RegisterMeRoutes mounts the /v1/me routes on r; the caller applies
// auth.NewRequireAuth first. link may be nil (an untyped nil) when OIDC is not
// wired, in which case the OIDC link start is not mounted.
func RegisterMeRoutes(r chi.Router, h *MeHandler, link OIDCLinkStarter) {
	innerme.RegisterMeRoutes(r, h)
	if link != nil {
		r.Post("/credentials/oidc/{provider}/start", link.LinkStart)
	}
}

// NewSessionHandler constructs the /v1/session handler.
func NewSessionHandler(sessions *usersservice.SessionService) *SessionHandler {
	return innersession.NewSessionHandler(sessions)
}

// OIDCStepUpStarter is the OIDC handler's start of the step-up flow, mounted
// on POST /step-up/oidc/{provider}/start of the /v1/session group.
// *authhandlers.OIDCHandler satisfies it.
type OIDCStepUpStarter interface {
	StepUpStart(w http.ResponseWriter, r *http.Request)
}

// RegisterSessionRoutes mounts the /v1/session routes on r; the caller applies
// auth.NewRequireAuth first. stepUp may be nil (an untyped nil) when OIDC is
// not wired, in which case the OIDC step-up start is not mounted.
func RegisterSessionRoutes(r chi.Router, h *SessionHandler, stepUp OIDCStepUpStarter) {
	innersession.RegisterSessionRoutes(r, h)
	if stepUp != nil {
		r.Post("/step-up/oidc/{provider}/start", stepUp.StepUpStart)
	}
}

// NewPrincipalsHandler constructs the /v1/principals handler over the user
// service, which must have been wired with UserService.WithSelfService.
func NewPrincipalsHandler(users *usersservice.UserService) *PrincipalsHandler {
	return innerprincipals.NewPrincipalsHandler(users)
}

// RegisterPrincipalRoutes mounts the /v1/principals routes on r; the caller
// applies auth.NewRequireAuth first.
func RegisterPrincipalRoutes(r chi.Router, h *PrincipalsHandler) {
	innerprincipals.RegisterPrincipalRoutes(r, h)
}

// NewPartiesHandler constructs the party-administration handler over the
// profile service and the party gating service.
func NewPartiesHandler(profiles *usersservice.ProfileService, policy *usersservice.PartyPolicyService) *PartiesHandler {
	return innerparties.NewPartiesHandler(profiles, policy)
}

// RegisterPartyRoutes mounts the /v1/parties routes on r (a party's profiles,
// managed users, authentication policy, credential authorizations and
// confinements); the caller applies auth.NewRequireAuth first.
func RegisterPartyRoutes(r chi.Router, h *PartiesHandler) {
	innerparties.RegisterPartyRoutes(r, h)
}

// RegisterProfileRoutes mounts the /v1/profiles routes on r (a party profile's
// status); the caller applies auth.NewRequireAuth first.
func RegisterProfileRoutes(r chi.Router, h *PartiesHandler) {
	innerparties.RegisterProfileRoutes(r, h)
}

// Live is the liveness health-check handler.
func Live(w http.ResponseWriter, r *http.Request) {
	inner.Live(w, r)
}

// Ready returns a readiness health-check handler that pings the pool.
func Ready(pool *pgxpool.Pool) http.HandlerFunc {
	return inner.Ready(pool)
}
