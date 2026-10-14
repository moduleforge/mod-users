package service

// State-conflict classification tests for UserAccountService.Update and the
// email unique-violation matcher shared with Create: a 23505 on
// user_accounts_email_key is the 409 users.email_taken conflict, wrapped or
// not; a 23505 on any other constraint stays an internal error.

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/moduleforge/core-api/apiresp"
	db "github.com/moduleforge/mod-users/model/db"
)

func updateWithExecErr(t *testing.T, execErr error) error {
	t.Helper()
	id := uuid.New()
	ua := db.UserAccount{ID: 20, Uuid: id, AccountHolder: 200, Email: pgtype.Text{String: "old@example.com", Valid: true}}
	ftx := &fakeTx{queryUA: ua, execErr: execErr, execErrSubstr: "UPDATE user_accounts"}
	svc := newUpgradeTestService(newStubUAQuerier(ua), ftx)
	_, err := svc.Update(authedCtx(), id, UpdateUserAccountInput{Email: ptr("taken@example.com")})
	return err
}

func TestUpdate_EmailUniqueViolation_IsEmailTaken(t *testing.T) {
	t.Parallel()

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: userAccountsEmailConstraint}
	err := updateWithExecErr(t, pgErr)

	assertFieldError(t, err, http.StatusConflict, "email", "users.email_taken")
	if err != ErrEmailTaken {
		t.Errorf("got %v, want ErrEmailTaken", err)
	}
}

// A unique violation on a different constraint is not an email conflict; it
// stays an internal (500-class) error and must not be reported as a 409.
func TestUpdate_OtherUniqueViolation_StaysInternal(t *testing.T) {
	t.Parallel()

	pgErr := &pgconn.PgError{Code: "23505", ConstraintName: "user_accounts_uuid_key"}
	err := updateWithExecErr(t, pgErr)

	if err == nil {
		t.Fatal("expected an error")
	}
	if errors.Is(err, apiresp.ErrConflict) {
		t.Errorf("got a conflict for a non-email constraint: %v", err)
	}
	var got *pgconn.PgError
	if !errors.As(err, &got) || got.ConstraintName != "user_accounts_uuid_key" {
		t.Errorf("underlying error not preserved: %v", err)
	}
}

func TestIsEmailTakenViolation(t *testing.T) {
	t.Parallel()

	email := &pgconn.PgError{Code: "23505", ConstraintName: userAccountsEmailConstraint}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"bare email violation", email, true},
		{"wrapped email violation", fmt.Errorf("user_accounts.Create user_account: %w", email), true},
		{"double wrapped", fmt.Errorf("txhelper: %w", fmt.Errorf("x: %w", email)), true},
		{"other constraint", &pgconn.PgError{Code: "23505", ConstraintName: "user_accounts_account_holder_key"}, false},
		{"wrapped other constraint", fmt.Errorf("w: %w", &pgconn.PgError{Code: "23505", ConstraintName: "entities_pkey"}), false},
		{"email constraint, other code", &pgconn.PgError{Code: "23502", ConstraintName: userAccountsEmailConstraint}, false},
		{"non-pg error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isEmailTakenViolation(c.err); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
