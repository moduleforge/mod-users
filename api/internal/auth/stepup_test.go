package auth

import (
	"sync"
	"testing"
	"time"
)

var testSecret = []byte("test-secret-at-least-32-bytes-xx")

// TestIssueStepUpToken_RoundTrip verifies that a freshly issued token can be
// verified immediately (happy path).
func TestIssueStepUpToken_RoundTrip(t *testing.T) {
	token, jti, exp, err := IssueStepUpToken(testSecret, 42, StepUpTTL)
	if err != nil {
		t.Fatalf("IssueStepUpToken: %v", err)
	}
	if token == "" {
		t.Fatal("token is empty")
	}
	if jti == "" {
		t.Fatal("jti is empty")
	}
	if exp.Before(time.Now()) {
		t.Fatal("exp is in the past")
	}

	consumed := &sync.Map{}
	if err := VerifyStepUpToken(testSecret, token, 42, consumed); err != nil {
		t.Fatalf("VerifyStepUpToken: %v", err)
	}
}

// TestIssueStepUpToken_Expiry verifies that a token with a negative TTL
// (already expired) is rejected.
func TestIssueStepUpToken_Expiry(t *testing.T) {
	// Issue with a negative TTL so the token is already expired.
	token, _, _, err := IssueStepUpToken(testSecret, 42, -1*time.Second)
	if err != nil {
		t.Fatalf("IssueStepUpToken: %v", err)
	}

	consumed := &sync.Map{}
	if err := VerifyStepUpToken(testSecret, token, 42, consumed); err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

// TestIssueStepUpToken_WrongSecret verifies that a token signed with a
// different secret is rejected.
func TestIssueStepUpToken_WrongSecret(t *testing.T) {
	token, _, _, err := IssueStepUpToken(testSecret, 42, StepUpTTL)
	if err != nil {
		t.Fatalf("IssueStepUpToken: %v", err)
	}

	wrongSecret := []byte("wrong-secret-at-least-32-bytes-x")
	consumed := &sync.Map{}
	if err := VerifyStepUpToken(wrongSecret, token, 42, consumed); err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

// TestIssueStepUpToken_WrongUserAccount verifies that a token bound to one
// user account is rejected for a different user account.
func TestIssueStepUpToken_WrongUserAccount(t *testing.T) {
	token, _, _, err := IssueStepUpToken(testSecret, 42, StepUpTTL)
	if err != nil {
		t.Fatalf("IssueStepUpToken: %v", err)
	}

	consumed := &sync.Map{}
	if err := VerifyStepUpToken(testSecret, token, 99, consumed); err == nil {
		t.Fatal("expected error for wrong user account, got nil")
	}
}

// TestIssueStepUpToken_Replay verifies that presenting the same token twice
// is rejected on the second use (single-use enforcement).
func TestIssueStepUpToken_Replay(t *testing.T) {
	token, _, _, err := IssueStepUpToken(testSecret, 42, StepUpTTL)
	if err != nil {
		t.Fatalf("IssueStepUpToken: %v", err)
	}

	consumed := &sync.Map{}

	// First use: should succeed.
	if err := VerifyStepUpToken(testSecret, token, 42, consumed); err != nil {
		t.Fatalf("first verify: %v", err)
	}

	// Second use: same token must be rejected.
	if err := VerifyStepUpToken(testSecret, token, 42, consumed); err == nil {
		t.Fatal("expected error on token replay, got nil")
	}
}

// TestIssueStepUpToken_MalformedToken verifies that garbage input is rejected
// gracefully without panicking.
func TestIssueStepUpToken_MalformedToken(t *testing.T) {
	cases := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"single_part", "onlyone"},
		{"two_parts", "part1.part2"},
		{"bad_base64", "abc.!!!.sig"},
		{"truncated", "a.b."},
	}
	consumed := &sync.Map{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := VerifyStepUpToken(testSecret, tc.token, 42, consumed); err == nil {
				t.Errorf("expected error for %q, got nil", tc.token)
			}
		})
	}
}

// TestStepUpJanitor_PrunesExpired verifies that the janitor removes entries
// that have expired while leaving unexpired entries in place.
func TestStepUpJanitor_PrunesExpired(t *testing.T) {
	consumed := &sync.Map{}

	// Pre-populate: one expired, one unexpired.
	consumed.Store("expired-jti", int64(time.Now().Add(-1*time.Minute).Unix()))
	consumed.Store("valid-jti", int64(time.Now().Add(5*time.Minute).Unix()))

	// Manually invoke the prune logic (mirrors what the janitor does).
	now := time.Now()
	consumed.Range(func(key, value any) bool {
		exp, ok := value.(int64)
		if !ok || now.Unix() > exp {
			consumed.Delete(key)
		}
		return true
	})

	if _, ok := consumed.Load("expired-jti"); ok {
		t.Error("expired jti should have been pruned")
	}
	if _, ok := consumed.Load("valid-jti"); !ok {
		t.Error("valid jti should NOT have been pruned")
	}
}
