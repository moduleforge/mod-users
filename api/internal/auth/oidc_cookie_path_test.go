package auth

import (
	"net/http/httptest"
	"testing"
)

func TestOIDCStateCookiePath(t *testing.T) {
	cases := []struct{ name, url, want string }{
		{"root start", "/v1/auth/oidc/google/start", "/v1/auth/oidc/"},
		{"root callback", "/v1/auth/oidc/google/callback", "/v1/auth/oidc/"},
		{"manage prefix", "/manage/v1/auth/oidc/google/callback", "/manage/v1/auth/oidc/"},
		{"no marker", "/something/else", "/v1/auth/oidc/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OIDCStateCookiePath(httptest.NewRequest("GET", tc.url, nil), ""); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestOIDCStateCookiePath_Configured(t *testing.T) {
	cases := []struct{ name, url, base, want string }{
		{"stripped proxy", "/v1/auth/oidc/google/callback", "/manage", "/manage/v1/auth/oidc/"},
		{"base wins over request prefix", "/other/v1/auth/oidc/google/start", "/manage", "/manage/v1/auth/oidc/"},
		{"no marker", "/something/else", "/manage", "/manage/v1/auth/oidc/"},
		{"unset derives from request", "/manage/v1/auth/oidc/google/callback", "", "/manage/v1/auth/oidc/"},
		{"invalid base ignored", "/v1/auth/oidc/google/start", "/a;b", "/v1/auth/oidc/"},
		{"traversal base ignored", "/v1/auth/oidc/google/start", "/../x", "/v1/auth/oidc/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OIDCStateCookiePath(httptest.NewRequest("GET", tc.url, nil), tc.base); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	if got := OIDCStateCookiePath(nil, "/manage"); got != "/manage/v1/auth/oidc/" {
		t.Fatalf("nil request: got %q", got)
	}
}
