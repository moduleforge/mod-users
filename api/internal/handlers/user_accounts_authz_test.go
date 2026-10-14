package handlers

// Tests for the user-account, assume and PUT /self routes through the real
// handlers and a real UserAccountService built with fakes (a deny-all
// authorizer and a querier that only knows one account). They pin the
// error-precedence rule: every stateless check (path UUID, body decode,
// Validator) answers 400 before authorization, and a well-formed request from
// an unauthorized caller still gets 403.
//
// The apiresp.WriteError mapping tests at the bottom are independent of the
// handlers.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	"github.com/moduleforge/core-api/observer"
	"github.com/moduleforge/core-api/opctx"
	"github.com/moduleforge/core-api/types"
	"github.com/moduleforge/mod-users/api/internal/auth"
	localAuthz "github.com/moduleforge/mod-users/api/internal/authz"
	svc "github.com/moduleforge/mod-users/api/internal/service"
	db "github.com/moduleforge/mod-users/model/db"
)

// shapeEnv is a router over the real handlers. The caller is authenticated
// but holds no grants: every Authorize call is denied.
type shapeEnv struct {
	router      http.Handler
	grantCalls  int
	revokeCalls int
}

func newShapeEnv() *shapeEnv {
	env := &shapeEnv{}
	existing := db.UserAccount{ID: 7, Uuid: uuid.New(), AccountHolder: 700}
	svcReal := svc.NewUserAccountService(
		nil, &missQuerier{existing: existing}, nil, &denyAllAuthorizer{},
		&observer.ObserverGroup{}, nil, types.NewFromMap(map[string]int64{"natural_person": 1}), nil,
	)
	uh := NewUserAccountsHandler(svcReal,
		func(context.Context, uuid.UUID) error { env.grantCalls++; return nil },
		func(context.Context, uuid.UUID) error { env.revokeCalls++; return nil },
	)
	ah := NewAssumeHandler(svcReal, "test-secret-test-secret-test-secret", "test")
	sh := &SelfHandler{}

	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := auth.WithUserContext(req.Context(), &auth.UserContext{EntityID: 42})
			next.ServeHTTP(w, req.WithContext(opctx.WithActor(ctx, 42)))
		})
	})
	r.Post("/v1/user-accounts", uh.Create)
	r.Get("/v1/user-accounts/{uuid}", uh.Get)
	r.Put("/v1/user-accounts/{uuid}", uh.Update)
	r.Delete("/v1/user-accounts/{uuid}", uh.Delete)
	r.Post("/v1/user-accounts/{uuid}/grant-admin", uh.GrantAdmin)
	r.Post("/v1/user-accounts/{uuid}/revoke-admin", uh.RevokeAdmin)
	r.Post("/v1/user-accounts/{uuid}/assume", ah.Assume)
	r.Put("/v1/self", sh.Put)
	env.router = r
	return env
}

func (e *shapeEnv) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rr := httptest.NewRecorder()
	e.router.ServeHTTP(rr, req)
	return rr
}

