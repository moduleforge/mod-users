package principals

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRegisterPrincipalRoutes(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterPrincipalRoutes(r, NewPrincipalsHandler(nil))
	var got []string
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got = append(got, method+" "+route)
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{
		"GET /", "GET /{uuid}", "POST /{uuid}/disable", "POST /{uuid}/enable",
		"GET /{uuid}/credentials", "POST /{uuid}/credentials/api-keys",
		"POST /{uuid}/credentials/ssh-keys", "DELETE /{uuid}/credentials/{credential_uuid}",
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n got %v\nwant %v", got, want)
	}
}

func TestRoutesOutsideRequireAuthAreServerErrors(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterPrincipalRoutes(r, NewPrincipalsHandler(nil)) // nil service: never reached
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/"},
		{http.MethodGet, "/00000000-0000-0000-0000-000000000001"},
		{http.MethodPost, "/00000000-0000-0000-0000-000000000001/disable"},
		{http.MethodPost, "/00000000-0000-0000-0000-000000000001/enable"},
		{http.MethodGet, "/00000000-0000-0000-0000-000000000001/credentials"},
		{http.MethodPost, "/00000000-0000-0000-0000-000000000001/credentials/api-keys"},
		{http.MethodPost, "/00000000-0000-0000-0000-000000000001/credentials/ssh-keys"},
		{http.MethodDelete, "/00000000-0000-0000-0000-000000000001/credentials/00000000-0000-0000-0000-000000000002"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s %s = %d, want 500", tc.method, tc.path, rec.Code)
		}
	}
}

func TestQueryInt32(t *testing.T) {
	t.Parallel()
	tests := []struct {
		query   string
		want    int32
		wantErr bool
	}{
		{"", 0, false},
		{"limit=25", 25, false},
		{"limit=0", 0, false},
		{"limit=-1", 0, true},
		{"limit=abc", 0, true},
		{"limit=99999999999", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/?"+tc.query, nil)
			got, err := queryInt32(req, "limit")
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("queryInt32 = %d, %v; want %d, err %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
