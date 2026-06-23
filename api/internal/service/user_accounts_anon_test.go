package service

// Unit tests for CreateAnonymousUser service logic and the new sentinel errors.
//
// These tests cover only the pure-logic aspects (input validation, error
// sentinel identity) that do not require a running Postgres. Transaction-
// dependent behavior (token generation, anon_tokens insert, cascade delete)
// is covered by integration tests.

import (
	"errors"
	"testing"
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
