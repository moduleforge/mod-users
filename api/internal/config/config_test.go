package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/moduleforge/users-module/api/internal/config"
)

// requiredEnv is a complete set of required environment variables with
// valid values. Tests derive from this by adding or removing keys.
var requiredEnv = map[string]string{
	"DB_URL":             "postgres://user:pass@localhost:5432/users",
	"OIDC_ISSUER_URL":    "http://auth.example.com",
	"OIDC_CLIENT_ID":     "client-id",
	"OIDC_CLIENT_SECRET": "client-secret",
	"OIDC_CLAIM_STYLE":   "authelia",
	"JWT_SECRET":         "supersecretkey",
	"SMTP_HOST":          "smtp.example.com",
	"SMTP_PORT":          "1025",
	"SMTP_FROM":          "no-reply@example.com",
	"DEPLOY_MODE":        "local",
}

// setEnv applies the given map to the process environment and returns a
// cleanup function that restores the previous values.
func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for k, v := range env {
		t.Setenv(k, v)
	}
}

func TestLoad(t *testing.T) {
	t.Run("all required fields set succeeds", func(t *testing.T) {
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
		if cfg.OIDC.ClaimStyle != "authelia" {
			t.Errorf("OIDC.ClaimStyle = %q, want %q", cfg.OIDC.ClaimStyle, "authelia")
		}
	})

	t.Run("missing required fields produces aggregated error", func(t *testing.T) {
		// Set none of the required vars; ensure the error lists all of them.
		// t.Setenv ensures cleanup even on test failure.
		for k := range requiredEnv {
			t.Setenv(k, "")
		}

		_, err := config.Load()
		if err == nil {
			t.Fatal("expected an error for missing required fields, got nil")
		}

		required := []string{
			"DB_URL",
			"OIDC_ISSUER_URL",
			"OIDC_CLIENT_ID",
			"OIDC_CLIENT_SECRET",
			"OIDC_CLAIM_STYLE",
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

	t.Run("serverless mode sets MaxConns=4", func(t *testing.T) {
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
		setEnv(t, requiredEnv)
		t.Setenv("DEPLOY_MODE", "local") // default would be 20
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
