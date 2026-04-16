package config_test

import (
	"strings"
	"testing"

	"github.com/moduleforge/users-module/api/internal/config"
)

// resetProviderEnv clears every AUTH_PROVIDER_* / AUTH_PROVIDERS key that
// might bleed in from the parent shell. t.Setenv handles restoration.
func resetProviderEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AUTH_PROVIDERS", "")
	keys := []string{
		"AUTH_PROVIDER_GOOGLE_CLIENT_ID",
		"AUTH_PROVIDER_GOOGLE_CLIENT_SECRET",
		"AUTH_PROVIDER_GOOGLE_ISSUER_URL",
		"AUTH_PROVIDER_GOOGLE_CLAIM_STYLE",
		"AUTH_PROVIDER_GOOGLE_DISPLAY_NAME",
		"AUTH_PROVIDER_GOOGLE_SCOPES",
		"AUTH_PROVIDER_MICROSOFT_CLIENT_ID",
		"AUTH_PROVIDER_MICROSOFT_CLIENT_SECRET",
		"AUTH_PROVIDER_AUTHELIA_CLIENT_ID",
		"AUTH_PROVIDER_AUTHELIA_CLIENT_SECRET",
		"AUTH_PROVIDER_AUTHELIA_ISSUER_URL",
		"AUTH_PROVIDER_AUTHELIA_CLAIM_STYLE",
		"AUTH_PROVIDER_AUTHELIA_DISPLAY_NAME",
		"AUTH_PROVIDER_CUSTOM_CLIENT_ID",
		"AUTH_PROVIDER_CUSTOM_CLIENT_SECRET",
	}
	for _, k := range keys {
		t.Setenv(k, "")
	}
}

