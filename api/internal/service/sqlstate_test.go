package service

import (
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestSQLStateCodes(t *testing.T) {
	want := map[string]string{
		SessionInvariantSQLState:    "MF101",
		CredentialInvariantSQLState: "MF102",
		UserInvariantSQLState:       "MF103",
	}
	for got, w := range want {
		if got != w {
			t.Errorf("SQLSTATE constant = %q, want %q", got, w)
		}
	}
}

func TestSQLStatePredicates(t *testing.T) {
	predicates := []struct {
		name string
		code string
		is   func(error) bool
	}{
		{"session", SessionInvariantSQLState, IsSessionInvariantError},
		{"credential", CredentialInvariantSQLState, IsCredentialInvariantError},
		{"user", UserInvariantSQLState, IsUserInvariantError},
	}
	all := []string{SessionInvariantSQLState, CredentialInvariantSQLState, UserInvariantSQLState}
	for _, p := range predicates {
		cases := []struct {
			name string
			err  error
			want bool
		}{
			{"nil", nil, false},
			{"plain", fmt.Errorf("boom"), false},
			{"unique violation", &pgconn.PgError{Code: "23505"}, false},
			{"mod-core code", &pgconn.PgError{Code: "MF004"}, false},
			{"direct", &pgconn.PgError{Code: p.code}, true},
			{"wrapped", fmt.Errorf("ctx: %w", &pgconn.PgError{Code: p.code}), true},
		}
		for _, other := range all {
			if other != p.code {
				cases = append(cases, struct {
					name string
					err  error
					want bool
				}{"other " + other, &pgconn.PgError{Code: other}, false})
			}
		}
		for _, c := range cases {
			if got := p.is(c.err); got != c.want {
				t.Errorf("%s/%s: got %v, want %v", p.name, c.name, got, c.want)
			}
		}
	}
}

func TestUniqueViolationCode(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		wantOK   bool
	}{
		{"nil", nil, "", false},
		{"plain", fmt.Errorf("boom"), "", false},
		{"identifier", &pgconn.PgError{Code: "23505", ConstraintName: "user_identifiers_kind_value_uq"}, "users.identifier_taken", true},
		{"oidc", &pgconn.PgError{Code: "23505", ConstraintName: "cred_oidc_active_issuer_subject_uq"}, "users.oidc_identity_in_use", true},
		{"ssh", &pgconn.PgError{Code: "23505", ConstraintName: "cred_ssh_key_active_fingerprint_uq"}, "users.ssh_key_in_use", true},
		{"profile", &pgconn.PgError{Code: "23505", ConstraintName: "profiles_one_open_per_holder_party"}, "users.profile_exists", true},
		{"wrapped", fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505", ConstraintName: "user_identifiers_kind_value_uq"}), "users.identifier_taken", true},
		{"unnamed index", &pgconn.PgError{Code: "23505", ConstraintName: "credentials_uuid_key"}, "", false},
		{"named index wrong code", &pgconn.PgError{Code: "23503", ConstraintName: "user_identifiers_kind_value_uq"}, "", false},
	}
	for _, c := range cases {
		code, ok := UniqueViolationCode(c.err)
		if code != c.wantCode || ok != c.wantOK {
			t.Errorf("%s: got (%q, %v), want (%q, %v)", c.name, code, ok, c.wantCode, c.wantOK)
		}
	}
}
