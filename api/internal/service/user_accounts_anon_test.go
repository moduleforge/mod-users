package service

// Unit tests for CreateAnonymousUser service logic and the new sentinel errors.
//
// These tests cover only the pure-logic aspects (input validation, error
// sentinel identity) that do not require a running Postgres. Transaction-
// dependent behavior (token generation, anon_tokens insert, cascade delete)
// is covered by integration tests.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moduleforge/core-api/apiresp"
)

func TestCreateAnonymousUserInput_Validation(t *testing.T) {
	t.Parallel()

	// Build a minimal UserAccountService; all fields are nil because the
	// DeviceID check happens before any field is touched.
	svc := &UserAccountService{}

	_, err := svc.CreateAnonymousUser(t.Context(), CreateAnonymousUserInput{
		DeviceID: "",
	})
	if err == nil {
		t.Fatal("expected error for empty DeviceID, got nil")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got: %v", err)
	}
}

func TestCreateAnonymousUserInput_BlankDeviceID(t *testing.T) {
	t.Parallel()

	svc := &UserAccountService{}

	_, err := svc.CreateAnonymousUser(t.Context(), CreateAnonymousUserInput{
		DeviceID: "   ", // whitespace only
	})
	if err == nil {
		t.Fatal("expected error for whitespace-only DeviceID, got nil")
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Errorf("expected ErrInvalidInput, got: %v", err)
	}
}

func TestErrAnonymousAccount_SentinelIdentity(t *testing.T) {
	t.Parallel()

	// ErrAnonymousAccount must be a distinct, non-nil sentinel.
	if ErrAnonymousAccount == nil {
		t.Fatal("ErrAnonymousAccount is nil")
	}
	if errors.Is(ErrAnonymousAccount, ErrInvalidInput) {
		t.Error("ErrAnonymousAccount must not wrap ErrInvalidInput")
	}
	if errors.Is(ErrAnonymousAccount, ErrEmailTaken) {
		t.Error("ErrAnonymousAccount must not wrap ErrEmailTaken")
	}
}

// TestErrEmailTaken_ConflictDetail asserts, at the point of definition, that
// ErrEmailTaken (built via apiresp.Conflict) classifies as apiresp.ErrConflict
// and carries the users.email_taken field-level detail directly on the
// sentinel — the mechanical crux of the ZVum fold-in. apiresp exposes no
// public detail-carrying interface to recover the details via errors.As, so
// this re-derives them the same way a real caller would: routing the
// sentinel through apiresp.WriteError and decoding the resulting envelope.
func TestErrEmailTaken_ConflictDetail(t *testing.T) {
	t.Parallel()

	if !errors.Is(ErrEmailTaken, apiresp.ErrConflict) {
		t.Fatal("ErrEmailTaken must wrap apiresp.ErrConflict")
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/user-accounts", nil)
	rr := httptest.NewRecorder()
	apiresp.WriteError(rr, req, ErrEmailTaken)

	if rr.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want %d", rr.Code, http.StatusConflict)
	}

	var body struct {
		Error struct {
			Code    string               `json:"code"`
			Message string               `json:"message"`
			Details []apiresp.FieldError `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body not valid JSON: %v, body=%s", err, rr.Body.String())
	}

	if body.Error.Code != "conflict" {
		t.Errorf("error.code: got %q, want %q", body.Error.Code, "conflict")
	}

	wantDetails := []apiresp.FieldError{
		{Field: "email", Code: "users.email_taken", Message: "email is already registered"},
	}
	if len(body.Error.Details) != len(wantDetails) {
		t.Fatalf("error.details: got %+v, want %+v", body.Error.Details, wantDetails)
	}
	for i, want := range wantDetails {
		if body.Error.Details[i] != want {
			t.Errorf("error.details[%d]: got %+v, want %+v", i, body.Error.Details[i], want)
		}
	}
}
