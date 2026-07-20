// Package handlers is the public facade for users-module HTTP handlers.
// It re-exports handler types, constructors, and route-registration functions
// from internal/handlers so external modules (e.g. the generated wiring) can
// use them without accessing the internal package directly.
package handlers

import (
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	coreAuthz "github.com/moduleforge/core-api/authz"
	"github.com/moduleforge/core-api/entity"
	"github.com/moduleforge/core-api/observer"
	coreservice "github.com/moduleforge/core-api/service"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"

	"github.com/moduleforge/mod-users/api/auth"
	"github.com/moduleforge/mod-users/api/config"
	inner "github.com/moduleforge/mod-users/api/internal/handlers"
	authhandlers "github.com/moduleforge/mod-users/api/internal/handlers/auth"
	innersvc "github.com/moduleforge/mod-users/api/internal/service"
)

// Handler type aliases.
type OIDCConfigHandler = inner.OIDCConfigHandler
type UserAccountsHandler = inner.UserAccountsHandler
type ProvidersHandler = inner.ProvidersHandler
type AssumeHandler = inner.AssumeHandler
type AppsHandler = inner.AppsHandler
type GrantAdminFn = inner.GrantAdminFn
type SelfHandler = inner.SelfHandler
type IdentitiesHandler = inner.IdentitiesHandler

// NewOIDCConfigHandler constructs the OIDC config handler from individual
// dependencies declared in the module manifest.
//
// envNoOIDCEnv and adminChecker were added after the original four params
// (Phase 02 onboarding-boot-helpers task) so every existing positional
// caller keeps working with only an append. adminChecker may be nil for
// deployments with no admin-checker concept — inner tolerates a nil
// AdminChecker (see OIDCConfigDeps.AdminChecker's doc comment).
func NewOIDCConfigHandler(
	queries *usersdb.Queries,
	oauth *auth.OAuth,
	envRegistry config.ProviderRegistry,
	tokenDisplay config.TokenDisplay,
	envNoOIDCEnv bool,
	adminChecker func(r *http.Request) (bool, error),
) *OIDCConfigHandler {
	return inner.NewOIDCConfigHandler(inner.OIDCConfigDeps{
		Queries:      queries,
		OAuth:        oauth,
		EnvRegistry:  envRegistry,
		EnvNoOIDCEnv: envNoOIDCEnv,
		TokenDisplay: tokenDisplay,
		AdminChecker: adminChecker,
	})
}

// NewUserAccountsHandler constructs the user accounts handler.
// grantAdmin and revokeAdmin may be nil for deployments that do not wire
// the admin-grant closures (known gap: phase-4 closure design).
func NewUserAccountsHandler(svc *innersvc.UserAccountService, grantAdmin, revokeAdmin GrantAdminFn) *UserAccountsHandler {
	return inner.NewUserAccountsHandler(svc, grantAdmin, revokeAdmin)
}

// NewProvidersHandler constructs the OIDC providers handler from individual
// dependencies declared in the module manifest.
func NewProvidersHandler(
	queries *usersdb.Queries,
	oauth *auth.OAuth,
	envRegistry config.ProviderRegistry,
	redirectBase string,
	confirmer *OIDCConfigHandler,
) *ProvidersHandler {
	return inner.NewProvidersHandler(inner.ProvidersDeps{
		Queries:      queries,
		EnvRegistry:  envRegistry,
		OAuth:        oauth,
		RedirectBase: redirectBase,
		Confirmer:    confirmer,
	})
}

// NewAssumeHandler constructs the assume-identity handler.
func NewAssumeHandler(
	svc *innersvc.UserAccountService,
	jwtSecret, issuer string,
) *AssumeHandler {
	return inner.NewAssumeHandler(svc, jwtSecret, issuer)
}

// NewAppsHandler constructs the apps handler, which serves the
// apps/user-accounts membership endpoints (/v1/apps/{uuid}/user-accounts).
// Top-level /apps CRUD lives in mod-core; entityResolver resolves the
// {uuid} path param to the app's internal id since mod-users no longer
// owns an apps query of its own.
func NewAppsHandler(
	pool *pgxpool.Pool,
	queries *usersdb.Queries,
	az coreAuthz.Authorizer,
	observers *observer.ObserverGroup,
	entityResolver *entity.Resolver,
	coreQ *coredb.Queries,
) *AppsHandler {
	return inner.NewAppsHandler(pool, queries, az, observers, entityResolver, coreQ)
}

