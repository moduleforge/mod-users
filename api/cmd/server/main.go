package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	audithttpapi "github.com/moduleforge/audit-api/httpapi"
	auditservice "github.com/moduleforge/audit-api/service"
	auditdb "github.com/moduleforge/audit-model/db"
	authzapi "github.com/moduleforge/authz-api/authz"
	authzhttpapi "github.com/moduleforge/authz-api/httpapi"
	authzservice "github.com/moduleforge/authz-api/service"
	authzdb "github.com/moduleforge/authz-model/db"
	"github.com/moduleforge/core-api/authz/setup"
	"github.com/moduleforge/core-api/display"
	"github.com/moduleforge/core-api/entity"
	"github.com/moduleforge/core-api/fieldcrypto"
	corehttpapi "github.com/moduleforge/core-api/httpapi"
	"github.com/moduleforge/core-api/observer"
	coreservice "github.com/moduleforge/core-api/service"
	"github.com/moduleforge/core-api/types"
	coredb "github.com/moduleforge/core-model/db"
	pubauth "github.com/moduleforge/mod-users/api/auth"
	"github.com/moduleforge/mod-users/api/internal/auth"
	localAuthz "github.com/moduleforge/mod-users/api/internal/authz"
	"github.com/moduleforge/mod-users/api/internal/config"
	localdb "github.com/moduleforge/mod-users/api/internal/db"
	"github.com/moduleforge/mod-users/api/internal/email"
	"github.com/moduleforge/mod-users/api/internal/handlers"
	authhandlers "github.com/moduleforge/mod-users/api/internal/handlers/auth"
	"github.com/moduleforge/mod-users/api/internal/handlers/me"
	"github.com/moduleforge/mod-users/api/internal/handlers/parties"
	"github.com/moduleforge/mod-users/api/internal/handlers/principals"
	"github.com/moduleforge/mod-users/api/internal/handlers/session"
	"github.com/moduleforge/mod-users/api/internal/observability"
	"github.com/moduleforge/mod-users/api/internal/server"
	"github.com/moduleforge/mod-users/api/usersservice"
	db "github.com/moduleforge/mod-users/model/db"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("config load failed", "error", err)
		os.Exit(1)
	}

	logLevel := resolveLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	otelShutdown, err := observability.Init(ctx, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "otel init failed", "error", err)
		os.Exit(1)
	}

	// Open pgx pool.
	pool, err := localdb.New(ctx, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "database connection failed", "error", err)
		os.Exit(1)
	}

	// Build query layer.
	queries := db.New(pool)
	coreQueries := coredb.New(pool)

	// Build auth components. The Verifier is used by RequireAuth to validate
	// incoming Bearer tokens — post-Phase 9 those are always the local JWTs
	// minted by /v1/auth/login or the OIDC callback, never a raw provider
	// id_token. So the verifier is local-only. NewLocalVerifier pins the
	// accepted algorithm to JWT_ALG: EdDSA-signed local tokens (verified
	// against the public half of JWT_SIGNING_KEY plus any
	// JWT_VERIFY_PUBLIC_KEYS) when JWT_ALG=EdDSA, HS256 otherwise.
	verifier, err := auth.NewLocalVerifier(ctx, cfg.LocalAuth)
	if err != nil {
		slog.ErrorContext(ctx, "auth verifier init failed", "error", err)
		os.Exit(1)
	}

	// Authenticator: verifies the bearer access token and re-checks the
	// session or credential and the acting profile on every request.
	authenticator := pubauth.NewAuthenticator(pool, verifier, cfg.Auth)

	// Anonymous system actor. Resolved once at startup by slug;
	// NewAnonymousActor asserts at boot that the actor holds zero grants and
	// refuses to start if it finds any. This hand-written dev server mounts no
	// anonymous route, so only the boot-time assertion is used
	// (moduleforge.module.yaml's anonymousActor and resolveOrAnonymous entries
	// serve generated apps).
	if _, err := pubauth.NewAnonymousActor(ctx, pool); err != nil {
		slog.ErrorContext(ctx, "anonymous actor init failed", "error", err)
		os.Exit(1)
	}

	// Merge env-declared providers with the DB-persisted override layer
	// (phase 9.11a) so a prior admin edit sticks across restarts.
	// LoadMergedProviders is idempotent with no DB rows — boots without
	// oidc_providers entries produce a registry identical to env.
	merged, err := config.LoadMergedProviders(ctx, cfg.Providers, queries)
	if err != nil {
		slog.ErrorContext(ctx, "oidc provider merge failed", "error", err)
		os.Exit(1)
	}
	cfg.Providers = config.MergedRegistry(merged)

	// Build the OAuth orchestrator. Per-provider discovery failures are
	// captured in ProviderState.Err and logged; the bad provider is simply
	// omitted from EnabledProviders(). Only construction-level problems
	// (missing JWT_SECRET, missing OAUTH_REDIRECT_BASE_URL) are still fatal
	// because no amount of provider toggling can recover from them.
	oauth, err := auth.NewOAuth(ctx, cfg)
	if err != nil {
		slog.ErrorContext(ctx, "oauth init failed", "error", err)
		os.Exit(1)
	}
	slog.InfoContext(ctx, "oauth initialized",
		"status", oauth.Status(),
		"enabled_providers", len(oauth.EnabledProviders()),
		"total_providers", len(oauth.AllProviders()),
	)

	// az is assigned after opReg is built (further below), and adminCheck
	// after the bootstrap service. The AdminChecker closure only runs at
	// HTTP request time, well after all initialization completes, so
	// adminCheck is always non-nil when the closure executes.
	var (
		az         *localAuthz.Authorizer
		adminCheck func(r *http.Request) (bool, error)
	)

	// Build the onboarding handler + state cache. The handler owns the
	// oidc_config row and the derived BootState; RequireOIDCConfirmed
	// reads its CurrentState closure on every /v1/* request.
	onboarding := handlers.NewOIDCConfigHandler(handlers.OIDCConfigDeps{
		Queries:      queries,
		OAuth:        oauth,
		EnvRegistry:  cfg.Providers,
		EnvNoOIDCEnv: oauth.EnvNoOIDCAccounts(),
		TokenDisplay: cfg.Onboarding.TokenDisplay,
		// AdminChecker lets an operator administrator configure OIDC without
		// a setup token: a session whose acting profile is an operator
		// profile holding wildcard manage (handlers.NewOperatorAdminChecker).
		// Returns (false, nil) on missing/invalid auth so /confirm falls
		// through to the setup-token check (accepted only while the
		// bootstrap or the OIDC confirmation is pending, decision U9);
		// surfaces internal faults as errors so they become 500 instead of
		// being masked.
		AdminChecker: func(r *http.Request) (bool, error) {
			return adminCheck(r)
		},
		// Platform decides whether the bootstrap is pending: the setup token
		// stays active until the operator party is set and OIDC is confirmed.
		Platform: coreQueries,
	})
	if err := onboarding.RefreshState(ctx); err != nil {
		slog.ErrorContext(ctx, "oidc_config: initial state load failed", "error", err)
		os.Exit(1)
	}
	// Replay DB overrides on top of the env-built registry so a prior
	// "microsoft off" confirmation sticks across restarts.
	if err := onboarding.ApplyDBOverridesToOAuth(ctx); err != nil {
		slog.ErrorContext(ctx, "oidc_config: apply DB overrides failed", "error", err)
		os.Exit(1)
	}

	// Setup-token + state-display lifecycle. The one setup token serves the
	// bootstrap and the OIDC configuration and is active while either is
	// pending (decision U9). TOKEN_DISPLAY=none is the production-strict
	// escape hatch — revert to Phase 9.1's fail-fast if the OIDC state is
	// unconfirmed; onboarding endpoints are NOT mounted regardless of
	// whether this exits.
	if cfg.Onboarding.TokenDisplay == config.TokenDisplayNone {
		if !onboarding.CurrentState().Confirmed() {
			slog.ErrorContext(ctx, "TOKEN_DISPLAY=none and OIDC state is unconfirmed — exiting per fail-fast policy",
				"state", string(onboarding.CurrentState()),
			)
			for _, s := range oauth.AllProviders() {
				if !s.InitOK {
					slog.ErrorContext(ctx, "provider init failed",
						"provider", s.ID,
						"error", s.Err,
					)
				}
			}
			os.Exit(1)
		}
		if !onboarding.Bootstrapped() {
			slog.WarnContext(ctx, "bootstrap pending but TOKEN_DISPLAY=none: no setup token is shown, so POST /v1/bootstrap cannot succeed",
				"bootstrap_pending", true,
			)
		}
	} else {
		// Ensure the setup token is active iff the state calls for
		// it. EnsureSetupToken returns a non-empty plaintext in two
		// cases: first-boot (no prior hash) and restart-while-active
		// (prior hash present but the plaintext was unrecoverable, so
		// the token is rotated to give ops a fresh recoverable value).
		// Both cases should trigger a fresh banner.
		plain, err := onboarding.EnsureSetupToken(ctx)
		if err != nil {
			slog.ErrorContext(ctx, "oidc_config: ensure setup token", "error", err)
			os.Exit(1)
		}
		if plain != "" {
			if cfg.Onboarding.TokenDisplay == config.TokenDisplayStderr ||
				cfg.Onboarding.TokenDisplay == config.TokenDisplayBoth {
				auth.PrintSetupTokenBanner(plain, cfg.Server.GUIBaseURL+"/oidc-config")
			}
			if cfg.Onboarding.TokenDisplay == config.TokenDisplayLocalhost {
				// Structured log only; the banner is stderr-exclusive.
				slog.ErrorContext(ctx, "first-run setup required: setup token ready for the bootstrap and OIDC configuration (use /v1/oidc-config/setup-token from loopback)",
					"setup_token_required", true,
					"bootstrap_route", "/v1/bootstrap",
				)
			}
		}
	}

	// Initialize the field cipher for SSN/EIN encryption. NewFromEnvOrGenerate
	// bootstraps the cipher from the DB-backed key table, generating and
	// persisting the first key when none exists yet — it does not fail fast
	// on a missing key. Its signature is fixed by moduleforge.module.yaml's
	// cipher service block and must not change.
	fieldCipher, err := fieldcrypto.NewFromEnvOrGenerate(ctx, coredb.New(pool))
	if err != nil {
		slog.ErrorContext(ctx, "field cipher init failed", "error", err)
		os.Exit(1)
	}

	// Build display renderer registry. Only core builtins are registered here;
	// peer modules (tags, contacts, etc.) are composed at the application layer,
	// not from inside users-module.
	displayReg := display.NewRegistry(coredb.New(pool))
	coreservice.RegisterBuiltins(displayReg, coredb.New(pool))

	// Build the TypeResolver (startup-time slug→typeID cache) and EntityResolver
	// (UUID→internal ID lookup with 403-on-missing policy). Both are required by
	// coreservice.New after Phase E.
	typeResolver, err := types.New(ctx, coredb.New(pool))
	if err != nil {
		slog.ErrorContext(ctx, "type resolver init failed", "error", err)
		os.Exit(1)
	}
	entityResolver := entity.NewResolver()

	// Load the OperationRegistry from the authz_operations table. This must run
	// after migrations (which seed the operations rows) and before ApplyFuncs
	// (which uses the registry to build GrantTableGenerator bodies). The registry
	// caches the SatisfiedBy closure; it is also injected into services that
	// issue list queries so they can compute op_ids slices at call time.
	opReg, err := authzapi.NewOperationRegistry(ctx, authzdb.New(pool))
	if err != nil {
		slog.ErrorContext(ctx, "authz operation registry load failed", "error", err)
		os.Exit(1)
	}

	// Apply access-function bodies for row-level scoping. Each peer module
	// ships stub functions in its migrations; here we replace those stubs
	// with the policy bodies from the GrantTableGenerator (Phase 2.2),
	// filtered through mod-users' owning-party row filter.
	// The DDL is idempotent; safe to run on every startup.
	//
	// The slug list must match all resources that have a list query JOINing
	// an access function. legal_entity composes natural_person + corporation;
	// contacts list queries JOIN accessible_legal_entity_ids_for_actor.
	//
	// See core-module/docs/architecture/authorization-design.md "Row-level
	// scoping" for the architectural rationale.
	authzSlugs := []string{
		"natural_person",
		"organization",
		"user",
		"profile",
		"service_account",
		"legal_entity",
		"tag",
		"authz_actor_group",
		"authz_target_group",
	}
	// WHY the hooked generator: single-row Authorize applies the owning-party
	// gate to every target with an owner, so every slug's list function must
	// drop the same rows. AccessFuncGenerator is setup.NewRowFilterGenerator(
	// setup.NewGrantTableGenerator(), "mod_users.access_row_permitted"), the
	// same constructor host composition roots use, so the dev server and the
	// hosts install one definition. IMPLIES: every list query that joins one
	// of these functions must run in a transaction bound with
	// setup.BindAuthInputs, or it silently drops the gated rows; a constructor
	// error (an invalid predicate name) fails startup rather than installing
	// unfiltered functions.
	accessGen, err := localAuthz.AccessFuncGenerator()
	if err != nil {
		slog.ErrorContext(ctx, "authz access-function generator failed", "error", err)
		os.Exit(1)
	}
	if err := setup.ApplyFuncs(ctx, pool, accessGen, authzSlugs); err != nil {
		slog.ErrorContext(ctx, "authz access-function setup failed", "error", err)
		os.Exit(1)
	}

	// Build the Authorizer. The grants-driven Authorizer uses the OperationRegistry,
	// a wildcard-grant check, and a recursive-CTE grant check for targeted grants.
	// Wildcard grants (target_id IS NULL in the grants table) allow an actor to
	// pass any authorization check.
	az = localAuthz.New(authzdb.New(pool), opReg, pool)

	// The per-request liveness check evaluates the invite-only app gate through
	// the same Authorizer (decision U15).
	authenticator.SetAppGate(az)

	// Build the audit-module Observer and compose it into an ObserverGroup.
	// The audit Observer writes one audit_log row inside the operation's transaction,
	// providing transactional consistency. This is the only place in users-module
	// that imports audit-module; service code in internal/ remains agnostic.
	auditObserver := auditservice.New(func(tx pgx.Tx) *auditdb.Queries {
		return auditdb.New(tx)
	})
	observerGroup := observer.NewObserverGroup(auditObserver)

	// Build audit-module's read service and HTTP handler. These serve
	// GET /v1/audit, /v1/audit/by-actor/{uuid}, /v1/audit/by-entity/{entity_uuid}.
	auditSvcs := auditservice.NewServices(auditdb.New(pool), coredb.New(pool), az)
	auditHandler := audithttpapi.NewAuditHandler(auditSvcs.Audit)

	// Build authz-module services. These serve the admin authz management API
	// under /v1/authz: operations, actor groups, target groups, and grants.
	// typeResolver must be fully loaded before this point (it is, see above).
	// The outside-collaborator gate is the trailing argument: a profile outside
	// a party's own can join the party's actor groups or be named as a grant
	// actor over its content only when its holder's credentials meet the
	// party's authentication policy or the caller confirms the mismatch.
	collaboratorGate := usersservice.NewCollaboratorGate()
	authzSvcs := authzservice.NewFromArgs(
		pool,
		authzdb.New(pool),
		coredb.New(pool),
		az,
		observerGroup,
		typeResolver,
		opReg,
		collaboratorGate,
	)

	// Build core services and router. coreSvcs delegates entity CRUD to the
	// service layer; coreRouter mounts /entities/* routes (including /self).
	coreSvcs := coreservice.New(coredb.New(pool), pool, az, observerGroup, fieldCipher, entityResolver, typeResolver)
	coreRouter := corehttpapi.NewRouter(corehttpapi.Deps{
		Services: coreSvcs,
		Logger:   logger,
	})

	// Build email sender. SMTP is optional — deployments that never send
	// email (e.g. a personal single-owner instance) can leave it entirely
	// unconfigured, in which case config.Load already logged a boot
	// warning and we fall back to a no-op sender so registration/reset
	// flows still work, minus the actual email delivery.
	var emailSender email.Sender
	if cfg.SMTP.Host == "" {
		emailSender = email.NoOpSender{}
	} else {
		emailSender = email.NewSMTPSender(
			cfg.SMTP.Host,
			cfg.SMTP.Port,
			cfg.SMTP.From,
			cfg.SMTP.User,
			cfg.SMTP.Pass,
		)
	}

	// Build server + router.
	srv, r := server.New(cfg)

	// Health endpoints (unauthenticated).
	r.Get("/healthz", handlers.Live)
	r.Get("/readyz", handlers.Ready(pool))

	// The signer for the EdDSA access tokens every sign-in mints.
	jwtSigner, err := auth.NewSigner(cfg.LocalAuth)
	if err != nil {
		slog.ErrorContext(ctx, "jwt signer init failed", "error", err)
		os.Exit(1)
	}

	// Identity services. Credentials and challenges first; sessions need both;
	// the user service is wired for self-service last because it needs the
	// session and challenge services.
	challengeSvc := usersservice.NewChallengeService(queries, emailSender)
	credSvc := usersservice.NewCredentialService(queries, coreQueries, az, observerGroup, auth.HashPassword)
	sessionSvc := usersservice.NewSessionService(
		pool, queries, coreQueries, az, observerGroup, jwtSigner, challengeSvc, usersservice.SessionConfigFrom(cfg),
	)
	// The constructor also registers the user and profile display renderers on
	// displayReg, the same path the generated composition root takes.
	userSvc := usersservice.NewUserServiceWithSelfService(
		pool, queries, coreQueries, az, observerGroup, coreSvcs.NaturalPerson, credSvc,
		sessionSvc, challengeSvc, opReg, cfg.Auth.SelfServiceMaxAuthAge, displayReg,
	)

	// impersonationService: impersonation as a child session of the
	// administrator's own (POST /v1/impersonations); its lifetimes ride on the
	// session configuration.
	impersonationSvc := usersservice.NewImpersonationService(pool, queries, az, observerGroup, sessionSvc)

	// The party profile lifecycle: invitations, managed users, departure
	// cleanup. Other modules (mod-billing seats) subscribe with
	// profileSvc.AddLifecycleObserver.
	profileSvc := usersservice.NewProfileService(pool, queries, coreQueries, az, observerGroup, userSvc, credSvc, sessionSvc)
	// partyPolicyService: the party authentication policy, credential
	// authorizations and confinements behind /v1/parties/{party_uuid}.
	partyPolicySvc := usersservice.NewPartyPolicyService(pool, coreQueries, az, observerGroup, userSvc)
	// personalPartyService: EnsurePersonalParty, a Go API for mod-billing's
	// personal team plans. It has no HTTP route, so the dev server only
	// constructs it to keep the wiring identical to the manifest's.
	_ = usersservice.NewPersonalPartyService(observerGroup)
	// bootstrapService: the first-run bootstrap (POST /v1/bootstrap) and the
	// operator-administrator check behind the OIDC configuration routes.
	bootstrapSvc := usersservice.NewBootstrapService(pool, az, observerGroup, userSvc, credSvc, sessionSvc, profileSvc)
	adminCheck = handlers.NewOperatorAdminChecker(authenticator, bootstrapSvc)

	// Handlers.
	authHandler := authhandlers.New(
		pool, queries, coreQueries, userSvc, credSvc, challengeSvc, sessionSvc, oauth, emailSender, cfg.Server.GUIBaseURL,
	)
	oidcHandler := authhandlers.NewOIDCHandler(
		pool, queries, coreQueries, oauth, auth.NewOIDCResolver(queries),
		userSvc, credSvc, challengeSvc, sessionSvc, observerGroup, cfg,
	)
	meHandler := me.NewMeHandler(userSvc, profileSvc)
	sessionHandler := session.NewSessionHandler(sessionSvc)
	impersonationHandler := handlers.NewImpersonationHandler(impersonationSvc)
	principalsHandler := principals.NewPrincipalsHandler(userSvc)
	partiesHandler := parties.NewPartiesHandler(profileSvc, partyPolicySvc)
	bootstrapHandler := handlers.NewBootstrapHandler(bootstrapSvc, onboarding)
	providersHandler := handlers.NewProvidersHandler(handlers.ProvidersDeps{
		Queries:      queries,
		EnvRegistry:  cfg.Providers,
		OAuth:        oauth,
		RedirectBase: cfg.Auth.OAuthRedirectBaseURL,
		Parties:      coreQueries,
		Confirmer:    onboarding,
	})

	// Onboarding endpoints. Mounted only when TOKEN_DISPLAY != none.
	// They must be reachable even when state is unconfirmed (the whole
	// point), so they sit OUTSIDE the RequireOIDCConfirmed gate.
	if cfg.Onboarding.TokenDisplay != config.TokenDisplayNone {
		r.Route("/v1/oidc-config", func(r chi.Router) {
			handlers.RegisterOIDCConfigRoutes(r, onboarding, providersHandler)
		})
	}

	// First-run bootstrap: public, outside RequireOIDCConfirmed (the bootstrap
	// may precede the OIDC configuration) and outside RequireAuth (the
	// X-Setup-Token header is the credential).
	r.Route("/v1/bootstrap", func(r chi.Router) {
		handlers.RegisterBootstrapRoutes(r, bootstrapHandler)
	})

	// Everything else on /v1 — including local + OIDC auth — is gated
	// by RequireOIDCConfirmed. When TOKEN_DISPLAY=none the middleware
	// is effectively a no-op (we already exited on unconfirmed state),
	// but attaching it unconditionally keeps behavior consistent and
	// cheap (a CurrentState() read is a single atomic pointer load).
	requireConfirmed := auth.RequireOIDCConfirmed(onboarding.CurrentState)
	requireAuth := auth.NewRequireAuth(authenticator)

	// Public auth routes: no authentication middleware.
	r.Route("/v1/auth", func(r chi.Router) {
		r.Use(requireConfirmed)
		authhandlers.RegisterRoutes(r, authHandler, oidcHandler)
	})

	// Session routes: bearer-authenticated, including the OIDC step-up start.
	r.Route("/v1/session", func(r chi.Router) {
		r.Use(requireConfirmed)
		r.Use(requireAuth)
		session.RegisterSessionRoutes(r, sessionHandler)
		r.Post("/step-up/oidc/{provider}/start", oidcHandler.StepUpStart)
	})

	// Impersonation: start (POST /), renew and end the caller's impersonation
	// session. Authorization is enforced at the service layer.
	r.Route("/v1/impersonations", func(r chi.Router) {
		r.Use(requireConfirmed)
		r.Use(requireAuth)
		handlers.RegisterImpersonationRoutes(r, impersonationHandler)
	})

	// Self-service routes, including the OIDC credential-link start. Reachable
	// to accounts with an unverified email (the GUI renders the "verify your
	// email" page from them).
	r.Route("/v1/me", func(r chi.Router) {
		r.Use(requireConfirmed)
		r.Use(requireAuth)
		me.RegisterMeRoutes(r, meHandler)
		r.Post("/credentials/oidc/{provider}/start", oidcHandler.LinkStart)
	})

	r.Route("/v1/principals", func(r chi.Router) {
		r.Use(requireConfirmed)
		r.Use(requireAuth)
		principals.RegisterPrincipalRoutes(r, principalsHandler)
	})

	// Party administration: a party's profiles and managed users, and the
	// status of a party profile. Authorization is enforced at the service
	// layer.
	r.Route("/v1/parties", func(r chi.Router) {
		r.Use(requireConfirmed)
		r.Use(requireAuth)
		parties.RegisterPartyRoutes(r, partiesHandler)
	})
	r.Route("/v1/profiles", func(r chi.Router) {
		r.Use(requireConfirmed)
		r.Use(requireAuth)
		parties.RegisterProfileRoutes(r, partiesHandler)
	})

	r.Route("/v1", func(r chi.Router) {
		r.Use(requireConfirmed)
		r.Group(func(r chi.Router) {
			r.Use(requireAuth)

			// Everything else requires a verified email address.
			r.Group(func(r chi.Router) {
				r.Use(auth.NewRequireVerifiedEmail())

				// Core entity CRUD: /v1/entities/natural-persons, /organizations, etc.
				r.Mount("/", coreRouter)

				// Audit log endpoints (admin-only). Authorization is enforced at the
				// service layer by the Authorizer:
				//   GET /v1/audit                        — ListRecent (admin)
				//   GET /v1/audit/by-actor/{uuid}        — entries where uuid is the actor
				//   GET /v1/audit/by-entity/{entity_uuid} — entries where uuid is the target
				r.Route("/audit", func(r chi.Router) {
					audithttpapi.RegisterRoutes(r, auditHandler)
				})

				// Authz management endpoints. Authorization is enforced at the
				// service layer via Authorize("manage", nil). Routes:
				//   /v1/authz/operations — CRUD for operation definitions
				//   /v1/authz/actor-groups — CRUD + member management for actor groups
				//   /v1/authz/target-groups — CRUD + member management for target groups
				//   /v1/authz/grants — create / list / get / revoke grants
				r.Route("/authz", func(r chi.Router) {
					authzhttpapi.RegisterRoutes(r, authzSvcs)
				})
			})
		})
	})

	slog.InfoContext(ctx, "users-api starting", "addr", cfg.Server.Addr)

	// Start server in background.
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.ErrorContext(ctx, "server error", "error", err)
			os.Exit(1)
		}
	}()

	// Block until signal.
	<-ctx.Done()
	stop()

	slog.Info("shutdown signal received, beginning graceful shutdown")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer cancel()

	// Shutdown sequence: HTTP server → pool → OTel.
	slog.Info("shutting down server")
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	slog.Info("closing database pool")
	pool.Close()

	slog.Info("flushing otel telemetry")
	if err := otelShutdown(shutdownCtx); err != nil {
		slog.Error("otel shutdown error", "error", err)
	}

	slog.Info("shutdown complete")
}

func resolveLogLevel(level string) slog.Level {
	switch strings.ToUpper(level) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
