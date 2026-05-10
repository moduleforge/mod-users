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

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "internal_error" {
		t.Errorf("expected error=internal_error, got %v", body["error"])
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

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}

	// Stable wire contract — Phase 5 GUI depends on these exact values.
	if body["error"] != "email_unverified" {
		t.Errorf("expected error=email_unverified, got %v", body["error"])
	}
	if body["message"] != "Verify your email address before continuing." {
		t.Errorf("unexpected message: %v", body["message"])
	}
	if body["verify_path"] != "/v1/auth/email-code/request" {
		t.Errorf("unexpected verify_path: %v", body["verify_path"])
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