func TestLoadProviders(t *testing.T) {
	t.Run("zero providers configured returns empty registry", func(t *testing.T) {
		resetProviderEnv(t)

		reg, err := config.LoadProviders()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(reg) != 0 {
			t.Errorf("expected empty registry, got %v", reg)
		}
	})

	t.Run("google with defaults", func(t *testing.T) {
		resetProviderEnv(t)
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_ID", "gid")
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_SECRET", "gsecret")

		reg, err := config.LoadProviders()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		p, ok := reg["google"]
		if !ok {
			t.Fatalf("google missing; registry=%v", reg)
		}
		if p.IssuerURL != "https://accounts.google.com" {
			t.Errorf("IssuerURL = %q, want google default", p.IssuerURL)
		}
		if p.ClaimStyle != "google" {
			t.Errorf("ClaimStyle = %q, want %q", p.ClaimStyle, "google")
		}
		if p.DisplayName != "Google" {
			t.Errorf("DisplayName = %q, want %q", p.DisplayName, "Google")
		}
		if len(p.Scopes) != 3 {
			t.Errorf("Scopes = %v, want default three-scope set", p.Scopes)
		}
	})

	t.Run("microsoft with defaults", func(t *testing.T) {
		resetProviderEnv(t)
		t.Setenv("AUTH_PROVIDER_MICROSOFT_CLIENT_ID", "mid")
		t.Setenv("AUTH_PROVIDER_MICROSOFT_CLIENT_SECRET", "msecret")

		reg, err := config.LoadProviders()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		p, ok := reg["microsoft"]
		if !ok {
			t.Fatalf("microsoft missing; registry=%v", reg)
		}
		if p.IssuerURL != "https://login.microsoftonline.com/common/v2.0" {
			t.Errorf("IssuerURL = %q, want microsoft default", p.IssuerURL)
		}
		if p.ClaimStyle != "microsoft" {
			t.Errorf("ClaimStyle = %q, want %q", p.ClaimStyle, "microsoft")
		}
	})

	t.Run("authelia requires explicit issuer + claim_style", func(t *testing.T) {
		resetProviderEnv(t)
		t.Setenv("AUTH_PROVIDER_AUTHELIA_CLIENT_ID", "aid")
		t.Setenv("AUTH_PROVIDER_AUTHELIA_CLIENT_SECRET", "asecret")

		_, err := config.LoadProviders()
		if err == nil {
			t.Fatal("expected error for authelia without issuer/claim_style")
		}
		if !strings.Contains(err.Error(), "AUTH_PROVIDER_AUTHELIA_ISSUER_URL") {
			t.Errorf("error should mention missing issuer url: %v", err)
		}
	})

	t.Run("authelia fully configured", func(t *testing.T) {
		resetProviderEnv(t)
		t.Setenv("AUTH_PROVIDER_AUTHELIA_CLIENT_ID", "aid")
		t.Setenv("AUTH_PROVIDER_AUTHELIA_CLIENT_SECRET", "asecret")
		t.Setenv("AUTH_PROVIDER_AUTHELIA_ISSUER_URL", "https://auth.example.com")
		t.Setenv("AUTH_PROVIDER_AUTHELIA_CLAIM_STYLE", "authelia")
		t.Setenv("AUTH_PROVIDER_AUTHELIA_DISPLAY_NAME", "Authelia")

		reg, err := config.LoadProviders()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		p, ok := reg["authelia"]
		if !ok {
			t.Fatalf("authelia missing; registry=%v", reg)
		}
		if p.IssuerURL != "https://auth.example.com" {
			t.Errorf("IssuerURL = %q, want explicit value", p.IssuerURL)
		}
		if p.ClaimStyle != "authelia" {
			t.Errorf("ClaimStyle = %q, want %q", p.ClaimStyle, "authelia")
		}
	})

	t.Run("display_name falls back to title-case of id", func(t *testing.T) {
		// No DISPLAY_NAME set → fallback path. authelia has no built-in default,
		// so the last-resort titleCase("authelia") should yield "Authelia".
		resetProviderEnv(t)
		t.Setenv("AUTH_PROVIDER_AUTHELIA_CLIENT_ID", "aid")
		t.Setenv("AUTH_PROVIDER_AUTHELIA_CLIENT_SECRET", "asecret")
		t.Setenv("AUTH_PROVIDER_AUTHELIA_ISSUER_URL", "https://auth.example.com")
		t.Setenv("AUTH_PROVIDER_AUTHELIA_CLAIM_STYLE", "authelia")

		reg, err := config.LoadProviders()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		p, ok := reg["authelia"]
		if !ok {
			t.Fatalf("authelia missing; registry=%v", reg)
		}
		if p.DisplayName != "Authelia" {
			t.Errorf("DisplayName = %q, want %q (not ALL-CAPS)", p.DisplayName, "Authelia")
		}
	})

	t.Run("missing client_secret is a hard error", func(t *testing.T) {
		resetProviderEnv(t)
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_ID", "gid")

		_, err := config.LoadProviders()
		if err == nil {
			t.Fatal("expected error for missing client_secret")
		}
		if !strings.Contains(err.Error(), "AUTH_PROVIDER_GOOGLE_CLIENT_SECRET") {
			t.Errorf("error should mention missing secret: %v", err)
		}
	})

	t.Run("AUTH_PROVIDERS limits candidates", func(t *testing.T) {
		resetProviderEnv(t)
		// Both google and microsoft are set, but AUTH_PROVIDERS only lists google.
		t.Setenv("AUTH_PROVIDERS", "google")
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_ID", "gid")
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_SECRET", "gsecret")
		t.Setenv("AUTH_PROVIDER_MICROSOFT_CLIENT_ID", "mid")
		t.Setenv("AUTH_PROVIDER_MICROSOFT_CLIENT_SECRET", "msecret")

		reg, err := config.LoadProviders()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := reg["google"]; !ok {
			t.Errorf("expected google in registry, got %v", reg)
		}
		if _, ok := reg["microsoft"]; ok {
			t.Errorf("microsoft should be excluded by AUTH_PROVIDERS allowlist")
		}
	})

	t.Run("AUTH_PROVIDERS with missing client_id is silently skipped", func(t *testing.T) {
		resetProviderEnv(t)
		t.Setenv("AUTH_PROVIDERS", "google,microsoft")
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_ID", "gid")
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_SECRET", "gsecret")

		reg, err := config.LoadProviders()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(reg) != 1 {
			t.Errorf("expected 1 provider, got %d: %v", len(reg), reg)
		}
	})

	t.Run("explicit scopes override defaults", func(t *testing.T) {
		resetProviderEnv(t)
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_ID", "gid")
		t.Setenv("AUTH_PROVIDER_GOOGLE_CLIENT_SECRET", "gsecret")
		t.Setenv("AUTH_PROVIDER_GOOGLE_SCOPES", "openid,email")

		reg, err := config.LoadProviders()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		p := reg["google"]
		if len(p.Scopes) != 2 || p.Scopes[0] != "openid" || p.Scopes[1] != "email" {
			t.Errorf("Scopes = %v, want [openid email]", p.Scopes)
		}
	})
}
