package config_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/moduleforge/mod-users/api/internal/config"
)

// unreachableDB refuses connections, so a signing-key bootstrap attempt fails
// fast and visibly.
const unreachableDB = "postgres://u:p@127.0.0.1:1/none?connect_timeout=2"

// genEd25519PEM generates a throwaway Ed25519 key and returns it with its
// PKCS#8 PEM encoding. No key material is checked in.
func genEd25519PEM(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return priv, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
}

// loadWith loads the config from requiredEnv (which carries a valid
// JWT_SIGNING_KEY) overlaid with extra, after clearing the optional signing
// variables and every AUTH_* lifetime and audience variable.
func loadWith(t *testing.T, extra map[string]string) (*config.Config, error) {
	t.Helper()
	clearProviderEnv(t)
	setEnv(t, requiredEnv)
	for _, k := range []string{
		"JWT_SIGNING_KID", "JWT_VERIFY_PUBLIC_KEYS", "MFAPP_DATABASE_URL",
		"AUTH_ACCESS_TOKEN_TTL", "AUTH_SESSION_MAX_AGE", "AUTH_SESSION_IDLE_TIMEOUT",
		"AUTH_CLIENT_CREDENTIALS_TTL", "AUTH_IMPERSONATION_TTL", "AUTH_IMPERSONATION_MAX",
		"AUTH_SELF_SERVICE_MAX_AUTH_AGE", "AUTH_DEFAULT_AUDIENCE", "AUTH_ACCEPTED_AUDIENCES",
	} {
		t.Setenv(k, "")
	}
	setEnv(t, extra)
	return config.Load()
}

func TestLoadJWTSigning(t *testing.T) {
	priv, pemStr := genEd25519PEM(t)

	t.Run("PEM key and kid", func(t *testing.T) {
		cfg, err := loadWith(t, map[string]string{"JWT_SIGNING_KEY": pemStr, "JWT_SIGNING_KID": "k1"})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.LocalAuth.JWTSigningKID != "k1" || !priv.Equal(cfg.LocalAuth.JWTSigningKey) {
			t.Errorf("signing config not applied: kid=%q", cfg.LocalAuth.JWTSigningKID)
		}
	})

	t.Run("escaped newlines in the PEM", func(t *testing.T) {
		escaped := strings.ReplaceAll(strings.TrimSpace(pemStr), "\n", `\n`)
		cfg, err := loadWith(t, map[string]string{"JWT_SIGNING_KEY": escaped})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !priv.Equal(cfg.LocalAuth.JWTSigningKey) {
			t.Error("parsed key does not match")
		}
	})

	t.Run("no key and no DB URL reports the missing key", func(t *testing.T) {
		_, err := loadWith(t, map[string]string{"JWT_SIGNING_KEY": "", "DB_URL": ""})
		if err == nil || !strings.Contains(err.Error(), "JWT_SIGNING_KEY") {
			t.Fatalf("expected JWT_SIGNING_KEY error, got %v", err)
		}
	})

	t.Run("no key bootstraps from the database", func(t *testing.T) {
		_, err := loadWith(t, map[string]string{"JWT_SIGNING_KEY": "", "DB_URL": unreachableDB})
		if err == nil || !strings.Contains(err.Error(), "JWT_SIGNING_KEY: bootstrap from database failed") {
			t.Fatalf("expected a bootstrap failure, got %v", err)
		}
	})

	failures := []struct {
		name    string
		env     map[string]string
		wantSub string
	}{
		{"garbage key", map[string]string{"JWT_SIGNING_KEY": "not-a-pem-SECRETMARKER", "DB_URL": unreachableDB}, "JWT_SIGNING_KEY"},
		{"kid with whitespace", map[string]string{"JWT_SIGNING_KID": "a b"}, "JWT_SIGNING_KID"},
		{"kid too long", map[string]string{"JWT_SIGNING_KID": strings.Repeat("k", 129)}, "JWT_SIGNING_KID"},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadWith(t, tc.env)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q missing %q", err, tc.wantSub)
			}
			if strings.Contains(err.Error(), "SECRETMARKER") {
				t.Errorf("error leaks key input: %v", err)
			}
			if strings.Contains(err.Error(), "bootstrap from database") {
				t.Errorf("an invalid JWT_SIGNING_KEY must not fall back to the persisted key: %v", err)
			}
		})
	}

	t.Run("PEM block with malformed DER rejected", func(t *testing.T) {
		bad := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("garbage")}))
		_, err := loadWith(t, map[string]string{"JWT_SIGNING_KEY": bad})
		if err == nil || !strings.Contains(err.Error(), "JWT_SIGNING_KEY") {
			t.Fatalf("expected JWT_SIGNING_KEY error, got %v", err)
		}
	})
}

