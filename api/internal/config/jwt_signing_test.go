package config_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/config"
)

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

func loadWith(t *testing.T, extra map[string]string) (*config.Config, error) {
	t.Helper()
	clearProviderEnv(t)
	setEnv(t, requiredEnv)
	for _, k := range []string{"JWT_ALG", "JWT_SIGNING_KEY", "JWT_SIGNING_KID", "JWT_VERIFY_PUBLIC_KEYS"} {
		t.Setenv(k, "")
	}
	setEnv(t, extra)
	return config.Load()
}

func TestLoadJWTSigning(t *testing.T) {
	priv, pemStr := genEd25519PEM(t)

	t.Run("default is HS256 with no key or kid", func(t *testing.T) {
		cfg, err := loadWith(t, nil)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.LocalAuth.JWTAlg != "HS256" || cfg.LocalAuth.JWTSigningKey != nil || cfg.LocalAuth.JWTSigningKID != "" {
			t.Errorf("unexpected defaults: alg=%q key=%v kid=%q", cfg.LocalAuth.JWTAlg, cfg.LocalAuth.JWTSigningKey, cfg.LocalAuth.JWTSigningKID)
		}
	})

	t.Run("EdDSA with PEM key and kid", func(t *testing.T) {
		cfg, err := loadWith(t, map[string]string{"JWT_ALG": "EdDSA", "JWT_SIGNING_KEY": pemStr, "JWT_SIGNING_KID": "k1"})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.LocalAuth.JWTAlg != "EdDSA" || cfg.LocalAuth.JWTSigningKID != "k1" || !priv.Equal(cfg.LocalAuth.JWTSigningKey) {
			t.Errorf("EdDSA config not applied: alg=%q kid=%q", cfg.LocalAuth.JWTAlg, cfg.LocalAuth.JWTSigningKID)
		}
	})

	t.Run("EdDSA accepts escaped newlines in the PEM", func(t *testing.T) {
		escaped := strings.ReplaceAll(strings.TrimSpace(pemStr), "\n", `\n`)
		cfg, err := loadWith(t, map[string]string{"JWT_ALG": "EdDSA", "JWT_SIGNING_KEY": escaped})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !priv.Equal(cfg.LocalAuth.JWTSigningKey) {
			t.Error("parsed key does not match")
		}
	})

	t.Run("HS256 still requires JWT_SECRET", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("JWT_SECRET", "")
		t.Setenv("DB_URL", "")
		t.Setenv("MFAPP_DATABASE_URL", "")
		_, err := config.Load()
		if err == nil || !strings.Contains(err.Error(), "JWT_SECRET") {
			t.Fatalf("expected JWT_SECRET error, got %v", err)
		}
	})

	failures := []struct {
		name    string
		env     map[string]string
		wantSub string
	}{
		{"EdDSA without key", map[string]string{"JWT_ALG": "EdDSA"}, "JWT_SIGNING_KEY: required"},
		{"EdDSA with garbage key", map[string]string{"JWT_ALG": "EdDSA", "JWT_SIGNING_KEY": "not-a-pem-SECRETMARKER"}, "JWT_SIGNING_KEY"},
		{"unknown alg", map[string]string{"JWT_ALG": "RS256"}, "JWT_ALG"},
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
		})
	}

	t.Run("PEM block with malformed DER rejected", func(t *testing.T) {
		bad := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("garbage")}))
		_, err := loadWith(t, map[string]string{"JWT_ALG": "EdDSA", "JWT_SIGNING_KEY": bad})
		if err == nil || !strings.Contains(err.Error(), "JWT_SIGNING_KEY") {
			t.Fatalf("expected JWT_SIGNING_KEY error, got %v", err)
		}
	})
}
