package config_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func genPublicPEM(t *testing.T) (ed25519.PublicKey, string) {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	return pub, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestLoadJWTVerifyPublicKeys(t *testing.T) {
	pubA, pemA := genPublicPEM(t)
	pubB, pemB := genPublicPEM(t)

	t.Run("unset yields nil", func(t *testing.T) {
		cfg, err := loadWith(t, nil)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if cfg.LocalAuth.JWTVerifyPublicKeys != nil {
			t.Errorf("want nil keys, got %v", cfg.LocalAuth.JWTVerifyPublicKeys)
		}
	})

	t.Run("single PEM is stored under the empty kid", func(t *testing.T) {
		cfg, err := loadWith(t, map[string]string{"JWT_VERIFY_PUBLIC_KEYS": pemA})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if k := cfg.LocalAuth.JWTVerifyPublicKeys[""]; !k.Equal(pubA) || len(cfg.LocalAuth.JWTVerifyPublicKeys) != 1 {
			t.Errorf("unexpected keys: %v", cfg.LocalAuth.JWTVerifyPublicKeys)
		}
	})

	t.Run("single PEM with escaped newlines", func(t *testing.T) {
		esc := strings.ReplaceAll(strings.TrimSpace(pemA), "\n", `\n`)
		cfg, err := loadWith(t, map[string]string{"JWT_VERIFY_PUBLIC_KEYS": esc})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if !cfg.LocalAuth.JWTVerifyPublicKeys[""].Equal(pubA) {
			t.Error("key mismatch")
		}
	})

	t.Run("JSON kid map", func(t *testing.T) {
		raw, _ := json.Marshal(map[string]string{"k1": pemA, "k2": pemB})
		cfg, err := loadWith(t, map[string]string{"JWT_VERIFY_PUBLIC_KEYS": string(raw)})
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		got := cfg.LocalAuth.JWTVerifyPublicKeys
		if len(got) != 2 || !got["k1"].Equal(pubA) || !got["k2"].Equal(pubB) {
			t.Errorf("unexpected keys: %v", got)
		}
	})

	bad := map[string]string{
		"garbage":      "not a key",
		"private key":  func() string { _, p := genEd25519PEM(t); return p }(),
		"bad json":     `{"k1": }`,
		"empty object": `{}`,
		"empty kid":    `{"": "x"}`,
		"bad kid":      `{"a b": "x"}`,
		"bad pem":      `{"k1": "nope"}`,
	}
	for name, v := range bad {
		t.Run("rejects "+name, func(t *testing.T) {
			_, err := loadWith(t, map[string]string{"JWT_VERIFY_PUBLIC_KEYS": v})
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), "JWT_VERIFY_PUBLIC_KEYS") {
				t.Errorf("error does not name the variable: %v", err)
			}
			if strings.Contains(err.Error(), "-----") {
				t.Errorf("error echoes key material: %v", err)
			}
		})
	}
}