// TestLoadJWTSecretOptional covers the JWT_SECRET contract: optional (only
// the OAuth state key), whitespace treated as unset, an explicit value kept.
func TestLoadJWTSecretOptional(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"unset":           {"", ""},
		"whitespace only": {"   ", ""},
		"explicit":        {"short", "short"},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := loadWith(t, map[string]string{"JWT_SECRET": tc.in})
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.LocalAuth.JWTSecret != tc.want {
				t.Errorf("JWTSecret = %q, want %q", cfg.LocalAuth.JWTSecret, tc.want)
			}
		})
	}
}

func TestLoadAuthLifetimesAndAudiences(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg, err := loadWith(t, nil)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		a := cfg.Auth
		got := []time.Duration{a.AccessTokenTTL, a.SessionMaxAge, a.SessionIdleTimeout, a.ClientCredentialsTTL,
			a.ImpersonationTTL, a.ImpersonationMax, a.SelfServiceMaxAuthAge}
		want := []time.Duration{time.Hour, 720 * time.Hour, 168 * time.Hour, 15 * time.Minute,
			30 * time.Minute, 60 * time.Minute, 10 * time.Minute}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("lifetime %d = %v, want %v", i, got[i], want[i])
			}
		}
		if a.DefaultAudience != config.DefaultAudience || a.AcceptedAudiences != nil {
			t.Errorf("audiences = %q / %v", a.DefaultAudience, a.AcceptedAudiences)
		}
	})

	t.Run("overrides", func(t *testing.T) {
		cfg, err := loadWith(t, map[string]string{
			"AUTH_ACCESS_TOKEN_TTL":   "20m",
			"AUTH_DEFAULT_AUDIENCE":   "portal",
			"AUTH_ACCEPTED_AUDIENCES": " portal, billing ,,",
		})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.Auth.AccessTokenTTL != 20*time.Minute || cfg.Auth.DefaultAudience != "portal" {
			t.Errorf("overrides not applied: %+v", cfg.Auth)
		}
		if strings.Join(cfg.Auth.AcceptedAudiences, "|") != "portal|billing" {
			t.Errorf("AcceptedAudiences = %v", cfg.Auth.AcceptedAudiences)
		}
	})

	for name, tc := range map[string]struct {
		env     map[string]string
		wantSub string
	}{
		"access ttl above 1h":     {map[string]string{"AUTH_ACCESS_TOKEN_TTL": "2h"}, "AUTH_ACCESS_TOKEN_TTL"},
		"zero duration":           {map[string]string{"AUTH_SESSION_MAX_AGE": "0s"}, "AUTH_SESSION_MAX_AGE"},
		"negative duration":       {map[string]string{"AUTH_CLIENT_CREDENTIALS_TTL": "-1m"}, "AUTH_CLIENT_CREDENTIALS_TTL"},
		"unparseable":             {map[string]string{"AUTH_SELF_SERVICE_MAX_AUTH_AGE": "soon"}, "AUTH_SELF_SERVICE_MAX_AUTH_AGE"},
		"impersonation max < ttl": {map[string]string{"AUTH_IMPERSONATION_TTL": "40m", "AUTH_IMPERSONATION_MAX": "30m"}, "AUTH_IMPERSONATION_MAX"},
		"blank default audience":  {map[string]string{"AUTH_DEFAULT_AUDIENCE": "   "}, "AUTH_DEFAULT_AUDIENCE"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadWith(t, tc.env)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("expected %s error, got %v", tc.wantSub, err)
			}
		})
	}
}
