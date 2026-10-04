package auth

import (
	"net/http/httptest"
	"testing"
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
