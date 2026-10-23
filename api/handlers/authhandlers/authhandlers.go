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
	inner "github.com/moduleforge/mod-users/api/internal/handlers/auth"
	"github.com/moduleforge/mod-users/api/usersservice"
)

// Handler is the local-auth request handler (the /v1/auth routes).
type Handler = inner.Handler

// OIDCHandler is the OIDC-flow handler: start and callback of the /v1/auth
// sign-in, plus the link-credential and step-up starts.
type OIDCHandler = inner.OIDCHandler

// Sender delivers the password-reset links; email.Sender satisfies it.
type Sender = inner.Sender

// OIDCRoutes is what RegisterRoutes needs from the OIDC handler.
// *OIDCHandler satisfies it.
type OIDCRoutes = inner.OIDCRoutes

// New constructs a Handler for the /v1/auth route group. oauth lists the
// enabled OIDC providers for GET /providers and may be nil.
func New(
	pool *pgxpool.Pool,
	queries *usersdb.Queries,
	coreQ *coredb.Queries,
	users *usersservice.UserService,
	creds *usersservice.CredentialService,
	challenges *usersservice.ChallengeService,
	sessions *usersservice.SessionService,
	oauth *auth.OAuth,
	sender Sender,
	guiBase string,
) *Handler {
	return inner.New(pool, queries, coreQ, users, creds, challenges, sessions, oauth, sender, guiBase)
}

// RegisterRoutes mounts /v1/auth/* routes on r. oidc may be nil (an untyped
// nil) when OIDC is not configured (the two OIDC routes are then skipped).
func RegisterRoutes(r chi.Router, h *Handler, oidc OIDCRoutes) {
	inner.RegisterRoutes(r, h, oidc)
}

// NewOIDCHandler constructs the OIDCHandler. resolver is normally
// auth.NewOIDCResolver over the module's queries; obs may be nil.
func NewOIDCHandler(
	pool *pgxpool.Pool,
	queries *usersdb.Queries,
	coreQ *coredb.Queries,
	oauth *auth.OAuth,
	resolver *auth.OIDCResolver,
	users *usersservice.UserService,
	creds *usersservice.CredentialService,
	challenges *usersservice.ChallengeService,
	sessions *usersservice.SessionService,
	obs *observer.ObserverGroup,
	cfg *config.Config,
) *OIDCHandler {
	return inner.NewOIDCHandler(pool, queries, coreQ, oauth, resolver, users, creds, challenges, sessions, obs, cfg)
}
