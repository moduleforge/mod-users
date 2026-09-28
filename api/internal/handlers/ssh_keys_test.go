package handlers

// Tests for SSHKeysHandler and its Register* route functions
// (plan/phase-01-ssh-public-keys/004-add-ssh-key-http-routes.md).
//
// All tests use httptest plus stubSSHKeyService (an in-memory stand-in for
// *usersservice.SSHKeyService satisfying the unexported sshKeyService
// interface) -- no real database or transaction is involved.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/moduleforge/core-api/apiresp"
	usersservice "github.com/moduleforge/mod-users/api/internal/service"
)

// ---------------------------------------------------------------------------
// Stub service
// ---------------------------------------------------------------------------

// stubSSHKeyService is an in-memory stand-in for *usersservice.SSHKeyService,
// satisfying the sshKeyService interface SSHKeysHandler depends on.
type stubSSHKeyService struct {
	registerFn func(ctx context.Context, accountUUID uuid.UUID, publicKeyLine string, label *string, stepUpUsed bool) (usersservice.SSHKey, error)
	listFn     func(ctx context.Context, accountUUID uuid.UUID, limit, offset int32) ([]usersservice.SSHKey, int64, error)
	revokeFn   func(ctx context.Context, accountUUID, keyUUID uuid.UUID, stepUpUsed bool) error

	registerCalled bool
	listCalled     bool
	revokeCalled   bool
}

func (s *stubSSHKeyService) Register(ctx context.Context, accountUUID uuid.UUID, publicKeyLine string, label *string, stepUpUsed bool) (usersservice.SSHKey, error) {
	s.registerCalled = true
	if s.registerFn != nil {
		return s.registerFn(ctx, accountUUID, publicKeyLine, label, stepUpUsed)
	}
	return usersservice.SSHKey{UUID: uuid.New()}, nil
}

func (s *stubSSHKeyService) List(ctx context.Context, accountUUID uuid.UUID, limit, offset int32) ([]usersservice.SSHKey, int64, error) {
	s.listCalled = true
	if s.listFn != nil {
		return s.listFn(ctx, accountUUID, limit, offset)
	}
	return nil, 0, nil
}

