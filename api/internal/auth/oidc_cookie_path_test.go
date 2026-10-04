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
			if got := OIDCStateCookiePath(httptest.NewRequest("GET", tc.url, nil)); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
