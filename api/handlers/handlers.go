// Package handlers is the public facade for users-module HTTP handlers.
// It re-exports handler types, constructors, and route-registration functions
// from internal/handlers so external modules (e.g. the generated wiring) can
// use them without accessing the internal package directly.
package handlers

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	usersdb "github.com/moduleforge/users-module/model/db"

	"github.com/moduleforge/users-module/api/auth"
	"github.com/moduleforge/users-module/api/config"
	inner "github.com/moduleforge/users-module/api/internal/handlers"
	innersvc "github.com/moduleforge/users-module/api/internal/service"
)

// Handler type aliases.
type OIDCConfigHandler = inner.OIDCConfigHandler
type UserAccountsHandler = inner.UserAccountsHandler
type ProvidersHandler = inner.ProvidersHandler
type AssumeHandler = inner.AssumeHandler
type AppsHandler = inner.AppsHandler
type GrantAdminFn = inner.GrantAdminFn

// NewOIDCConfigHandler constructs the OIDC config handler from individual
// dependencies declared in the module manifest.
func NewOIDCConfigHandler(
	queries *usersdb.Queries,
	oauth *auth.OAuth,
	envRegistry config.ProviderRegistry,
	tokenDisplay config.TokenDisplay,
) *OIDCConfigHandler {
	return inner.NewOIDCConfigHandler(inner.OIDCConfigDeps{
		Queries:      queries,
		OAuth:        oauth,
		EnvRegistry:  envRegistry,
		TokenDisplay: tokenDisplay,
	})
}

// NewUserAccountsHandler constructs the user accounts handler.
// grantAdmin and revokeAdmin may be nil for deployments that do not wire
// the admin-grant closures (known gap: phase-4 closure design).
func NewUserAccountsHandler(svc *innersvc.UserAccountService, grantAdmin, revokeAdmin GrantAdminFn) *UserAccountsHandler {
	return inner.NewUserAccountsHandler(svc, grantAdmin, revokeAdmin)
}

// RegisterOIDCConfigRoutes mounts the OIDC-config endpoints on r.
func RegisterOIDCConfigRoutes(r chi.Router, h *OIDCConfigHandler, p *ProvidersHandler) {
	inner.RegisterOIDCConfigRoutes(r, h, p)
}

// RegisterAccountRoutes mounts the user-accounts, assume-identity, and apps
// endpoints on r. assume and apps may be nil for partial deployments.
func RegisterAccountRoutes(r chi.Router, h *UserAccountsHandler, assume *AssumeHandler, apps *AppsHandler) {
	inner.RegisterAccountRoutes(r, h, assume, apps)
}

// Live is the liveness health-check handler.
func Live(w http.ResponseWriter, r *http.Request) {
	inner.Live(w, r)
}

// Ready returns a readiness health-check handler that pings the pool.
func Ready(pool *pgxpool.Pool) http.HandlerFunc {
	return inner.Ready(pool)
}
