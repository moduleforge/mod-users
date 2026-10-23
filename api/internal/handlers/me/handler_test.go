package me

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	localauth "github.com/moduleforge/mod-users/api/internal/auth"
)

func TestRegisterMeRoutes(t *testing.T) {
	t.Parallel()
	r := chi.NewRouter()
	RegisterMeRoutes(r, NewMeHandler(nil))
	var got []string
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		got = append(got, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	want := []string{
		"GET /", "PUT /", "GET /profiles",
		"GET /identifiers", "POST /identifiers", "POST /identifiers/{uuid}/verify",
		"POST /identifiers/{uuid}/verification", "PUT /identifiers/{uuid}/primary", "DELETE /identifiers/{uuid}",
		"GET /credentials", "POST /credentials/password", "POST /credentials/ssh-keys",
		"PATCH /credentials/{uuid}", "DELETE /credentials/{uuid}",
		"GET /sessions", "DELETE /sessions/{uuid}", "POST /upgrade",
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
	RegisterMeRoutes(r, NewMeHandler(nil)) // nil service: never reached
	cases := []struct {
		name string
		id   *localauth.Identity
		want int
	}{
		{"no identity (mounted outside RequireAuth)", nil, http.StatusInternalServerError},
		{"client credentials caller", &localauth.Identity{PrincipalKind: localauth.PrincipalKindServiceAccount, CredentialID: 4}, http.StatusForbidden},
		{"service account with a session id", &localauth.Identity{PrincipalKind: localauth.PrincipalKindServiceAccount, SessionID: 4}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", nil)
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

func TestOptionalRefDistinguishesAbsentNullAndValue(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	tests := []struct {
		name    string
		body    string
		wantSet bool
		wantID  uuid.UUID
		wantErr bool
	}{
		{"absent", `{}`, false, uuid.Nil, false},
		{"null clears", `{"default_profile_uuid":null}`, true, uuid.Nil, false},
		{"uuid", `{"default_profile_uuid":"` + id.String() + `"}`, true, id, false},
		{"not a uuid", `{"default_profile_uuid":"nope"}`, true, uuid.Nil, true},
		{"nil uuid is not a reference", `{"default_profile_uuid":"` + uuid.Nil.String() + `"}`, true, uuid.Nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var req putRequest
			if err := json.Unmarshal([]byte(tc.body), &req); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got, fes := req.DefaultProfileUUID.parse("default_profile_uuid")
			if (len(fes) > 0) != tc.wantErr {
				t.Fatalf("field errors = %v, want error %v", fes, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got.Set != tc.wantSet || got.Value != tc.wantID {
				t.Fatalf("parse = %+v, want set=%v id=%v", got, tc.wantSet, tc.wantID)
			}
		})
	}
}

func TestPutRequestValidate(t *testing.T) {
	t.Parallel()
	blank, name := "  ", "Ada"
	if fes := (&putRequest{GivenName: &name}).Validate(); len(fes) != 0 {
		t.Fatalf("valid = %v", fes)
	}
	if fes := (&putRequest{GivenName: &blank, FamilyName: &blank}).Validate(); len(fes) != 2 {
		t.Fatalf("blank names = %v", fes)
	}
}

func TestRequestValidation(t *testing.T) {
	t.Parallel()
	good := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl test@host"
	badID := "nope"
	empty := ""
	tests := []struct {
		name string
		want int
		got  func() int
	}{
		{"identifier ok", 0, func() int { return len((&addIdentifierRequest{Value: "a@example.com"}).Validate()) }},
		{"identifier blank", 1, func() int { return len((&addIdentifierRequest{Value: " "}).Validate()) }},
		{"identifier bad email", 1, func() int { return len((&addIdentifierRequest{Value: "not-an-email"}).Validate()) }},
		{"identifier bad kind", 1, func() int { return len((&addIdentifierRequest{Kind: "phone", Value: "+1555"}).Validate()) }},
		{"verify code missing", 1, func() int { return len((&verifyRequest{}).Validate()) }},
		{"password ok", 0, func() int { return len((&addPasswordRequest{Password: "correct horse battery"}).Validate()) }},
		{"password short", 1, func() int { return len((&addPasswordRequest{Password: "short"}).Validate()) }},
		{"password missing", 1, func() int { return len((&addPasswordRequest{}).Validate()) }},
		{"password bad identifier", 1, func() int {
			return len((&addPasswordRequest{Password: "correct horse battery", IdentifierUUID: &badID}).Validate())
		}},
		{"ssh ok", 0, func() int { return len((&addSSHKeyRequest{PublicKey: good}).Validate()) }},
		{"ssh missing", 1, func() int { return len((&addSSHKeyRequest{}).Validate()) }},
		{"ssh garbage", 1, func() int { return len((&addSSHKeyRequest{PublicKey: "garbage"}).Validate()) }},
		{"patch empty", 1, func() int { return len((&patchCredentialRequest{}).Validate()) }},
		{"patch label", 0, func() int { return len((&patchCredentialRequest{Label: &empty}).Validate()) }},
		{"upgrade ok", 0, func() int {
			return len((&upgradeRequest{Email: "a@example.com", Password: "correct horse battery", GivenName: "A", FamilyName: "B"}).Validate())
		}},
		{"upgrade empty", 4, func() int { return len((&upgradeRequest{}).Validate()) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.got(); got != tc.want {
				t.Fatalf("field errors = %d, want %d", got, tc.want)
			}
		})
	}
}
