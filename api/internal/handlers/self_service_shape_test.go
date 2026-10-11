package handlers

// Order tests for the identities and SSH-key credential routes: every
// stateless check (path UUID, body decode, required fields) runs before the
// step-up gate, so a malformed request is a 400 and never consumes a step-up
// token, while a well-formed request without a token still gets the 409
// users.step_up_required action-required envelope.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// selfsvcSyncMapLen counts the entries in m.
func selfsvcSyncMapLen(m *sync.Map) int {
	n := 0
	m.Range(func(_, _ any) bool { n++; return true })
	return n
}

// selfsvcAssertStepUpRequired asserts the 409 users.step_up_required envelope.
func selfsvcAssertStepUpRequired(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	action, ok := body["action"].(map[string]any)
	if !ok {
		t.Fatalf("action = %v, want an object", body["action"])
	}
	if action["code"] != "users.step_up_required" || action["path"] != "/step-up" {
		t.Errorf("action = %v, want code users.step_up_required and path /step-up", action)
	}
}

// selfsvcIdentitiesRouter mounts the identities credential routes behind a
// step-up-required handler whose token store is consumed.
func selfsvcIdentitiesRouter(consumed *sync.Map) chi.Router {
	h := &IdentitiesHandler{stepUpRequired: true, jwtSecret: stepUpTestSecret, consumed: consumed}
	r := chi.NewRouter()
	r.Delete("/identities/{identity_uuid}", h.Unlink)
	r.Post("/credential/password", h.SetPassword)
	return r
}

func TestSelfsvcIdentities_MalformedRequestIs400BeforeStepUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode string
	}{
		{"unlink bad uuid", http.MethodDelete, "/identities/not-a-uuid", "", "invalid_uuid"},
		{"set password malformed json", http.MethodPost, "/credential/password", `{not json`, "invalid_json_body"},
		{"set password unknown field", http.MethodPost, "/credential/password", `{"new_password":"aaaaaaaaaaaaaa","bogus":1}`, "invalid_json_body"},
		{"set password empty body", http.MethodPost, "/credential/password", ``, "invalid_json_body"},
		{"set password missing new_password", http.MethodPost, "/credential/password", `{}`, "users.new_password_required"},
		{"set password too short", http.MethodPost, "/credential/password", `{"new_password":"short"}`, "users.password_too_short"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			consumed := &sync.Map{}
			r := selfsvcIdentitiesRouter(consumed)
			uc := newTestUC(1, 10)

			// A valid token is presented: a malformed request must not consume it.
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("X-Step-Up-Token", issueTestStepUpToken(t, uc.UserAccountID))
			req = withUC(req, uc)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if got := decodeErrorCode(t, rec.Body.Bytes()); got != tc.wantCode {
				t.Errorf("detail code = %q, want %q; body=%s", got, tc.wantCode, rec.Body.String())
			}
			if n := selfsvcSyncMapLen(consumed); n != 0 {
				t.Errorf("step-up token store has %d entries, want 0 (untouched)", n)
			}
		})
	}
}

