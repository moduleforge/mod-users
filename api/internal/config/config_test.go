package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// requiredEnv is a complete set of required environment variables with valid
// values that exercise the local-auth-only happy path. Tests derive from this
// by adding provider keys or clearing specific fields.
var requiredEnv = map[string]string{
	"DB_URL":      "postgres://user:pass@localhost:5432/users",
	"JWT_SECRET":  "supersecretkey",
	"SMTP_HOST":   "smtp.example.com",
	"SMTP_PORT":   "1025",
	"SMTP_FROM":   "no-reply@example.com",
	"DEPLOY_MODE": "local",
}

// setEnv applies the given map to the process environment. t.Setenv handles
// cleanup automatically on test completion.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// clearProviderEnv wipes AUTH_PROVIDERS and any AUTH_PROVIDER_*_CLIENT_ID keys
// so auto-discovery does not pick up state leaked from the parent process.
// t.Setenv restores the originals when the test finishes.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_PROVIDERS", "")
	// Clear commonly-set provider envs. Tests that need specific providers
	// will set them explicitly after this call.
	providerKeys := []string{
		"AUTH_PROVIDER_GOOGLE_CLIENT_ID",
		"AUTH_PROVIDER_GOOGLE_CLIENT_SECRET",
		"AUTH_PROVIDER_MICROSOFT_CLIENT_ID",
		"AUTH_PROVIDER_MICROSOFT_CLIENT_SECRET",
		"AUTH_PROVIDER_AUTHELIA_CLIENT_ID",
		"AUTH_PROVIDER_AUTHELIA_CLIENT_SECRET",
		"AUTH_PROVIDER_AUTHELIA_ISSUER_URL",
		"AUTH_PROVIDER_AUTHELIA_CLAIM_STYLE",
	}
	for _, k := range providerKeys {
		t.Setenv(k, "")
	}
}

