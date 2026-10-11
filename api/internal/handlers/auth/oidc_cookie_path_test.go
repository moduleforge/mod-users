package auth

import (
	"net/http/httptest"
	"net/url"
	"testing"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
	"github.com/moduleforge/mod-users/api/internal/config"
)

func TestStateCookiePathSetAndClear(t *testing.T) {
	cases := []struct{ name, url, want string }{
		{"root mount", "/v1/auth/oidc/google/callback", "/v1/auth/oidc/"},
		{"manage prefix", "/manage/v1/auth/oidc/google/callback", "/manage/v1/auth/oidc/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &OIDCHandler{}
			r := httptest.NewRequest("GET", tc.url, nil)
			if got := h.newStateCookie("v", 300, r).Path; got != tc.want {
				t.Fatalf("set Path = %q want %q", got, tc.want)
			}
			w := httptest.NewRecorder()
			h.clearStateCookie(w, r)
			cs := w.Result().Cookies()
			if len(cs) != 1 || cs[0].Path != tc.want || cs[0].MaxAge >= 0 {
				t.Fatalf("clear cookie = %+v want Path %q MaxAge<0", cs, tc.want)
			}
		})
	}
}

func TestStateCookiePathConfiguredSetAndClear(t *testing.T) {
	cfg := &config.Config{}
	cfg.Auth.CookiePath = "/manage"
	h := &OIDCHandler{cfg: cfg}
	// Proxy stripped the /manage prefix: request carries only /v1/auth/oidc/...
	r := httptest.NewRequest("GET", "/v1/auth/oidc/google/callback", nil)
	const want = "/manage/v1/auth/oidc/"
	if got := h.newStateCookie("v", 300, r).Path; got != want {
		t.Fatalf("set Path = %q want %q", got, want)
	}
	w := httptest.NewRecorder()
	h.clearStateCookie(w, r)
	cs := w.Result().Cookies()
	if len(cs) != 1 || cs[0].Path != want || cs[0].MaxAge >= 0 {
		t.Fatalf("clear cookie = %+v want Path %q MaxAge<0", cs, want)
	}
}

func TestRedirectToTestResultBasePath(t *testing.T) {
	cases := []struct {
		name, base, wantPath string
	}{
		{"unset", "", "/oidc-config"},
		{"configured", "/manage", "/manage/oidc-config"},
		{"nested", "/a/b", "/a/b/oidc-config"},
		{"invalid ignored", "/bad;path", "/oidc-config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Auth.CookiePath = tc.base
			h := &OIDCHandler{
				cfg:   cfg,
				oauth: &localauth.OAuth{FrontendReturnURL: "http://gui.test/auth/oidc/return?x=1#frag"},
			}
			r := httptest.NewRequest("GET", "/v1/auth/oidc/google/callback", nil)
			r.Header.Set("X-Forwarded-Prefix", "/evil")
			w := httptest.NewRecorder()
			h.redirectToTestResult(w, r, "google", "a@b.test", "sub", "iss", "")
			u, err := url.Parse(w.Header().Get("Location"))
			if err != nil {
				t.Fatal(err)
			}
			if u.Host != "gui.test" || u.Path != tc.wantPath {
				t.Fatalf("Location = %s want path %q", u, tc.wantPath)
			}
			if u.Fragment != "" {
				t.Fatalf("fragment not cleared: %s", u)
			}
			q := u.Query()
			if q.Get("test_result") != "ok" || q.Get("test_provider") != "google" || q.Get("test_email") != "a@b.test" || q.Get("x") != "" {
				t.Fatalf("bad query: %s", u.RawQuery)
			}
		})
	}
}
