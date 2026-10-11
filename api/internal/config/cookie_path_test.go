package config_test

import (
	"strings"
	"testing"

	"github.com/moduleforge/mod-users/api/internal/config"
)

func TestValidateCookieBasePath(t *testing.T) {
	valid := []string{"", "/manage", "/a/b", "/mod-users_1.x~y"}
	invalid := []string{
		"manage", "/", "/manage/", "//manage", "/a//b", "/..", "/a/../b", "/.", "/a/./b",
		"/man age", "/manage;Domain=evil.com", "/manage\r\nSet-Cookie: x=y", "/manage\n",
		"/manage\x00", "/manage,", "/man%2e%2e", "/manage?x", "/manage#x", "/m\\n",
		"/manage\t", "/maé",
	}
	for _, p := range valid {
		if err := config.ValidateCookieBasePath(p); err != nil {
			t.Errorf("%q: unexpected error %v", p, err)
		}
	}
	for _, p := range invalid {
		if err := config.ValidateCookieBasePath(p); err == nil {
			t.Errorf("%q: expected error", p)
		}
	}
}

func TestLoad_CookiePath(t *testing.T) {
	t.Run("unset is empty", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("USERS_COOKIE_PATH", "")
		cfg, err := config.Load()
		if err != nil || cfg.Auth.CookiePath != "" {
			t.Fatalf("got %q, %v", cfg.Auth.CookiePath, err)
		}
	})
	t.Run("valid is loaded", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("USERS_COOKIE_PATH", "/manage")
		cfg, err := config.Load()
		if err != nil || cfg.Auth.CookiePath != "/manage" {
			t.Fatalf("got %q, %v", cfg.Auth.CookiePath, err)
		}
	})
	t.Run("invalid fails startup", func(t *testing.T) {
		clearProviderEnv(t)
		setEnv(t, requiredEnv)
		t.Setenv("USERS_COOKIE_PATH", "/a/../b")
		if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "USERS_COOKIE_PATH") {
			t.Fatalf("want USERS_COOKIE_PATH error, got %v", err)
		}
	})
}