func TestSelfsvcIdentities_NoTokenMalformedIs400AndWellFormedIs409(t *testing.T) {
	t.Parallel()

	consumed := &sync.Map{}
	r := selfsvcIdentitiesRouter(consumed)
	uc := newTestUC(1, 10)

	// Malformed, no token: 400.
	req := withUC(httptest.NewRequest(http.MethodPost, "/credential/password", strings.NewReader(`{"new_password":"x"}`)), uc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed, no token: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	// Well-formed, no token: unchanged 409 envelope.
	req = withUC(httptest.NewRequest(http.MethodPost, "/credential/password", strings.NewReader(`{"new_password":"a-long-enough-password"}`)), uc)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	selfsvcAssertStepUpRequired(t, rec)

	req = withUC(httptest.NewRequest(http.MethodDelete, "/identities/"+uuid.New().String(), nil), uc)
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	selfsvcAssertStepUpRequired(t, rec)

	if n := selfsvcSyncMapLen(consumed); n != 0 {
		t.Errorf("step-up token store has %d entries, want 0", n)
	}
}

func TestSelfsvcSSHKeys_MalformedRequestIs400BeforeStepUp(t *testing.T) {
	t.Parallel()

	uc := newTestUC(1, 10)
	// Targets the caller's own account so the operator routes are step-up gated too.
	own := uc.UserUUID
	other := uuid.New().String()
	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		wantCode string
	}{
		{"register self malformed json", http.MethodPost, "/self/ssh-keys", `{not json`, "invalid_json_body"},
		{"register self missing public_key", http.MethodPost, "/self/ssh-keys", `{}`, "users.public_key_required"},
		{"revoke self bad key uuid", http.MethodDelete, "/self/ssh-keys/not-a-uuid", ``, "invalid_uuid"},
		{"register operator bad account uuid", http.MethodPost, "/user-accounts/not-a-uuid/ssh-keys", `{"public_key":"k"}`, "invalid_uuid"},
		{"register operator self malformed json", http.MethodPost, "/user-accounts/" + own + "/ssh-keys", `{not json`, "invalid_json_body"},
		{"register operator self missing public_key", http.MethodPost, "/user-accounts/" + own + "/ssh-keys", `{}`, "users.public_key_required"},
		{"register operator other missing public_key", http.MethodPost, "/user-accounts/" + other + "/ssh-keys", `{}`, "users.public_key_required"},
		{"revoke operator bad account uuid", http.MethodDelete, "/user-accounts/not-a-uuid/ssh-keys/" + uuid.New().String(), ``, "invalid_uuid"},
		{"revoke operator self bad key uuid", http.MethodDelete, "/user-accounts/" + own + "/ssh-keys/not-a-uuid", ``, "invalid_uuid"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			svc := &stubSSHKeyService{}
			consumed := &sync.Map{}
			r := newSSHKeysTestRouter(NewSSHKeysHandler(svc, stepUpTestSecret, consumed, true))

			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("X-Step-Up-Token", issueTestStepUpToken(t, uc.UserAccountID))
			req = withUC(req, uc)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if got := decodeErrorCode(t, rec.Body.Bytes()); got != tc.wantCode {
				t.Errorf("detail code = %q, want %q; body=%s", got, tc.wantCode, rec.Body.String())
			}
			if n := selfsvcSyncMapLen(consumed); n != 0 {
				t.Errorf("step-up token store has %d entries, want 0 (untouched)", n)
			}
			if svc.registerCalled || svc.revokeCalled {
				t.Error("service must not be called for a malformed request")
			}
		})
	}
}

func TestSelfsvcSSHKeys_NoTokenMalformedIs400AndWellFormedIs409(t *testing.T) {
	t.Parallel()

	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{}
	consumed := &sync.Map{}
	r := newSSHKeysTestRouter(NewSSHKeysHandler(svc, stepUpTestSecret, consumed, true))

	// Malformed, no token: 400.
	req := withUC(httptest.NewRequest(http.MethodPost, "/self/ssh-keys", strings.NewReader(`{}`)), uc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed, no token: status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}

	// Well-formed, no token: unchanged 409 envelope, on every gated route.
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/self/ssh-keys", `{"public_key":"ssh-ed25519 AAAA"}`},
		{http.MethodDelete, "/self/ssh-keys/" + uuid.New().String(), ``},
		{http.MethodPost, "/user-accounts/" + uc.UserUUID + "/ssh-keys", `{"public_key":"ssh-ed25519 AAAA"}`},
		{http.MethodDelete, "/user-accounts/" + uc.UserUUID + "/ssh-keys/" + uuid.New().String(), ``},
	} {
		req = withUC(httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)), uc)
		rec = httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		selfsvcAssertStepUpRequired(t, rec)
	}
	if svc.registerCalled || svc.revokeCalled {
		t.Error("service must not be called when step-up fails")
	}
}
