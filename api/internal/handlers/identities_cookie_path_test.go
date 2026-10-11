package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestNewOIDCStateCookiePath(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/self/identities/oidc/google/link", nil)
	if got := newOIDCStateCookie("v", 300, r, "/manage").Path; got != "/manage/v1/auth/oidc/" {
		t.Fatalf("configured Path = %q", got)
	}
	r2 := httptest.NewRequest("POST", "/manage/v1/auth/oidc/google/start", nil)
	if got := newOIDCStateCookie("v", 300, r2, "").Path; got != "/manage/v1/auth/oidc/" {
		t.Fatalf("unset Path = %q", got)
	}
	if got := newOIDCStateCookie("v", 300, r, "").Path; got != "/v1/auth/oidc/" {
		t.Fatalf("unset default Path = %q", got)
	}
}
