package service

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/moduleforge/core-api/apiresp"
)

func TestCanonicalizeEmail(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"  Alice@Example.COM ": "alice@example.com",
		"bob@example.com":      "bob@example.com",
		"":                     "",
	}
	for in, want := range tests {
		if got := CanonicalizeEmail(in); got != want {
			t.Errorf("CanonicalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		ok    bool
	}{
		{"plain", "alice@example.com", true},
		{"empty", "", false},
		{"no domain", "alice", false},
		{"display name", "alice <alice@example.com>", false},
		{"too long", string(make([]byte, 300)) + "@example.com", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateEmail(tc.value)
			if tc.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.ok && !errors.Is(err, apiresp.ErrInvalidInput) {
				t.Fatalf("got %v, want invalid input", err)
			}
		})
	}
}

func TestHashToken(t *testing.T) {
	t.Parallel()
	h := HashToken("secret")
	if len(h) != 64 {
		t.Fatalf("hash length = %d, want 64 hex chars", len(h))
	}
	if h == HashToken("other") {
		t.Fatal("distinct tokens hashed equal")
	}
	if !TokenMatchesHash("secret", h) {
		t.Error("TokenMatchesHash rejected the right token")
	}
	if TokenMatchesHash("wrong", h) {
		t.Error("TokenMatchesHash accepted the wrong token")
	}
}

func TestMapUniqueViolation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		constraint string
		want       error
	}{
		{IdentifierKindValueIndex, ErrIdentifierTaken},
		{OIDCActiveIssuerSubjectIndex, ErrOIDCIdentityInUse},
		{SSHKeyActiveFingerprintIndex, ErrSSHKeyInUse},
		{ProfilesOneOpenPerHolderParty, ErrProfileExists},
	}
	for _, tc := range tests {
		err := mapUniqueViolation(&pgconn.PgError{Code: "23505", ConstraintName: tc.constraint})
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.constraint, err, tc.want)
		}
		if !errors.Is(err, apiresp.ErrConflict) {
			t.Errorf("%s: not a conflict: %v", tc.constraint, err)
		}
	}
	other := &pgconn.PgError{Code: "23505", ConstraintName: "some_other_uq"}
	if got := mapUniqueViolation(other); got != error(other) {
		t.Errorf("unmapped index must pass through, got %v", got)
	}
}

func TestIsPasswordExistsViolation(t *testing.T) {
	t.Parallel()
	for _, name := range []string{passwordActiveIdentifierIndex, passwordActiveUnboundIndex} {
		if !isPasswordExistsViolation(&pgconn.PgError{Code: "23505", ConstraintName: name}) {
			t.Errorf("%s not recognized", name)
		}
	}
	if isPasswordExistsViolation(&pgconn.PgError{Code: "23505", ConstraintName: "x"}) {
		t.Error("unrelated index recognized")
	}
	if isPasswordExistsViolation(errors.New("boom")) {
		t.Error("non-pg error recognized")
	}
}
