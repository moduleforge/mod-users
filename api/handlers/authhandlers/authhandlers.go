// Package authhandlers is the public facade for users-module auth HTTP handlers.
// It re-exports types, constructors, and route-registration from
// internal/handlers/auth so external modules can use them without accessing
// the internal package directly.
package authhandlers

import (
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/moduleforge/core-api/observer"
	coredb "github.com/moduleforge/core-model/db"
	usersdb "github.com/moduleforge/mod-users/model/db"

	"github.com/moduleforge/mod-users/api/auth"
	"github.com/moduleforge/mod-users/api/config"
	"github.com/moduleforge/mod-users/api/email"
	inner "github.com/moduleforge/mod-users/api/internal/handlers/auth"
	innersvc "github.com/moduleforge/mod-users/api/internal/service"
)

// Handler is the local-auth and OIDC-callback request handler.
type Handler = inner.Handler

// OIDCHandler is the OIDC-flow handler (start, callback, list providers).
type OIDCHandler = inner.OIDCHandler

// FirstUserHookFn is the type for the first-user registration hook.
type FirstUserHookFn = inner.FirstUserHookFn

// New constructs a Handler for the /v1/auth route group.
func New(
	pool *pgxpool.Pool,
	queries *usersdb.Queries,
	coreQ *coredb.Queries,
	jwtSecret, issuer string,
	sender email.Sender,
	guiBase string,
) *Handler {
	return inner.New(pool, queries, coreQ, jwtSecret, issuer, sender, guiBase)
}

// NewWithSigner constructs a Handler that always signs local tokens with
// signer (see auth.NewSigner). Prefer it over New for any app that may run with
// JWT_ALG=EdDSA; a nil signer is an error.
func NewWithSigner(
	pool *pgxpool.Pool,
	queries *usersdb.Queries,
	coreQ *coredb.Queries,
	signer *auth.Signer,
	issuer string,
	sender email.Sender,
	guiBase string,
) (*Handler, error) {
	return inner.NewWithSigner(pool, queries, coreQ, signer, issuer, sender, guiBase)
}

// RegisterRoutes mounts /v1/auth/* routes on r. oidc may be nil when OIDC
// is not configured (routes that need an OIDCHandler are skipped).
func RegisterRoutes(r chi.Router, h *Handler, oidc *OIDCHandler) {
	inner.RegisterRoutes(r, h, oidc)
}

// NewOIDCHandler constructs an OIDCHandler with a connection pool and observer
// group for production use. It delegates to the internal constructor that
// supports the link-mode callback path.
func NewOIDCHandler(
	pool *pgxpool.Pool,
	queries *usersdb.Queries,
	oauth *auth.OAuth,
	resolver *auth.UserResolver,
	userSvc *innersvc.UserAccountService,
	cfg *config.Config,
	obs *observer.ObserverGroup,
) *OIDCHandler {
	return inner.NewOIDCHandlerWithPool(pool, queries, oauth, resolver, userSvc, cfg, obs)
}