func (s *stubSSHKeyService) Revoke(ctx context.Context, accountUUID, keyUUID uuid.UUID, stepUpUsed bool) error {
	s.revokeCalled = true
	if s.revokeFn != nil {
		return s.revokeFn(ctx, accountUUID, keyUUID, stepUpUsed)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newSSHKeysTestRouter mounts all three SSH-key route groups behind h, using
// the same Register* functions the module manifest references.
func newSSHKeysTestRouter(h *SSHKeysHandler) chi.Router {
	r := chi.NewRouter()
	RegisterSelfSSHKeysReadRoute(r, h)
	RegisterSelfSSHKeysWriteRoutes(r, h)
	RegisterUserAccountSSHKeyRoutes(r, h)
	return r
}

// jsonRequest builds a request with a JSON-encoded body.
func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// decodeErrorCode returns the error.details[0].code (or error.code if no
// details) from an apiresp.Envelope-shaped response body.
func decodeErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Details []struct {
				Code string `json:"code"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("unmarshal error envelope: %v; body=%s", err, body)
	}
	if len(env.Error.Details) > 0 {
		return env.Error.Details[0].Code
	}
	return env.Error.Code
}

// ---------------------------------------------------------------------------
// Route mounting
// ---------------------------------------------------------------------------

func TestSSHKeysRoutes_MountedAtRightPathAndMethod(t *testing.T) {
	uc := newTestUC(1, 10)
	accountUUID := uuid.New()
	keyUUID := uuid.New()

	tests := []struct {
		name       string
		method     string
		path       string
		withUC     bool
		wantStatus int
		wantCalled func(*stubSSHKeyService) bool
	}{
		{
			name:       "GET /self/ssh-keys -> ListSelf",
			method:     http.MethodGet,
			path:       "/self/ssh-keys",
			withUC:     true,
			wantStatus: http.StatusOK,
			wantCalled: func(s *stubSSHKeyService) bool { return s.listCalled },
		},
		{
			name:       "POST /self/ssh-keys -> RegisterSelf",
			method:     http.MethodPost,
			path:       "/self/ssh-keys",
			withUC:     true,
			wantStatus: http.StatusCreated,
			wantCalled: func(s *stubSSHKeyService) bool { return s.registerCalled },
		},
		{
			name:       "DELETE /self/ssh-keys/{key_uuid} -> RevokeSelf",
			method:     http.MethodDelete,
			path:       "/self/ssh-keys/" + keyUUID.String(),
			withUC:     true,
			wantStatus: http.StatusNoContent,
			wantCalled: func(s *stubSSHKeyService) bool { return s.revokeCalled },
		},
		{
			name:       "GET /user-accounts/{uuid}/ssh-keys -> ListForAccount",
			method:     http.MethodGet,
			path:       "/user-accounts/" + accountUUID.String() + "/ssh-keys",
			wantStatus: http.StatusOK,
			wantCalled: func(s *stubSSHKeyService) bool { return s.listCalled },
		},
		{
			name:       "POST /user-accounts/{uuid}/ssh-keys -> RegisterForAccount",
			method:     http.MethodPost,
			path:       "/user-accounts/" + accountUUID.String() + "/ssh-keys",
			wantStatus: http.StatusCreated,
			wantCalled: func(s *stubSSHKeyService) bool { return s.registerCalled },
		},
		{
			name:       "DELETE /user-accounts/{uuid}/ssh-keys/{key_uuid} -> RevokeForAccount",
			method:     http.MethodDelete,
			path:       "/user-accounts/" + accountUUID.String() + "/ssh-keys/" + keyUUID.String(),
			wantStatus: http.StatusNoContent,
			wantCalled: func(s *stubSSHKeyService) bool { return s.revokeCalled },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubSSHKeyService{}
			h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
			r := newSSHKeysTestRouter(h)

			var req *http.Request
			if tt.method == http.MethodPost {
				req = jsonRequest(t, tt.method, tt.path, registerSSHKeyRequest{PublicKey: "ssh-ed25519 AAAA"})
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			if tt.withUC {
				req = withUC(req, uc)
			}

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !tt.wantCalled(svc) {
				t.Error("expected service method to be called, was not")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Step-up enforcement (design note D8)
// ---------------------------------------------------------------------------

func TestSSHKeysHandler_StepUp_SelfRoutes_FlagOn_NoToken_Returns409(t *testing.T) {
	uc := newTestUC(1, 10)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"RegisterSelf", http.MethodPost, "/self/ssh-keys"},
		{"RevokeSelf", http.MethodDelete, "/self/ssh-keys/" + uuid.New().String()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubSSHKeyService{}
			h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, true)
			r := newSSHKeysTestRouter(h)

			var req *http.Request
			if tt.method == http.MethodPost {
				req = jsonRequest(t, tt.method, tt.path, registerSSHKeyRequest{PublicKey: "ssh-ed25519 AAAA"})
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			req = withUC(req, uc)

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

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
			if action["code"] != "users.step_up_required" {
				t.Errorf("action.code = %v, want users.step_up_required", action["code"])
			}
			if svc.registerCalled || svc.revokeCalled {
				t.Error("service must not be called when step-up fails")
			}
		})
	}
}

func TestSSHKeysHandler_StepUp_SelfRoutes_FlagOn_ValidToken_Proceeds(t *testing.T) {
	uc := newTestUC(1, 10)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCalled func(*stubSSHKeyService) bool
	}{
		{"RegisterSelf", http.MethodPost, "/self/ssh-keys", http.StatusCreated, func(s *stubSSHKeyService) bool { return s.registerCalled }},
		{"RevokeSelf", http.MethodDelete, "/self/ssh-keys/" + uuid.New().String(), http.StatusNoContent, func(s *stubSSHKeyService) bool { return s.revokeCalled }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubSSHKeyService{}
			h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, true)
			r := newSSHKeysTestRouter(h)

			token := issueTestStepUpToken(t, uc.UserAccountID)

			var req *http.Request
			if tt.method == http.MethodPost {
				req = jsonRequest(t, tt.method, tt.path, registerSSHKeyRequest{PublicKey: "ssh-ed25519 AAAA"})
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			req.Header.Set("X-Step-Up-Token", token)
			req = withUC(req, uc)

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !tt.wantCalled(svc) {
				t.Error("expected service method to be called")
			}
		})
	}
}

func TestSSHKeysHandler_StepUp_SelfRoutes_FlagOff_NoTokenNeeded(t *testing.T) {
	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	req := jsonRequest(t, http.MethodPost, "/self/ssh-keys", registerSSHKeyRequest{PublicKey: "ssh-ed25519 AAAA"})
	req = withUC(req, uc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if !svc.registerCalled {
		t.Error("expected service.Register to be called")
	}
}

func TestSSHKeysHandler_OperatorRoutes_NeverStepUpGated(t *testing.T) {
	accountUUID := uuid.New()

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCalled func(*stubSSHKeyService) bool
	}{
		{"RegisterForAccount", http.MethodPost, "/user-accounts/" + accountUUID.String() + "/ssh-keys", http.StatusCreated, func(s *stubSSHKeyService) bool { return s.registerCalled }},
		{"RevokeForAccount", http.MethodDelete, "/user-accounts/" + accountUUID.String() + "/ssh-keys/" + uuid.New().String(), http.StatusNoContent, func(s *stubSSHKeyService) bool { return s.revokeCalled }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubSSHKeyService{}
			// stepUpRequired=true on the handler -- operator routes must
			// ignore it entirely; no X-Step-Up-Token header is sent.
			h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, true)
			r := newSSHKeysTestRouter(h)

			var req *http.Request
			if tt.method == http.MethodPost {
				req = jsonRequest(t, tt.method, tt.path, registerSSHKeyRequest{PublicKey: "ssh-ed25519 AAAA"})
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !tt.wantCalled(svc) {
				t.Error("expected service method to be called (operator routes are never step-up-gated)")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Status mapping
// ---------------------------------------------------------------------------

func TestSSHKeysHandler_StatusMapping_Forbidden(t *testing.T) {
	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{
		listFn: func(context.Context, uuid.UUID, int32, int32) ([]usersservice.SSHKey, int64, error) {
			return nil, 0, apiresp.ErrForbidden
		},
	}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	req := withUC(httptest.NewRequest(http.MethodGet, "/self/ssh-keys", nil), uc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403; body=%s", rec.Code, rec.Body.String())
	}
}

func TestSSHKeysHandler_StatusMapping_InvalidInput_PreservesDetailCode(t *testing.T) {
	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{
		registerFn: func(context.Context, uuid.UUID, string, *string, bool) (usersservice.SSHKey, error) {
			return usersservice.SSHKey{}, apiresp.InvalidInput(apiresp.FieldError{
				Field: "public_key", Code: "users.ssh_key_invalid", Message: "key is invalid",
			})
		},
	}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	req := jsonRequest(t, http.MethodPost, "/self/ssh-keys", registerSSHKeyRequest{PublicKey: "not-a-key"})
	req = withUC(req, uc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "users.ssh_key_invalid" {
		t.Errorf("detail code = %q, want users.ssh_key_invalid", code)
	}
}

func TestSSHKeysHandler_StatusMapping_Conflict(t *testing.T) {
	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{
		registerFn: func(context.Context, uuid.UUID, string, *string, bool) (usersservice.SSHKey, error) {
			return usersservice.SSHKey{}, apiresp.Conflict(apiresp.FieldError{
				Field: "public_key", Code: "users.ssh_key_in_use", Message: "already registered",
			})
		},
	}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	req := jsonRequest(t, http.MethodPost, "/self/ssh-keys", registerSSHKeyRequest{PublicKey: "ssh-ed25519 AAAA"})
	req = withUC(req, uc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "users.ssh_key_in_use" {
		t.Errorf("detail code = %q, want users.ssh_key_in_use", code)
	}
}

func TestSSHKeysHandler_RegisterSuccess_ReturnsDocumentedFieldsNoInternalIDs(t *testing.T) {
	uc := newTestUC(1, 10)
	wantUUID := uuid.New()
	svc := &stubSSHKeyService{
		registerFn: func(_ context.Context, accountUUID uuid.UUID, publicKeyLine string, label *string, stepUpUsed bool) (usersservice.SSHKey, error) {
			return usersservice.SSHKey{
				UUID:        wantUUID,
				KeyType:     "ssh-ed25519",
				Fingerprint: "SHA256:abc",
				PublicKey:   "ssh-ed25519 AAAA",
				Label:       "laptop",
			}, nil
		},
	}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	req := jsonRequest(t, http.MethodPost, "/self/ssh-keys", registerSSHKeyRequest{PublicKey: "ssh-ed25519 AAAA"})
	req = withUC(req, uc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	wantFields := []string{"uuid", "key_type", "fingerprint", "public_key", "label", "created_at"}
	for _, f := range wantFields {
		if _, ok := body[f]; !ok {
			t.Errorf("response missing documented field %q; body=%v", f, body)
		}
	}
	if body["uuid"] != wantUUID.String() {
		t.Errorf("uuid = %v, want %v", body["uuid"], wantUUID.String())
	}
	// No internal ids (AGENTS.md convention).
	for _, f := range []string{"id", "user_account_id", "account_holder"} {
		if _, ok := body[f]; ok {
			t.Errorf("response leaks internal field %q; body=%v", f, body)
		}
	}
}

func TestSSHKeysHandler_RevokeSuccess_Returns204(t *testing.T) {
	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	req := withUC(httptest.NewRequest(http.MethodDelete, "/self/ssh-keys/"+uuid.New().String(), nil), uc)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", rec.Body.String())
	}
	if !svc.revokeCalled {
		t.Error("expected service.Revoke to be called")
	}
}

// ---------------------------------------------------------------------------
// Malformed UUID path params
// ---------------------------------------------------------------------------

func TestSSHKeysHandler_MalformedUUID_Returns400(t *testing.T) {
	uc := newTestUC(1, 10)
	validAccount := uuid.New().String()

	tests := []struct {
		name   string
		method string
		path   string
		withUC bool
	}{
		{"self revoke: malformed key_uuid", http.MethodDelete, "/self/ssh-keys/not-a-uuid", true},
		{"operator list: malformed account uuid", http.MethodGet, "/user-accounts/not-a-uuid/ssh-keys", false},
		{"operator register: malformed account uuid", http.MethodPost, "/user-accounts/not-a-uuid/ssh-keys", false},
		{"operator revoke: malformed account uuid", http.MethodDelete, "/user-accounts/not-a-uuid/ssh-keys/" + uuid.New().String(), false},
		{"operator revoke: malformed key_uuid", http.MethodDelete, "/user-accounts/" + validAccount + "/ssh-keys/not-a-uuid", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &stubSSHKeyService{}
			h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
			r := newSSHKeysTestRouter(h)

			var req *http.Request
			if tt.method == http.MethodPost {
				req = jsonRequest(t, tt.method, tt.path, registerSSHKeyRequest{PublicKey: "ssh-ed25519 AAAA"})
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			if tt.withUC {
				req = withUC(req, uc)
			}

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
			}
			if svc.registerCalled || svc.listCalled || svc.revokeCalled {
				t.Error("service must not be called for a malformed uuid path param")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Oversized body (design note D6: cap at 32 KiB)
// ---------------------------------------------------------------------------

func TestSSHKeysHandler_OversizedBody_Returns400(t *testing.T) {
	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	oversized := strings.Repeat("a", 64*1024)
	body := fmt.Sprintf(`{"public_key":%q}`, oversized)

	req := httptest.NewRequest(http.MethodPost, "/self/ssh-keys", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withUC(req, uc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if svc.registerCalled {
		t.Error("service must not be called for an oversized body")
	}
}

// ---------------------------------------------------------------------------
// Malformed JSON / missing public_key
// ---------------------------------------------------------------------------

func TestSSHKeysHandler_MalformedJSON_Returns400(t *testing.T) {
	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	req := httptest.NewRequest(http.MethodPost, "/self/ssh-keys", strings.NewReader("{not json"))
	req.Header.Set("Content-Type", "application/json")
	req = withUC(req, uc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if svc.registerCalled {
		t.Error("service must not be called for malformed JSON")
	}
}

func TestSSHKeysHandler_MissingPublicKey_Returns400(t *testing.T) {
	uc := newTestUC(1, 10)
	svc := &stubSSHKeyService{}
	h := NewSSHKeysHandler(svc, stepUpTestSecret, &sync.Map{}, false)
	r := newSSHKeysTestRouter(h)

	req := jsonRequest(t, http.MethodPost, "/self/ssh-keys", registerSSHKeyRequest{})
	req = withUC(req, uc)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if svc.registerCalled {
		t.Error("service must not be called when public_key is missing")
	}
}
