package service

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// SQLSTATE codes raised by the mod_users schema (model/migrations/sql/0100_baseline.sql).
// They sit in mod-users' own migration range, apart from mod-core's MF0xx codes.
const (
	// SessionInvariantSQLState is raised for a session invariant violation:
	// impersonation nesting, an impersonated profile the principal does not
	// hold, an immutable column change, a non-user principal, or a session
	// factor whose credential belongs to another principal.
	SessionInvariantSQLState = "MF101"

	// CredentialInvariantSQLState is raised for a credential invariant
	// violation: a kind not allowed for the principal type or guest status, an
	// immutable column change, a default profile the principal does not hold,
	// a mismatched cred_<kind> row, or an identifier of another principal.
	CredentialInvariantSQLState = "MF102"

	// UserInvariantSQLState is raised for a user invariant violation: type,
	// owner, managed-user and guest profile rules, an immutable column change,
	// or a default profile the user does not hold.
	UserInvariantSQLState = "MF103"
)

// uniqueViolationSQLState is the standard Postgres code for a unique
// violation.
const uniqueViolationSQLState = "23505"

// Named unique indexes whose violations map to a 409. The names are part of
// the schema contract.
const (
	IdentifierKindValueIndex      = "user_identifiers_kind_value_uq"
	OIDCActiveIssuerSubjectIndex  = "cred_oidc_active_issuer_subject_uq"
	SSHKeyActiveFingerprintIndex  = "cred_ssh_key_active_fingerprint_uq"
	ProfilesOneOpenPerHolderParty = "profiles_one_open_per_holder_party" // mod-core's index
)

// Error codes (the `code` of the 409 response) for the named unique indexes.
const (
	CodeIdentifierTaken   = "users.identifier_taken"
	CodeOIDCIdentityInUse = "users.oidc_identity_in_use"
	CodeSSHKeyInUse       = "users.ssh_key_in_use"
	CodeProfileExists     = "users.profile_exists"
)

// uniqueIndexCodes maps a violated unique index to its 409 code.
var uniqueIndexCodes = map[string]string{
	IdentifierKindValueIndex:      CodeIdentifierTaken,
	OIDCActiveIssuerSubjectIndex:  CodeOIDCIdentityInUse,
	SSHKeyActiveFingerprintIndex:  CodeSSHKeyInUse,
	ProfilesOneOpenPerHolderParty: CodeProfileExists,
}

// IsSessionInvariantError reports whether err is (or wraps) the Postgres error
// raised for a session invariant violation (MF101).
func IsSessionInvariantError(err error) bool {
	return hasSQLState(err, SessionInvariantSQLState)
}

// IsCredentialInvariantError reports whether err is (or wraps) the Postgres
// error raised for a credential invariant violation (MF102).
func IsCredentialInvariantError(err error) bool {
	return hasSQLState(err, CredentialInvariantSQLState)
}

// IsUserInvariantError reports whether err is (or wraps) the Postgres error
// raised for a user invariant violation (MF103).
func IsUserInvariantError(err error) bool {
	return hasSQLState(err, UserInvariantSQLState)
}

// UniqueViolationCode maps a unique violation (23505) on one of the named
// unique indexes to its 409 error code. It reports false for any other error,
// including a unique violation on an unnamed index; the caller then treats the
// error as unmapped.
func UniqueViolationCode(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != uniqueViolationSQLState {
		return "", false
	}
	code, ok := uniqueIndexCodes[pgErr.ConstraintName]
	return code, ok
}

func hasSQLState(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
