package parties

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	svc "github.com/moduleforge/mod-users/api/internal/service"
)

func walk(t *testing.T, r chi.Router) string {
	t.Helper()
	var got []string
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got = append(got, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	sort.Strings(got)
	return strings.Join(got, "\n")
}

func TestRegisterRoutes(t *testing.T) {
	t.Parallel()
	parties := chi.NewRouter()
	RegisterPartyRoutes(parties, NewPartiesHandler(nil))
	want := []string{
		"GET /{party_uuid}/profiles", "POST /{party_uuid}/profiles", "POST /{party_uuid}/managed-users",
	}
	sort.Strings(want)
	if got := walk(t, parties); got != strings.Join(want, "\n") {
		t.Fatalf("party routes:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}

	profiles := chi.NewRouter()
	RegisterProfileRoutes(profiles, NewPartiesHandler(nil))
	if got := walk(t, profiles); got != "PATCH /{uuid}" {
		t.Fatalf("profile routes = %q", got)
	}
}

func TestRoutesRefuseCallsOutsideRequireAuth(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	h := NewPartiesHandler(nil) // nil service: never reached
	RegisterPartyRoutes(r, h)
	RegisterProfileRoutes(r, h)
	id := uuid.New().String()
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/" + id + "/profiles"},
		{http.MethodPost, "/" + id + "/profiles"},
		{http.MethodPost, "/" + id + "/managed-users"},
		{http.MethodPatch, "/" + id},
	} {
		req := httptest.NewRequest(rt.method, rt.path, strings.NewReader(`{}`))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s %s: status = %d, want 500 (mounted outside RequireAuth)", rt.method, rt.path, rec.Code)
		}
	}
}

func codes(fes []apiresp.FieldError) string {
	var out []string
	for _, f := range fes {
		out = append(out, f.Code)
	}
	return strings.Join(out, ",")
}

func TestInviteRequestValidate(t *testing.T) {
	t.Parallel()
	id := uuid.New().String()
	tests := []struct {
		name string
		req  inviteRequest
		want string
	}{
		{"email", inviteRequest{HolderEmail: "ada@example.com"}, ""},
		{"user uuid", inviteRequest{HolderUserUUID: id}, ""},
		{"confirmation flag alone is not a holder", inviteRequest{ConfirmPolicyMismatch: true}, "users.holder_required"},
		{"neither", inviteRequest{}, "users.holder_required"},
		{"both", inviteRequest{HolderEmail: "ada@example.com", HolderUserUUID: id}, "users.holder_ambiguous"},
		{"bad email", inviteRequest{HolderEmail: "nope"}, "users.email_invalid"},
		{"bad uuid", inviteRequest{HolderUserUUID: "nope"}, "invalid_uuid"},
		{"nil uuid", inviteRequest{HolderUserUUID: uuid.Nil.String()}, "invalid_uuid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := codes(tc.req.Validate())
			if !strings.HasPrefix(got, tc.want) || (tc.want == "" && got != "") {
				t.Fatalf("codes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestManagedUserRequestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  managedUserRequest
		want string
	}{
		{"ok", managedUserRequest{Email: "m@example.com", Password: "correct-horse-battery"}, ""},
		{"empty", managedUserRequest{}, "users.email_required,users.password_required"},
		{"bad email", managedUserRequest{Email: "nope", Password: "correct-horse-battery"}, "users.email_invalid"},
		{"short password", managedUserRequest{Email: "m@example.com", Password: "short"}, "users.password_too_short"},
		{"long password", managedUserRequest{Email: "m@example.com", Password: strings.Repeat("x", 2000)}, "users.password_too_long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := codes(tc.req.Validate()); got != tc.want {
				t.Fatalf("codes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPatchRequestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  patchRequest
		want string
	}{
		{"suspend", patchRequest{Status: "suspended"}, ""},
		{"resume", patchRequest{Status: "active"}, ""},
		{"end", patchRequest{Status: "ended", EndReason: "contract over"}, ""},
		{"end needs a reason", patchRequest{Status: "ended"}, "users.end_reason_required"},
		{"reason only with ended", patchRequest{Status: "suspended", EndReason: "x"}, "users.end_reason_unexpected"},
		{"status required", patchRequest{}, "users.status_required"},
		{"invited is not a target", patchRequest{Status: "invited"}, "users.profile_status_invalid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := codes(tc.req.Validate()); got != tc.want {
				t.Fatalf("codes = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQueryInt32(t *testing.T) {
	t.Parallel()
	get := func(q string) (int32, error) {
		return queryInt32(httptest.NewRequest(http.MethodGet, "/?"+q, nil), "limit")
	}
	if n, err := get(""); n != 0 || err != nil {
		t.Errorf("absent = %d, %v", n, err)
	}
	if n, err := get("limit=25"); n != 25 || err != nil {
		t.Errorf("25 = %d, %v", n, err)
	}
	for _, bad := range []string{"limit=-1", "limit=x", "limit=99999999999"} {
		if _, err := get(bad); !errors.Is(err, apiresp.ErrInvalidInput) {
			t.Errorf("%s = %v, want invalid input", bad, err)
		}
	}
}

func TestWriteError_RendersTheSharedEnvelopes(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPatch, "/", nil)

	for name, tc := range map[string]struct {
		err    error
		status int
		code   string
	}{
		"transition": {svc.ErrProfileTransition, http.StatusConflict, "users.profile_transition_invalid"},
		"exists":     {svc.ErrProfileExists, http.StatusConflict, "users.profile_exists"},
		"guest":      {svc.ErrGuestMustUpgrade, http.StatusConflict, "users.guest_upgrade_required"},
		"admissible": {svc.ErrHolderNotAdmissible, http.StatusConflict, "users.holder_not_admissible"},
	} {
		rec := httptest.NewRecorder()
		writeError(rec, req, fmt.Errorf("wrapped: %w", tc.err))
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"`+tc.code+`"`) {
			t.Errorf("%s: response = %d %s, want %d with %s", name, rec.Code, rec.Body.String(), tc.status, tc.code)
		}
	}

	t.Run("step-up required is the shared action-required envelope", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		writeError(rec, req, svc.ErrStepUpRequired)
		var body struct {
			Action struct{ Code string } `json:"action"`
			Error  json.RawMessage       `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if rec.Code != http.StatusConflict || body.Action.Code != "users.step_up_required" || body.Error != nil {
			t.Errorf("response = %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("an unexpected error is a 500 that never echoes it", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		writeError(rec, req, errors.New("pq: password authentication failed for user postgres"))
		if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "postgres") {
			t.Errorf("response = %d %s", rec.Code, rec.Body.String())
		}
	})
}