// shapeErrorBody decodes the error envelope.
func shapeErrorBody(t *testing.T, rr *httptest.ResponseRecorder) (code string, details []apiresp.FieldError) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string               `json:"code"`
			Details []apiresp.FieldError `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not valid JSON: %v, body=%s", err, rr.Body.String())
	}
	return body.Error.Code, body.Error.Details
}

// shapeWant400 asserts a 400 invalid_input whose details contain wantCode.
func shapeWant400(t *testing.T, rr *httptest.ResponseRecorder, wantCode string) {
	t.Helper()
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: got %d, want 400, body=%s", rr.Code, rr.Body.String())
	}
	code, details := shapeErrorBody(t, rr)
	if code != "invalid_input" {
		t.Errorf("error.code: got %q, want invalid_input", code)
	}
	if wantCode == "" {
		return
	}
	for _, d := range details {
		if d.Code == wantCode {
			return
		}
	}
	t.Errorf("details %+v lack code %q", details, wantCode)
}

func TestShape_Create_PrecedenceForUnauthorizedCaller(t *testing.T) {
	env := newShapeEnv()
	cases := []struct {
		name, body string
		want400    string // "" with status403 true means expect 403
		status403  bool
		bodyIs400  bool
	}{
		{name: "malformed body", body: "not-json", bodyIs400: true},
		{name: "unknown field", body: `{"email":"a@b.c","given_name":"A","family_name":"B","x":1}`, bodyIs400: true},
		{name: "missing email", body: `{"given_name":"A","family_name":"B"}`, want400: "users.email_required"},
		{name: "blank email", body: `{"email":"  ","given_name":"A","family_name":"B"}`, want400: "users.email_required"},
		{name: "blank given_name", body: `{"email":"a@b.c","given_name":" ","family_name":"B"}`, want400: "users.given_name_required"},
		{name: "missing family_name", body: `{"email":"a@b.c","given_name":"A"}`, want400: "users.family_name_required"},
		{name: "short password", body: `{"email":"a@b.c","given_name":"A","family_name":"B","password":"short"}`, want400: "users.password_too_short"},
		{name: "valid body", body: `{"email":"a@b.c","given_name":"A","family_name":"B","password":"long-enough-pw1"}`, status403: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.do(http.MethodPost, "/v1/user-accounts", tc.body)
			if tc.status403 {
				if rr.Code != http.StatusForbidden {
					t.Fatalf("status: got %d, want 403, body=%s", rr.Code, rr.Body.String())
				}
				return
			}
			shapeWant400(t, rr, tc.want400)
		})
	}
}

func TestShape_Create_MultipleFieldErrorsReported(t *testing.T) {
	rr := newShapeEnv().do(http.MethodPost, "/v1/user-accounts", `{}`)
	shapeWant400(t, rr, "users.email_required")
	_, details := shapeErrorBody(t, rr)
	if len(details) != 3 {
		t.Errorf("details: got %+v, want email, given_name and family_name", details)
	}
}

func TestShape_Update_BlankFieldsAre400BeforeAuthz(t *testing.T) {
	env := newShapeEnv()
	// The target does not exist and the caller holds no grants, so a
	// well-formed request is a masked 403; the blank-field cases must be 400.
	path := "/v1/user-accounts/" + uuid.New().String()
	cases := []struct{ name, body, want string }{
		{"blank email", `{"email":""}`, "users.email_required"},
		{"whitespace email", `{"email":"   "}`, "users.email_required"},
		{"blank given_name", `{"given_name":" "}`, "users.given_name_required"},
		{"blank family_name", `{"family_name":""}`, "users.family_name_required"},
		{"malformed body", `{`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shapeWant400(t, env.do(http.MethodPut, path, tc.body), tc.want)
		})
	}
	if rr := env.do(http.MethodPut, path, `{"given_name":"Ok"}`); rr.Code != http.StatusForbidden {
		t.Errorf("valid body, unauthorized caller: got %d, want 403", rr.Code)
	}
}

func TestShape_PathUUID_Invalid400BeforeAuthz(t *testing.T) {
	env := newShapeEnv()
	routes := []struct{ name, method, suffix, body string }{
		{"Get", http.MethodGet, "", ""},
		{"Update", http.MethodPut, "", `{"given_name":"Ok"}`},
		{"Delete", http.MethodDelete, "", ""},
		{"GrantAdmin", http.MethodPost, "/grant-admin", ""},
		{"RevokeAdmin", http.MethodPost, "/revoke-admin", ""},
		{"Assume", http.MethodPost, "/assume", ""},
	}
	for _, tc := range routes {
		t.Run(tc.name, func(t *testing.T) {
			rr := env.do(tc.method, "/v1/user-accounts/not-a-uuid"+tc.suffix, tc.body)
			shapeWant400(t, rr, "invalid_uuid")
			_, details := shapeErrorBody(t, rr)
			if len(details) != 1 || details[0].Field != "uuid" {
				t.Errorf("details: got %+v, want one entry for field uuid", details)
			}
		})
	}
	if env.grantCalls != 0 || env.revokeCalls != 0 {
		t.Errorf("grant/revoke closures ran on a malformed uuid (grant=%d revoke=%d)", env.grantCalls, env.revokeCalls)
	}
}

func TestShape_Self_BlankNamesAre400(t *testing.T) {
	env := newShapeEnv()
	cases := []struct{ name, body, want string }{
		{"blank given_name", `{"given_name":""}`, "natural_persons.given_name_required"},
		{"whitespace given_name", `{"given_name":"  "}`, "natural_persons.given_name_required"},
		{"blank family_name", `{"family_name":" "}`, "natural_persons.family_name_required"},
		{"malformed body", `nope`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			shapeWant400(t, env.do(http.MethodPut, "/v1/self", tc.body), tc.want)
		})
	}
}

func TestShape_Self_BothBlankNamesReported(t *testing.T) {
	rr := newShapeEnv().do(http.MethodPut, "/v1/self", `{"given_name":"","family_name":""}`)
	shapeWant400(t, rr, "natural_persons.given_name_required")
	_, details := shapeErrorBody(t, rr)
	if len(details) != 2 {
		t.Errorf("details: got %+v, want given_name and family_name", details)
	}
}

// TestShape_ServiceUpdate_AuthzBeforeBlankCheck covers in-process callers: the
// service rejects a blank field only after authorization succeeds.
func TestShape_ServiceUpdate_AuthzBeforeBlankCheck(t *testing.T) {
	existing := db.UserAccount{ID: 7, Uuid: uuid.New(), AccountHolder: 700}
	svcReal := svc.NewUserAccountService(nil, &missQuerier{existing: existing}, nil, &denyAllAuthorizer{},
		&observer.ObserverGroup{}, nil, nil, nil)
	ctx := opctx.WithActor(context.Background(), 42)
	blank := " "
	_, err := svcReal.Update(ctx, existing.Uuid, svc.UpdateUserAccountInput{Email: &blank})
	if !errors.Is(err, apiresp.ErrForbidden) {
		t.Errorf("unauthorized caller with blank email: got %v, want ErrForbidden", err)
	}
}

// ---------------------------------------------------------------------------
// Test: apiresp.WriteError service-error mapping table
// ---------------------------------------------------------------------------

func TestWriteError_ServiceErrorMapping(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantStatus  int
		wantCode    string
		wantDetails []apiresp.FieldError // nil when no details[] is expected
	}{
		// unauthenticated (was "unauthorized" before apiresp adoption; see
		// docs/mf-standards/architecture/api-response-design.md "Reserved core
		// codes" — unauthenticated is the canonical 401 code).
		{"unauthenticated", localAuthz.ErrUnauthenticated, http.StatusUnauthorized, "unauthenticated", nil},
		{"forbidden", localAuthz.ErrForbidden, http.StatusForbidden, "forbidden", nil},
		// email_taken (was a top-level "email_taken" code before apiresp
		// adoption): now top-level "conflict" (409) with a namespaced
		// details[].code, per the design doc's worked example.
		{
			name:       "email_taken",
			err:        svc.ErrEmailTaken,
			wantStatus: http.StatusConflict,
			wantCode:   "conflict",
			wantDetails: []apiresp.FieldError{
				{Field: "email", Code: "users.email_taken", Message: "email is already registered"},
			},
		},
		{"invalid_input", svc.ErrInvalidInput, http.StatusBadRequest, "invalid_input", nil},
		{"internal", errors.New("db down"), http.StatusInternalServerError, "internal_error", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/user-accounts", nil)
			rr := httptest.NewRecorder()
			apiresp.WriteError(rr, req, tt.err)
			if rr.Code != tt.wantStatus {
				t.Errorf("apiresp.WriteError(%v): status got %d, want %d", tt.err, rr.Code, tt.wantStatus)
			}

			var body struct {
				Error struct {
					Code    string               `json:"code"`
					Message string               `json:"message"`
					Details []apiresp.FieldError `json:"details"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("apiresp.WriteError(%v): body not valid JSON: %v, body=%s", tt.err, err, rr.Body.String())
			}
			if body.Error.Code != tt.wantCode {
				t.Errorf("apiresp.WriteError(%v): error.code got %q, want %q", tt.err, body.Error.Code, tt.wantCode)
			}
			if tt.wantDetails == nil {
				if len(body.Error.Details) != 0 {
					t.Errorf("apiresp.WriteError(%v): expected no details, got %+v", tt.err, body.Error.Details)
				}
				return
			}
			if len(body.Error.Details) != len(tt.wantDetails) {
				t.Fatalf("apiresp.WriteError(%v): details got %+v, want %+v", tt.err, body.Error.Details, tt.wantDetails)
			}
			for i, want := range tt.wantDetails {
				if body.Error.Details[i] != want {
					t.Errorf("apiresp.WriteError(%v): details[%d] got %+v, want %+v", tt.err, i, body.Error.Details[i], want)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test: apiresp.WriteError — unclassified error → 500 internal_error
// ---------------------------------------------------------------------------

// TestWriteError_AuthzUnclassifiedMapsTo500 asserts that apiresp.WriteError,
// like the service-error mapping, maps an error that is not a recognized apiresp sentinel
// (apiresp.ErrUnauthenticated / apiresp.ErrForbidden) to a 500 with the
// "internal_error" code, per apiresp.WriteError's default classification.
func TestWriteError_AuthzUnclassifiedMapsTo500(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/v1/user-accounts", nil)
	rr := httptest.NewRecorder()

	err := errors.New("db down")
	apiresp.WriteError(rr, req, err)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("apiresp.WriteError(%v): status got %d, want %d", err, rr.Code, http.StatusInternalServerError)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if decErr := json.Unmarshal(rr.Body.Bytes(), &body); decErr != nil {
		t.Fatalf("apiresp.WriteError(%v): body not valid JSON: %v, body=%s", err, decErr, rr.Body.String())
	}
	if body.Error.Code != "internal_error" {
		t.Errorf("apiresp.WriteError(%v): error.code got %q, want %q", err, body.Error.Code, "internal_error")
	}
}
