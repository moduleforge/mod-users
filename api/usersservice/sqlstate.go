package usersservice

import inner "github.com/moduleforge/mod-users/api/internal/service"

// SQLSTATE codes raised by the mod_users schema, and their predicates.
const (
	// SessionInvariantSQLState (MF101): session invariant violation.
	SessionInvariantSQLState = inner.SessionInvariantSQLState
	// CredentialInvariantSQLState (MF102): credential invariant violation.
	CredentialInvariantSQLState = inner.CredentialInvariantSQLState
	// UserInvariantSQLState (MF103): user invariant violation.
	UserInvariantSQLState = inner.UserInvariantSQLState
)

// IsSessionInvariantError reports whether err is (or wraps) an MF101 error.
func IsSessionInvariantError(err error) bool { return inner.IsSessionInvariantError(err) }

// IsCredentialInvariantError reports whether err is (or wraps) an MF102 error.
func IsCredentialInvariantError(err error) bool { return inner.IsCredentialInvariantError(err) }

// IsUserInvariantError reports whether err is (or wraps) an MF103 error.
func IsUserInvariantError(err error) bool { return inner.IsUserInvariantError(err) }

// UniqueViolationCode maps a unique violation on one of the named unique
// indexes (user_identifiers_kind_value_uq, cred_oidc_active_issuer_subject_uq,
// cred_ssh_key_active_fingerprint_uq, profiles_one_open_per_holder_party) to
// its 409 error code (users.identifier_taken, users.oidc_identity_in_use,
// users.ssh_key_in_use, users.profile_exists).
func UniqueViolationCode(err error) (string, bool) { return inner.UniqueViolationCode(err) }
