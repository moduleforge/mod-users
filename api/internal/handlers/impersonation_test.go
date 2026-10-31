package handlers

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
)

func TestRegisterImpersonationRoutes(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterImpersonationRoutes(r, NewImpersonationHandler(nil))
	var got []string
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got = append(got, method+" "+route)
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{"POST /", "POST /current/renew", "DELETE /current"}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n got %v\nwant %v", got, want)
	}
}

func TestImpersonationRoutesRefuseCallsOutsideRequireAuthAndNonInteractiveCallers(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterImpersonationRoutes(r, NewImpersonationHandler(nil)) // nil service: never reached
	routes := []struct{ method, path string }{
		{http.MethodPost, "/"}, {http.MethodPost, "/current/renew"}, {http.MethodDelete, "/current"},
	}
	cases := []struct {
		name string
		id   *localauth.Identity
		want int
	}{
		{"no identity (mounted outside RequireAuth)", nil, http.StatusInternalServerError},
		{"client credentials caller", &localauth.Identity{PrincipalKind: localauth.PrincipalKindServiceAccount, CredentialID: 4}, http.StatusForbidden},
	}
	for _, rt := range routes {
		for _, tc := range cases {
			t.Run(rt.method+" "+rt.path+" "+tc.name, func(t *testing.T) {
				t.Parallel()
				req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`))
				if tc.id != nil {
					req = req.WithContext(localauth.WithIdentity(req.Context(), tc.id))
				}
				rec := httptest.NewRecorder()
				r.ServeHTTP(rec, req)
				if rec.Code != tc.want {
					t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body)
				}
			})
		}
	}
}

// Stateless request errors are 400 before the service is touched (the service
// is nil here, so reaching it would panic).
func TestImpersonationStartShapeErrorsAre400BeforeTheService(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterImpersonationRoutes(r, NewImpersonationHandler(nil))
	id := &localauth.Identity{
		PrincipalKind: localauth.PrincipalKindUser, SessionID: 9, SessionUUID: uuid.New(), ProfileUUID: uuid.New(),
	}
	target := uuid.NewString()
	tests := []struct {
		name, body, wantDetail string
	}{
		{"malformed body", `{"profile_uuid":`, "invalid_json_body"},
		{"empty body", ``, "invalid_json_body"},
		{"unknown field", `{"profile_uuid":"` + target + `","reason":"x","extra":1}`, "invalid_json_body"},
		{"no profile", `{"reason":"x"}`, "users.profile_uuid_required"},
		{"bad profile uuid", `{"profile_uuid":"nope","reason":"x"}`, "users.profile_uuid_invalid"},
		{"no reason", `{"profile_uuid":"` + target + `"}`, "users.reason_required"},
		{"blank reason", `{"profile_uuid":"` + target + `","reason":"   "}`, "users.reason_required"},
		{"over-long reason", `{"profile_uuid":"` + target + `","reason":"` + strings.Repeat("a", 501) + `"}`, "users.reason_too_long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req = req.WithContext(localauth.WithIdentity(req.Context(), id))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body)
			}
			if body := rec.Body.String(); !strings.Contains(body, `"invalid_input"`) || !strings.Contains(body, `"`+tc.wantDetail+`"`) {
				t.Errorf("body = %s, want invalid_input with detail %q", body, tc.wantDetail)
			}
		})
	}
}
