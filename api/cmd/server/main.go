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

	"github.com/moduleforge/users-module/api/internal/audit"
	"github.com/moduleforge/users-module/api/internal/auth"
	"github.com/moduleforge/users-module/api/internal/config"
	localdb "github.com/moduleforge/users-module/api/internal/db"
	"github.com/moduleforge/users-module/api/internal/email"
	"github.com/moduleforge/users-module/api/internal/handlers"
	authhandlers "github.com/moduleforge/users-module/api/internal/handlers/auth"
	"github.com/moduleforge/users-module/api/internal/observability"
	"github.com/moduleforge/users-module/api/internal/server"
	db "github.com/moduleforge/users-module/model/db"
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

	// Build auth components. The Verifier is used by RequireAuth to validate
	// incoming Bearer tokens — post-Phase 9 those are always the local JWTs
	// minted by /v1/auth/login or the OIDC callback, never a raw provider
	// id_token. So the verifier is local-only.
	verifier, err := auth.NewVerifier(ctx, "", "", cfg.LocalAuth.JWTSecret, cfg.LocalAuth.LocalIssuer)
	if err != nil {
		slog.ErrorContext(ctx, "auth verifier init failed", "error", err)
		os.Exit(1)
	}

	// RequireAuth needs a ClaimMapper to turn the local JWT's claims into a
	// Principal. The local JWT uses flat "email" + "roles" claims, which the
	// generic mapper handles with the pass-through paths below. After Phase 9,
	// inbound Bearer tokens are always these locally-minted JWTs — provider
	// id_tokens are traded for a local JWT by the OIDC callback, not presented
	// directly to API endpoints.
	localMapper, err := auth.NewClaimMapper("generic", auth.MapperOptions{
		AdminRole: cfg.Auth.AdminRole,
		EmailPath: "email",
		RolesPath: "roles",
	})
	if err != nil {
		slog.ErrorContext(ctx, "local claim mapper init failed", "error", err)
		os.Exit(1)
	}

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

	resolver := auth.NewUserResolver(pool, queries, cfg.Auth.AdminRole, cfg.LocalAuth.LocalIssuer)
	auditWriter := audit.New(queries)

	// Build email sender.
	emailSender := email.NewSMTPSender(
		cfg.SMTP.Host,
		cfg.SMTP.Port,
		cfg.SMTP.From,
		cfg.SMTP.User,
		cfg.SMTP.Pass,
	)

	// Build server + router.
	srv, r := server.New(cfg)

	// Health endpoints (unauthenticated).
	r.Get("/healthz", handlers.Live)
	r.Get("/readyz", handlers.Ready(pool))

	// Local auth handlers (unauthenticated).
	authHandler := authhandlers.New(
		pool,
		queries,
		auditWriter,
		cfg.LocalAuth.JWTSecret,
		cfg.LocalAuth.LocalIssuer,
		emailSender,
		cfg.Server.GUIBaseURL,
	)

	oidcHandler := authhandlers.NewOIDCHandler(queries, oauth, resolver, cfg)

	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/register", authHandler.Register)
		r.Post("/login", authHandler.Login)
		r.Post("/email-code/request", authHandler.EmailCodeRequest)
		r.Post("/email-code/verify", authHandler.EmailCodeVerify)
		r.Post("/password-reset/request", authHandler.PasswordResetRequest)
		r.Post("/password-reset/confirm", authHandler.PasswordResetConfirm)

		// OIDC provider discovery + authorization-code flow (unauthenticated).
		r.Get("/providers", oidcHandler.ListProviders)
		r.Get("/oidc/{provider}/start", oidcHandler.Start)
		r.Get("/oidc/{provider}/callback", oidcHandler.Callback)
	})

	// Handlers for authenticated routes.
	selfHandler := handlers.NewSelfHandler(queries, auditWriter)
	usersHandler := handlers.NewUsersHandler(pool, queries, auditWriter)
	assumeHandler := handlers.NewAssumeHandler(queries, cfg.LocalAuth.JWTSecret, cfg.LocalAuth.LocalIssuer)
	auditHandler := handlers.NewAuditHandler(queries)
	appsHandler := handlers.NewAppsHandler(queries, auditWriter)

	r.Route("/v1", func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAuth(verifier, localMapper, resolver))

			// Self endpoints (any authenticated user).
			r.Get("/self", selfHandler.Get)
			r.Put("/self", selfHandler.Put)

			// Assume identity (admin).
			r.Delete("/assume", assumeHandler.EndAssume)

			// Admin-only routes.
			r.Group(func(r chi.Router) {
				r.Use(auth.RequireAdmin)

				// User management.
				r.Get("/users", usersHandler.List)
				r.Post("/users", usersHandler.Create)
				r.Get("/users/{uuid}", usersHandler.Get)
				r.Put("/users/{uuid}", usersHandler.Update)
				r.Delete("/users/{uuid}", usersHandler.Delete)
				r.Post("/users/{uuid}/grant-admin", usersHandler.GrantAdmin)
				r.Post("/users/{uuid}/revoke-admin", usersHandler.RevokeAdmin)
				r.Post("/users/{uuid}/assume", assumeHandler.Assume)

				// Audit log.
				r.Get("/users/{uuid}/audit", auditHandler.ByUser)
				r.Get("/audit/{entity_uuid}", auditHandler.ByEntity)

				// Apps (multi-tenancy).
				r.Post("/apps", appsHandler.Create)
				r.Get("/apps", appsHandler.List)
				r.Get("/apps/{uuid}", appsHandler.GetApp)
				r.Put("/apps/{uuid}", appsHandler.UpdateApp)
				r.Delete("/apps/{uuid}", appsHandler.DeleteApp)

				// Apps users.
				r.Post("/apps/{uuid}/users", appsHandler.AssignUser)
				r.Get("/apps/{uuid}/users", appsHandler.ListAppUsers)
				r.Delete("/apps/{uuid}/users/{user_uuid}", appsHandler.RemoveUser)
				r.Put("/apps/{uuid}/users/{user_uuid}/roles", appsHandler.UpdateUserRoles)
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