// RegisterOIDCConfigRoutes mounts the OIDC-config endpoints on r.
func RegisterOIDCConfigRoutes(r chi.Router, h *OIDCConfigHandler, p *ProvidersHandler) {
	inner.RegisterOIDCConfigRoutes(r, h, p)
}

// RegisterAccountRoutes mounts the user-accounts, assume-identity, and
// apps/user-accounts membership endpoints on r. assume and apps may be nil
// for partial deployments. Top-level /apps CRUD is not part of this
// registrar — it now lives in mod-core.
func RegisterAccountRoutes(r chi.Router, h *UserAccountsHandler, assume *AssumeHandler, apps *AppsHandler) {
	inner.RegisterAccountRoutes(r, h, assume, apps)
}

// NewSelfHandler constructs the /self handler. /self is a composite identity
// endpoint: core-module owns entity data (via coreSvcs.Entity.GetSelf) while
// users-module owns the user-account row (email, timestamps, uuid).
func NewSelfHandler(q *usersdb.Queries, coreQ *coredb.Queries, coreSvcs *coreservice.Services) *SelfHandler {
	return inner.NewSelfHandler(q, coreQ, coreSvcs)
}

// RegisterSelfGetRoute mounts GET /self on r. Deliberately separate from
// RegisterSelfPutRoute so each can carry its own middleware group in the
// module manifest -- GET must stay reachable to accounts with an unverified
// email (the GUI renders the "verify your email" page from it).
func RegisterSelfGetRoute(r chi.Router, h *SelfHandler) {
	inner.RegisterSelfGetRoute(r, h)
}

// RegisterSelfPutRoute mounts PUT /self on r. Requires a verified email --
// see RegisterSelfGetRoute's doc comment for why this is a separate entry.
func RegisterSelfPutRoute(r chi.Router, h *SelfHandler) {
	inner.RegisterSelfPutRoute(r, h)
}

// NewIdentitiesHandler constructs the identity/credential self-service
// handler from individually-typed dependencies declared in the module
// manifest. sender is typed as the exported, method-set-identical
// authhandlers.Sender interface (rather than inner's unexported emailSender)
// so the facade can name it; assigning it into the emailSender-typed
// IdentitiesHandlerDeps.Sender field is valid Go since the method sets are
// identical.
func NewIdentitiesHandler(
	pool *pgxpool.Pool,
	queries *usersdb.Queries,
	oauth *auth.OAuth,
	obs *observer.ObserverGroup,
	sender authhandlers.Sender,
	jwtSecret string,
	consumed *sync.Map,
	stepUpRequired bool,
) *IdentitiesHandler {
	return inner.NewIdentitiesHandlerWithDeps(inner.IdentitiesHandlerDeps{
		Pool:           pool,
		Queries:        queries,
		OAuth:          oauth,
		Obs:            obs,
		Sender:         sender,
		JWTSecret:      jwtSecret,
		Consumed:       consumed,
		StepUpRequired: stepUpRequired,
	})
}

// RegisterSelfIdentitiesReadRoute mounts GET /self/identities on r. Reachable
// to accounts with an unverified email -- see RegisterSelfGetRoute's doc
// comment for the rationale this mirrors.
func RegisterSelfIdentitiesReadRoute(r chi.Router, h *IdentitiesHandler) {
	inner.RegisterSelfIdentitiesReadRoute(r, h)
}

// RegisterSelfIdentitiesWriteRoutes mounts the six credential-mutating
// identity endpoints on r. Requires a verified email -- see
// RegisterSelfPutRoute's doc comment for the rationale this mirrors.
func RegisterSelfIdentitiesWriteRoutes(r chi.Router, h *IdentitiesHandler) {
	inner.RegisterSelfIdentitiesWriteRoutes(r, h)
}

// Live is the liveness health-check handler.
func Live(w http.ResponseWriter, r *http.Request) {
	inner.Live(w, r)
}

// Ready returns a readiness health-check handler that pings the pool.
func Ready(pool *pgxpool.Pool) http.HandlerFunc {
	return inner.Ready(pool)
}
