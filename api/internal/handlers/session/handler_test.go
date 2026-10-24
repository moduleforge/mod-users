package session

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

func TestRegisterSessionRoutes(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterSessionRoutes(r, NewSessionHandler(nil))
	var got []string
	if err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got = append(got, method+" "+route)
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{
		"GET /", "POST /switch", "POST /fork", "POST /step-up/password",
		"POST /step-up/email-code/request", "POST /step-up/email-code/verify", "POST /logout",
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("routes:\n got %v\nwant %v", got, want)
	}
}

func TestRoutesRefuseCallsOutsideRequireAuthAndNonInteractiveCallers(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterSessionRoutes(r, NewSessionHandler(nil)) // nil service: never reached
	routes := []struct{ method, path string }{
		{http.MethodGet, "/"}, {http.MethodPost, "/switch"}, {http.MethodPost, "/fork"},
		{http.MethodPost, "/step-up/password"}, {http.MethodPost, "/step-up/email-code/request"},
		{http.MethodPost, "/step-up/email-code/verify"}, {http.MethodPost, "/logout"},
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
func TestRequestShapeErrorsAre400BeforeTheService(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterSessionRoutes(r, NewSessionHandler(nil))
	id := &localauth.Identity{
		PrincipalKind: localauth.PrincipalKindUser, SessionID: 9, SessionUUID: uuid.New(), ProfileUUID: uuid.New(),
	}
	tests := []struct {
		name, path, body, wantDetail string
	}{
		{"switch without a profile", "/switch", `{}`, "users.profile_uuid_required"},
		{"switch with a bad profile uuid", "/switch", `{"profile_uuid":"nope"}`, "users.profile_uuid_invalid"},
		{"switch with a malformed body", "/switch", `{"profile_uuid":`, "invalid_json_body"},
		{"switch with an unknown field", "/switch", `{"profile_uuid":"` + uuid.NewString() + `","x":1}`, "invalid_json_body"},
		{"step-up without a password", "/step-up/password", `{}`, "users.password_required"},
		{"step-up code verify without a code", "/step-up/email-code/verify", `{"email":"a@example.test"}`, "users.code_required"},
		{"step-up code request with a malformed body", "/step-up/email-code/request", `{"email":`, "invalid_json_body"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
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