func TestLoad(t *testing.T) {
	t.Run("all required fields set succeeds with zero providers", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if cfg == nil {
			t.Fatal("expected non-nil config")
		}
		if cfg.DB.URL != requiredEnv["DB_URL"] {
			t.Errorf("DB.URL = %q, want %q", cfg.DB.URL, requiredEnv["DB_URL"])
		}
		if len(cfg.Providers) != 0 {
			t.Errorf("Providers = %v, want empty registry", cfg.Providers)
		}
		if cfg.Auth.AdminRole != "admin" {
			t.Errorf("Auth.AdminRole = %q, want %q", cfg.Auth.AdminRole, "admin")
		}
	})

	t.Run("missing required fields produces aggregated error", func(t *testing.T) {
		clearProviderEnv(t)
		for k := range requiredEnv {
			t.Setenv(k, "")
		}

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error for missing required fields, got nil")
		}

		// Without providers, only the always-required fields must be listed.
		required := []string{
			"DB_URL",
			"JWT_SECRET",
			"SMTP_HOST",
			"SMTP_PORT",
			"SMTP_FROM",
		}
		for _, field := range required {
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error should mention %q, got: %v", field, err)
			}
		}
	})

	t.Run("enabled provider requires oauth redirect + frontend return URL", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_ID", "google-client")
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_SECRET", "google-secret")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected error when provider enabled without redirect URLs, got nil")
		}
		for _, field := range []string{"AUTH_FRONTEND_RETURN_URL", "AUTH_OAUTH_REDIRECT_BASE_URL"} {
			if !strings.Contains(err.Error(), field) {
				t.Errorf("error should mention %q, got: %v", field, err)
			}
		}
	})

	t.Run("enabled provider with redirect URLs succeeds", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_ID", "google-client")
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_SECRET", "google-secret")
		t.Setenv("AUTH_FRONTEND_RETURN_URL", "http://localhost:3000/auth/oidc/return")
		t.Setenv("AUTH_OAUTH_REDIRECT_BASE_URL", "http://localhost:8080")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		p, ok := cfg.Providers["google"]
		if !ok {
			t.Fatalf("expected google provider, got %v", cfg.Providers)
		}
		if p.ClaimStyle != "google" {
			t.Errorf("ClaimStyle = %q, want %q (built-in default)", p.ClaimStyle, "google")
		}
		if p.DisplayName != "Google" {
			t.Errorf("DisplayName = %q, want %q", p.DisplayName, "Google")
		}
	})

	t.Run("serverless mode sets MaxConns=4", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("DEPLOY_MODE", "serverless")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.DB.MaxConns != 4 {
			t.Errorf("DB.MaxConns = %d, want 4 for serverless", cfg.DB.MaxConns)
		}
	})

	t.Run("non-serverless mode sets MaxConns=20", func(t *testing.T) {
		for _, mode := range []string{"local", "k8s"} {
			mode := mode
			t.Run(mode, func(t *testing.T) {
				clearProviderEnv(t)
				setEnv(t, requiredEnv)
				t.Setenv("DEPLOY_MODE", mode)

				cfg, err := config.Load()
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if cfg.DB.MaxConns != 20 {
					t.Errorf("DB.MaxConns = %d, want 20 for mode %q", cfg.DB.MaxConns, mode)
				}
			})
		}
	})

	t.Run("explicit DB_MAX_CONNS overrides mode default", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("DEPLOY_MODE", "local")
		t.Setenv("DB_MAX_CONNS", "10")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.DB.MaxConns != 10 {
			t.Errorf("DB.MaxConns = %d, want 10 (explicit override)", cfg.DB.MaxConns)
		}
	})

	t.Run("defaults are applied when optional env vars are absent", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if cfg.Server.Addr != ":8080" {
			t.Errorf("Server.Addr = %q, want %q", cfg.Server.Addr, ":8080")
		}
		if cfg.Server.ShutdownTimeout != 25*time.Second {
			t.Errorf("Server.ShutdownTimeout = %v, want 25s", cfg.Server.ShutdownTimeout)
		}
		if cfg.DB.MaxConnLifetime != 5*time.Minute {
			t.Errorf("DB.MaxConnLifetime = %v, want 5m", cfg.DB.MaxConnLifetime)
		}
		if cfg.DB.MaxConnIdleTime != 1*time.Minute {
			t.Errorf("DB.MaxConnIdleTime = %v, want 1m", cfg.DB.MaxConnIdleTime)
		}
		if cfg.LocalAuth.EmailCodeTTL != 5*time.Minute {
			t.Errorf("LocalAuth.EmailCodeTTL = %v, want 5m", cfg.LocalAuth.EmailCodeTTL)
		}
		if cfg.LocalAuth.PasswordResetTTL != 30*time.Minute {
			t.Errorf("LocalAuth.PasswordResetTTL = %v, want 30m", cfg.LocalAuth.PasswordResetTTL)
		}
		if cfg.LocalAuth.LocalIssuer != "users-module-local" {
			t.Errorf("LocalAuth.LocalIssuer = %q, want %q", cfg.LocalAuth.LocalIssuer, "users-module-local")
		}
		if cfg.OTel.ServiceName != "users-api" {
			t.Errorf("OTel.ServiceName = %q, want %q", cfg.OTel.ServiceName, "users-api")
		}
	})

	t.Run("explicit env override beats default", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("SERVER_ADDR", ":9090")
		t.Setenv("SERVER_SHUTDOWN_TIMEOUT", "10s")
		t.Setenv("OTEL_SERVICE_NAME", "my-custom-api")

		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Server.Addr != ":9090" {
			t.Errorf("Server.Addr = %q, want %q", cfg.Server.Addr, ":9090")
		}
		if cfg.Server.ShutdownTimeout != 10*time.Second {
			t.Errorf("Server.ShutdownTimeout = %v, want 10s", cfg.Server.ShutdownTimeout)
		}
		if cfg.OTel.ServiceName != "my-custom-api" {
			t.Errorf("OTel.ServiceName = %q, want %q", cfg.OTel.ServiceName, "my-custom-api")
		}
	})

	t.Run("malformed DB_MAX_CONNS produces validation error", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("DB_MAX_CONNS", "abc")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error for malformed DB_MAX_CONNS, got nil")
		}
		if !strings.Contains(err.Error(), "DB_MAX_CONNS") {
			t.Errorf("error should mention DB_MAX_CONNS, got: %v", err)
		}
		if !strings.Contains(err.Error(), "abc") {
			t.Errorf("error should include the bad value %q, got: %v", "abc", err)
		}
	})

	t.Run("malformed SERVER_SHUTDOWN_TIMEOUT produces validation error", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("SERVER_SHUTDOWN_TIMEOUT", "5 minutes")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error for malformed SERVER_SHUTDOWN_TIMEOUT, got nil")
		}
		if !strings.Contains(err.Error(), "SERVER_SHUTDOWN_TIMEOUT") {
			t.Errorf("error should mention SERVER_SHUTDOWN_TIMEOUT, got: %v", err)
		}
		if !strings.Contains(err.Error(), "5 minutes") {
			t.Errorf("error should include the bad value %q, got: %v", "5 minutes", err)
		}
	})

	t.Run("invalid DEPLOY_MODE produces validation error", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("DEPLOY_MODE", "docker-compose")

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error for invalid DEPLOY_MODE, got nil")
		}
		if !strings.Contains(err.Error(), "DEPLOY_MODE") {
			t.Errorf("error should mention DEPLOY_MODE, got: %v", err)
		}
		if !strings.Contains(err.Error(), "docker-compose") {
			t.Errorf("error should include the bad value %q, got: %v", "docker-compose", err)
		}
	})
}
