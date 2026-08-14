package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// sentinel handler that records whether it was called.
type captureHandler struct {
	called bool
}

func (h *captureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.called = true
	w.WriteHeader(http.StatusOK)
}

func TestRequireVerifiedEmail_MissingContext(t *testing.T) {
	t.Helper()

	next := &captureHandler{}
	mw := RequireVerifiedEmail(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/test", nil)
	// No UserContext on context — simulates middleware ordering mistake.
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rec.Code)
	}
	if next.called {
		t.Fatal("next handler must not be called when UserContext is missing")
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	// The specific "mounted before RequireAuth" text is server-logged only
	// via apiresp.WriteError's 5xx logging path, never returned to the
	// client — do not assert on it here.
	if body.Error.Code != "internal_error" {
		t.Errorf("expected error.code=internal_error, got %v", body.Error.Code)
	}
	if body.Error.Message != "an internal error occurred" {
		t.Errorf("expected error.message=%q, got %q", "an internal error occurred", body.Error.Message)
	}
}

func TestRequireVerifiedEmail_Unverified(t *testing.T) {
	next := &captureHandler{}
	mw := RequireVerifiedEmail(next)

	uc := &UserContext{
		UserAccountID:   1,
		UserUUID:        "00000000-0000-0000-0000-000000000001",
		EntityID:        10,
		Email:           "user@example.com",
		EmailVerifiedAt: nil, // not verified
	}

	req := httptest.NewRequest(http.MethodPut, "/v1/self", nil)
	req = req.WithContext(WithUserContext(req.Context(), uc))
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
	if next.called {
		t.Fatal("next handler must not be called for unverified account")
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	if _, ok := raw["action"]; !ok {
		t.Fatalf("expected top-level 'action' member, got %v", raw)
	}
	if _, ok := raw["error"]; ok {
		t.Errorf("expected no top-level 'error' member, got %v", raw)
	}

	action, ok := raw["action"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'action' to be an object, got %T", raw["action"])
	}

	// Stable wire contract — Phase 5 GUI depends on these exact values.
	if action["code"] != "users.email_unverified" {
		t.Errorf("expected action.code=users.email_unverified, got %v", action["code"])
	}
	if action["message"] != "Verify your email address before continuing." {
		t.Errorf("unexpected action.message: %v", action["message"])
	}
	if action["path"] != "/verify-email" {
		t.Errorf("unexpected action.path: %v", action["path"])
	}
	if _, ok := action["data"]; ok {
		t.Errorf("expected no action.data member when data is nil, got %v", action["data"])
	}
}

// TestRequireVerifiedEmail_AnonymousAccount confirms that an anonymous account
// (no email, no EmailVerifiedAt) is rejected with 403 email_unverified by the
// existing RequireVerifiedEmail middleware. Anonymous users are excluded from
// all /v1/self endpoints naturally by this check — no separate
// RequireNamedAccount middleware is needed.
func TestRequireVerifiedEmail_AnonymousAccount(t *testing.T) {
	next := &captureHandler{}
	mw := RequireVerifiedEmail(next)

	// Anonymous accounts have an empty Email and a nil EmailVerifiedAt.
	uc := &UserContext{
		UserAccountID:   2,
		UserUUID:        "00000000-0000-0000-0000-000000000002",
		EntityID:        20,
		Email:           "", // no email address
		EmailVerifiedAt: nil,
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/self/credential/step-up", nil)
	req = req.WithContext(WithUserContext(req.Context(), uc))
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for anonymous account, got %d", rec.Code)
	}
	if next.called {
		t.Fatal("next handler must not be called for anonymous account")
	}

	var raw map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&raw); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	action, ok := raw["action"].(map[string]any)
	if !ok {
		t.Fatalf("expected 'action' to be an object, got %T", raw["action"])
	}
	if action["code"] != "users.email_unverified" {
		t.Errorf("expected action.code=users.email_unverified, got %v", action["code"])
	}
}

func TestRequireVerifiedEmail_Verified(t *testing.T) {
	next := &captureHandler{}
	mw := RequireVerifiedEmail(next)

	now := time.Now()
	uc := &UserContext{
		UserAccountID:   1,
		UserUUID:        "00000000-0000-0000-0000-000000000001",
		EntityID:        10,
		Email:           "user@example.com",
		EmailVerifiedAt: &now,
	}

	req := httptest.NewRequest(http.MethodPut, "/v1/self", nil)
	req = req.WithContext(WithUserContext(req.Context(), uc))
	rec := httptest.NewRecorder()

	mw.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !next.called {
		t.Fatal("next handler must be called for verified account")
	}
}
